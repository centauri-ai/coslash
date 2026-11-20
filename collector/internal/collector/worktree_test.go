package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestLinkedCheckoutInSessionList(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "worktrees.sqlite")
	if err := session.UseLocationMemory(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.UseLocationMemory("") })
	main := filepath.Join(dir, "project")
	work := filepath.Join(dir, "checkout")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s %v", args, out, err)
		}
	}
	run("init", "-q", main)
	run("-C", main, "-c", "user.email=test@example.org", "-c", "user.name=test", "commit", "--allow-empty", "-qm", "init")
	run("-C", main, "remote", "add", "origin", "https://example.org/team/project.git")
	run("-C", main, "worktree", "add", "-qb", "feature", work)
	sub := filepath.Join(work, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	rows := []*vendors.ParsedSession{
		{Session: &session.Session{Agent: "codex", ID: "one", WorkingDirectory: work}},
		{Session: &session.Session{Agent: "codex", ID: "two", WorkingDirectory: sub}},
		{Session: &session.Session{Agent: "codex", ID: "three", WorkingDirectory: filepath.Join(dir, ".superset", "worktrees", "fake")}},
	}
	if err := probeGitEnvironmentContext(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	var id string
	for i, row := range rows {
		data, err := json.Marshal(row.Session)
		if err != nil {
			t.Fatal(err)
		}
		var displayed struct {
			Locations []struct{ ID, Label, Repo, Head string }
			Branch    *string
		}
		if err := json.Unmarshal(data, &displayed); err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			if len(displayed.Locations) != 0 {
				t.Fatal("inferred checkout from path")
			}
			continue
		}
		if len(displayed.Locations) != 1 || displayed.Locations[0].Label != "checkout" || displayed.Locations[0].Repo != "example.org/team/project" || displayed.Locations[0].Head != "feature" || displayed.Branch != nil {
			t.Fatalf("session %d: %+v", i, displayed)
		}
		if i == 0 {
			id = displayed.Locations[0].ID
		}
		if id == "" || displayed.Locations[0].ID != id {
			t.Fatal("subdirectory checkout identity differs")
		}
	}

	probe := func(key, cwd string, at int64) *session.Session {
		t.Helper()
		value := &session.Session{ID: key, Agent: "codex", WorkingDirectory: cwd, LastActivityTime: at}
		if err := probeGitEnvironmentContext(t.Context(), []*vendors.ParsedSession{{Session: value}}); err != nil {
			t.Fatal(err)
		}
		return value
	}
	run("-C", work, "checkout", "-qb", "next")
	if l := probe("one", work, 0).Locations[0]; l.ID != id || l.Head != "next" {
		t.Fatalf("switched branch: %+v", l)
	}
	moved := filepath.Join(dir, "renamed")
	run("-C", main, "worktree", "move", work, moved)
	if err := session.UseLocationMemory(db); err != nil {
		t.Fatal(err)
	}
	if l := probe("one", work, 0).Locations[0]; l.ID != id || l.Label != "renamed" || l.Availability != "available" {
		t.Fatalf("moved: %+v", l)
	}
	run("-C", main, "worktree", "remove", "--force", moved)
	if l := probe("one", work, 0).Locations[0]; l.ID != id || l.Availability != "removed" {
		t.Fatalf("removed: %+v", l)
	}
	run("-C", main, "worktree", "add", "--detach", work)
	if l := probe("new", work, 1).Locations[0]; l.ID == id || l.HeadKind != "detached" {
		t.Fatalf("new checkout: %+v", l)
	}
	if l := probe("one", work, 0).Locations; l[0].ID != id || len(l) != 1 {
		t.Fatalf("old session adopted replacement: %+v", l)
	}
	if l := probe("one", main, 2).Locations; len(l) != 2 || !l[0].Main || !l[0].Primary || l[1].ID != id || l[1].Primary {
		t.Fatalf("resumed across checkouts: %+v", l)
	}
	run("-C", main, "remote", "remove", "origin")
	unrelated := "example.org/unrelated/project"
	record := &session.Session{ID: "independent-origin", Agent: "codex", WorkingDirectory: work, Repository: &unrelated, LastActivityTime: 3}
	if err := probeGitEnvironmentContext(t.Context(), []*vendors.ParsedSession{{Session: record}}); err != nil {
		t.Fatal(err)
	}
	if len(record.Locations) != 1 || record.Locations[0].Repo != "" || record.Repository == nil || *record.Repository != unrelated {
		t.Fatalf("Git checkout and independently recorded remote were conflated: %+v, repo=%v", record.Locations, record.Repository)
	}
	run("-C", main, "remote", "add", "origin", "https://example.org/new/project.git")
	changed := probe("independent-origin", work, 3).Locations
	if len(changed) != 2 || changed[0].Repo != "example.org/new/project" || changed[1].Repo != "" || changed[0].ID != changed[1].ID {
		t.Fatalf("unchanged session did not retain checkout history across a remote change: %+v", changed)
	}

	// Reopen a database produced by the earlier collector. Its stored repo has no
	// provenance, so the next Git probe corrects the current parent while retaining history.
	legacy, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(`DROP TABLE tree_origins`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`UPDATE links SET repo=? WHERE session=? AND repo=''`, unrelated, record.ID); err != nil {
		t.Fatal(err)
	}
	if err := session.UseLocationMemory(db); err != nil {
		t.Fatal(err)
	}
	run("-C", main, "remote", "remove", "origin")
	recovered := probe(record.ID, work, 3).Locations
	if len(recovered) != 3 || !recovered[0].Primary || recovered[0].Repo != "" {
		t.Fatalf("fresh Git evidence did not correct the legacy checkout parent: %+v", recovered)
	}
	history := map[string]bool{}
	for _, location := range recovered[1:] {
		history[location.Repo] = true
	}
	if !history[unrelated] || !history["example.org/new/project"] {
		t.Fatalf("migration lost checkout history: %+v", recovered)
	}

}
