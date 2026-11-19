package search

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func openTest(t *testing.T) *Index {
	t.Helper()
	i, err := Open(filepath.Join(t.TempDir(), "search", "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { i.Close() })
	i.SetState("ready", 0)
	return i
}
func put(t *testing.T, i *Index, source string, s session.Session, coverage string) {
	t.Helper()
	if err := i.Upsert(context.Background(), source, s, coverage); err != nil {
		t.Fatal(err)
	}
}
func find(t *testing.T, i *Index, q string, opts Options) Response {
	t.Helper()
	r, err := i.Search(context.Background(), q, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func str(s string) *string { return &s }
func fixture(id, text string) session.Session {
	return session.Session{ID: id, Agent: "claude", DetailRevision: "r1", Name: str(id), SessionDetails: session.SessionDetails{Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestUser, Description: text, SearchText: text}}}}
}
func TestQuery(t *testing.T) {
	cases := []struct {
		raw    string
		terms  []string
		intent bool
	}{
		{`repo:demo agent:claude machine:x group:g status:idle since:123 why did we choose nonprod.98`, []string{"nonprod 98"}, true},
		{`"merge queue" src/foo.ts #750`, []string{"merge queue", "src foo ts", "750"}, false},
		{`why did we`, []string{"why", "did", "we"}, true},
		{`repo:demo`, nil, false},
		{`alpha OR NOT NEAR * "beta`, []string{"alpha", "near", "beta"}, false},
		{`one two three four five six seven eight nine ten`, []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}, false},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			q := ParseQuery(c.raw)
			if !reflect.DeepEqual(q.Terms, c.terms) || q.DecisionIntent != c.intent {
				t.Fatalf("%+v", q)
			}
		})
	}
	i := openTest(t)
	put(t, i, "local", fixture("escape", "alpha near beta"), "full")
	for _, raw := range []string{`alpha OR NOT NEAR * "beta`, `"`, `***`, `repo:x`} {
		_, err := i.Search(context.Background(), raw, Options{})
		if err != nil && err != ErrEmptyQuery {
			t.Fatalf("%q: %v", raw, err)
		}
	}
}
func TestIncrementalOverlayAndCoverage(t *testing.T) {
	i := openTest(t)
	s := fixture("same", "display cut")
	s.Digest[0].SearchText = "hidden full transcript needle"
	put(t, i, "local", s, "full")
	var before int
	if err := i.db.QueryRow("SELECT id FROM units WHERE kind='prompt'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	s.Digest[0].SearchText = ""
	s.Synthesis = &session.SessionSynthesis{KeyDecisions: []string{"durable overlay choice"}}
	s.Commits = []string{"commitneedle"}
	put(t, i, "local", s, "partial")
	var after int
	if err := i.db.QueryRow("SELECT id FROM units WHERE kind='prompt'").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("overlay replaced prompt")
	}
	for _, term := range []string{"needle", "choice", "commitneedle"} {
		r := find(t, i, term, Options{})
		if len(r.Sessions) != 1 || r.Sessions[0].Coverage != "full" {
			t.Fatalf("%s: %+v", term, r)
		}
	}
	s.DetailRevision = "r2"
	s.Digest[0].SearchText = "replacement"
	s.Synthesis = nil
	s.Commits = nil
	put(t, i, "local", s, "partial")
	if len(find(t, i, "needle", Options{}).Sessions) != 0 {
		t.Fatal("old content retained after revision")
	}
}
func TestIdentityFiltersAndLifecycle(t *testing.T) {
	i := openTest(t)
	s := fixture("same", "orbital cache")
	s.Repository = str("wanted")
	s.LastActivityTime = 100
	put(t, i, "local", s, "full")
	s.Agent = "codex"
	s.Repository = str("other")
	put(t, i, "ssh1", s, "record")
	r := find(t, i, "orbital", Options{})
	if len(r.Sessions) != 2 || len(r.Indexed) != 1 {
		t.Fatalf("%+v", r)
	}
	r = find(t, i, "orbital", Options{Repo: "wanted", Agent: "claude", Since: 100, Limit: 1})
	if len(r.Sessions) != 1 || r.Sessions[0].SourceID != "local" {
		t.Fatalf("%+v", r)
	}
	if len(find(t, i, "orbital", Options{Since: 101}).Sessions) != 0 {
		t.Fatal("since filter ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := i.Reconcile(ctx, "local", nil, 0); err == nil {
		t.Fatal("cancellation ignored")
	}
	if len(find(t, i, "orbital", Options{}).Sessions) != 2 {
		t.Fatal("interrupted pass deleted rows")
	}
	if err := i.ReconcileSources(context.Background(), []string{"local"}); err != nil {
		t.Fatal(err)
	}
	if len(find(t, i, "orbital", Options{}).Sessions) != 1 {
		t.Fatal("removed source retained")
	}
	if err := i.Reconcile(context.Background(), "local", nil, 101); err != nil {
		t.Fatal(err)
	}
	if len(find(t, i, "orbital", Options{}).Sessions) != 1 {
		t.Fatal("a pass over a later window deleted an older session")
	}
	if err := i.Reconcile(context.Background(), "local", nil, 100); err != nil {
		t.Fatal(err)
	}
	if len(find(t, i, "orbital", Options{}).Sessions) != 0 {
		t.Fatal("complete pass did not delete")
	}
}
func TestExistingDecisionsAreSearchableWithoutCitations(t *testing.T) {
	i := openTest(t)
	s := fixture("legacy", "")
	s.Name = nil
	s.Synthesis = &session.SessionSynthesis{KeyDecisions: []string{"merge queue commits", "merge queue policy", "merge queue strategy"}}
	put(t, i, "local", s, "full")
	r := find(t, i, "why merge queue", Options{})
	if len(r.Sessions) != 1 || len(r.Decisions) != 2 {
		t.Fatalf("decision grouping and cap: %+v", r)
	}
	for _, d := range r.Decisions {
		if d.Cited || len(d.Turns) != 0 || d.DecidedBy != "" || d.Kind != "decision" || d.Status != "decided" {
			t.Fatalf("invented decision metadata: %+v", d)
		}
	}
	s.Synthesis = nil
	put(t, i, "local", s, "full")
	if r := find(t, i, "merge queue", Options{}); len(r.Decisions) != 0 || len(r.Sessions) != 0 {
		t.Fatalf("removed synthesis retained in index: %+v", r)
	}
}
func TestSnippetRanges(t *testing.T) {
	m := makeMatch("prompt", 2, "😀 use \x01merge\x02 queue")
	if m.Snippet != "😀 use merge queue" || !reflect.DeepEqual(m.Ranges, [][2]int{{6, 11}}) {
		t.Fatalf("%+v", m)
	}
}
func TestReopenAndRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	i, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err = i.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %q: %v", mode, err)
	}
	put(t, i, "local", fixture("restart", "persistentword"), "full")
	i.Close()
	i, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(find(t, i, "persistentword", Options{}).Sessions) != 1 {
		t.Fatal("restart lost index")
	}
	i.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("PRAGMA user_version=999")
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	i, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(find(t, i, "persistentword", Options{}).Sessions) != 0 {
		t.Fatal("schema mismatch retained rows")
	}
	i.Close()
	if err = os.WriteFile(path, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	i, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	i.Close()
	if _, err = Open(t.TempDir()); err == nil {
		t.Fatal("directory accepted as index")
	}
}
func TestSyntheticRecall(t *testing.T) {
	i := openTest(t)
	answers := []string{"nonprod.98 release sequencing", "merge queue uses merge commits", "promoted staging artifacts", "retry bounded exponential backoff", "offline SQLite cache", "SSH full records stored locally", "search prompts keep full text", "port forwarding loopback token", "worktree paths preserve identity", "decision evidence cites turns"}
	questions := []string{"nonprod.98", "why merge queue commits", "promote staging", "what retry backoff", "why offline SQLite", "SSH records locally", "why prompts full text", "loopback token", "worktree identity", "decision evidence turns"}
	for n, text := range answers {
		s := fixture(text, text)
		if n == 1 {
			s.Digest = nil
			s.Synthesis = &session.SessionSynthesis{KeyDecisions: []string{text}}
		}
		put(t, i, "local", s, "full")
	}
	for n := range 20 {
		put(t, i, "local", fixture(strings.Repeat("x", n+1), "routine unrelated maintenance housekeeping chores"), "full")
	}
	for n, q := range questions {
		r := find(t, i, q, Options{})
		found := false
		for rank, s := range r.Sessions {
			if rank < 3 && s.SessionID == answers[n] {
				found = true
			}
		}
		if !found {
			t.Errorf("question %q missed top 3: %+v", q, r.Sessions)
		}
	}
}

func TestCoverageDemotesSingleTermDecoy(t *testing.T) {
	i := openTest(t)
	put(t, i, "local", fixture("answer", "merge queue commits graphite"), "full")
	put(t, i, "local", fixture("decoy", "graphite"), "full")
	for n := range 12 {
		put(t, i, "local", fixture(strings.Repeat("n", n+1), "merge queue commits unrelated"), "full")
	}
	r := find(t, i, "merge queue commits graphite", Options{})
	answer, decoy := -1, -1
	for rank, h := range r.Sessions {
		if h.SessionID == "answer" {
			answer = rank
		}
		if h.SessionID == "decoy" {
			decoy = rank
		}
	}
	if answer < 0 || answer >= decoy {
		t.Fatalf("answer rank %d, decoy %d: %+v", answer, decoy, r.Sessions)
	}
}
