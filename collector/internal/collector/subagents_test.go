package collector

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
)

func TestCursorSubagentStatusPreservesInTurnFallback(t *testing.T) {
	child := &vendors.ParsedSession{
		Session: &session.Session{Agent: vendors.AgentCursor, ID: "child"},
		InTurn:  true,
	}
	parent := &vendors.ParsedSession{
		Session: &session.Session{Agent: vendors.AgentCursor, ID: "parent"},
		Spawns:  map[string]vendors.SpawnState{},
	}

	if got := subagentStatus(child, parent, vendors.EmptySessionMetadata(), false); got != session.SubagentRunning {
		t.Fatalf("status = %q, want %q", got, session.SubagentRunning)
	}
}

func TestGrokSubagentsNestOnceWithMetaStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	group := filepath.Join(home, "sessions", "%2Fwork%2Frepo")
	write := func(path, body string) { writeGrokFile(t, path, body) }
	write(filepath.Join(group, "parent", "summary.json"), `{"info":{"id":"parent","cwd":"/work/repo"},"chat_format_version":1}`)
	for _, child := range []struct{ id, status string }{{"done-child", "completed"}, {"failed-child", "failed"}} {
		write(filepath.Join(group, child.id, "summary.json"),
			`{"info":{"id":"`+child.id+`","cwd":"/work/repo"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"parent"}`)
		meta := filepath.Join(group, "parent", "subagents", "sub-"+child.id)
		write(filepath.Join(meta, "meta.json"), `{"child_session_id":"`+child.id+`","status":"`+child.status+
			`","description":"Check `+child.id+`","prompt":"Look at it","duration_ms":1200,"tool_calls":3}`)
		write(filepath.Join(meta, "output.json"), `{"schema_version":1,"output":"done"}`)
	}

	parsed, metadata, err := grok.CollectContext(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	roots := finalizeSessions(parsed, map[string]*vendors.SessionMetadata{vendors.AgentGrok: metadata})
	if len(roots) != 1 || roots[0].Session.ID != "parent" {
		t.Fatalf("roots = %+v", roots)
	}
	subagents := map[string]session.Subagent{}
	for _, subagent := range roots[0].Session.Subagents {
		subagents[subagent.ID] = subagent
	}
	done, failed := subagents["done-child"], subagents["failed-child"]
	if len(subagents) != 2 || done.Status != session.SubagentReturned || failed.Status != session.SubagentAborted {
		t.Fatalf("subagents = %+v", roots[0].Session.Subagents)
	}
	if done.Result != "done" || done.Task != "Look at it" || done.Name != "Check done-child" ||
		done.ToolUses != 3 || done.DurationMs == nil || *done.DurationMs != 1200 {
		t.Fatalf("done subagent = %+v", done)
	}

	family, familyMetadata, err := grok.GetSessionFamily("parent")
	if err != nil || len(family) != 3 {
		t.Fatalf("family = %v, err = %v", family, err)
	}
	if roots := finalizeSessions(family, map[string]*vendors.SessionMetadata{vendors.AgentGrok: familyMetadata}); len(roots) != 1 || len(roots[0].Session.Subagents) != 2 {
		t.Fatalf("family roots = %+v", roots)
	}
}

func TestGrokSinceWindowKeepsSubagentFamiliesTogether(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name                string
		parentAge, childAge time.Duration
	}{
		{"old child of a recent parent", 0, 72 * time.Hour},
		{"recent child of an old parent", 72 * time.Hour, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("GROK_HOME", home)
			group := filepath.Join(home, "sessions", "%2Fwork%2Frepo")
			parent := filepath.Join(group, "parent", "summary.json")
			child := filepath.Join(group, "child", "summary.json")
			writeGrokFile(t, parent, `{"info":{"id":"parent","cwd":"/work/repo"},"chat_format_version":1}`)
			writeGrokFile(t, child, `{"info":{"id":"child","cwd":"/work/repo"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"parent"}`)
			writeGrokFile(t, filepath.Join(group, "parent", "subagents", "sub", "meta.json"), `{"child_session_id":"child","status":"completed"}`)
			for path, age := range map[string]time.Duration{parent: test.parentAge, child: test.childAge} {
				if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
					t.Fatal(err)
				}
			}

			parsed, metadata, err := grok.CollectContext(context.Background(), now.Add(-48*time.Hour).UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			roots := finalizeSessions(parsed, map[string]*vendors.SessionMetadata{vendors.AgentGrok: metadata})
			if len(roots) != 1 || roots[0].Session.ID != "parent" || len(roots[0].Session.Subagents) != 1 {
				t.Fatalf("listed roots = %+v", roots)
			}
			family, _, err := grok.GetSessionFamily("parent")
			if err != nil || len(family) != len(parsed) {
				t.Fatalf("family = %d sessions, listed = %d, err = %v", len(family), len(parsed), err)
			}
		})
	}
}

func TestGrokSinceWindowUsesParentMetaWhenChildOmitsParent(t *testing.T) {
	now := time.Now()
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	group := filepath.Join(home, "sessions", "%2Fwork%2Frepo")
	parent := filepath.Join(group, "parent", "summary.json")
	child := filepath.Join(group, "child", "summary.json")
	writeGrokFile(t, parent, `{"info":{"id":"parent","cwd":"/work/repo"},"chat_format_version":1}`)
	writeGrokFile(t, child, `{"info":{"id":"child","cwd":"/work/repo"},"chat_format_version":1,"session_kind":"subagent"}`)
	writeGrokFile(t, filepath.Join(group, "parent", "subagents", "sub", "meta.json"), `{"child_session_id":"child","status":"completed","description":"demo"}`)
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(parent, old, old); err != nil {
		t.Fatal(err)
	}

	parsed, metadata, err := grok.CollectContext(context.Background(), now.Add(-48*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	roots := finalizeSessions(parsed, map[string]*vendors.SessionMetadata{vendors.AgentGrok: metadata})
	if len(roots) != 1 || roots[0].Session.ID != "parent" || len(roots[0].Session.Subagents) != 1 || roots[0].Session.Subagents[0].ID != "child" {
		t.Fatalf("listed roots = %+v", roots)
	}
	family, _, err := grok.GetSessionFamily("child")
	if err != nil || len(family) != 2 || family[0].Session.ID != "parent" {
		t.Fatalf("family = %v, err = %v", family, err)
	}
}

func writeGrokFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrokLiveProjectionMatchesPlannedAndFrozenIndependentRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	group := filepath.Join(home, "sessions", "repo")
	dirs := []string{}
	for _, item := range []struct{ id, kind, parent string }{
		{"root", "", ""}, {"child", "subagent", ""},
		{"fork", "fork", "root"}, {"worktree", "worktree", "root"},
	} {
		dir := filepath.Join(group, item.id)
		dirs = append(dirs, dir)
		writeGrokFile(t, filepath.Join(dir, "summary.json"), `{"info":{"id":"`+item.id+`"},"chat_format_version":1,"session_kind":"`+item.kind+`","parent_session_id":"`+item.parent+`"}`)
		if item.id != "root" {
			writeGrokFile(t, filepath.Join(group, "root", "subagents", item.id, "meta.json"), `{"child_session_id":"`+item.id+`","description":"task","status":"completed"}`)
		}
		at := time.Unix(1000, 0)
		if item.id == "fork" || item.id == "worktree" {
			at = time.Unix(3000, 0)
		}
		if err := os.Chtimes(filepath.Join(dir, "summary.json"), at, at); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := grok.PlanFamiliesContext(t.Context(), dirs)
	if err != nil || len(refs) != 3 {
		t.Fatalf("families=%v err=%v", refs, err)
	}
	check := func(t *testing.T, parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata, want map[string][]string) {
		t.Helper()
		roots := finalizeSessions(parsed, map[string]*vendors.SessionMetadata{vendors.AgentGrok: metadata})
		got := map[string][]string{}
		for _, root := range roots {
			got[root.Session.ID] = []string{}
			for _, child := range root.Session.Subagents {
				got[root.Session.ID] = append(got[root.Session.ID], child.ID)
			}
			if root.Session.ID == "fork" || root.Session.ID == "worktree" {
				if root.ParentID != "" || root.Session.ParentSessionID != "root" {
					t.Fatalf("lost independent origin: %+v", root)
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("roots/subagents=%v want=%v", got, want)
		}
	}
	want := map[string][]string{"root": {"child"}, "fork": {}, "worktree": {}}
	t.Run("live all time", func(t *testing.T) {
		parsed, metadata, err := grok.CollectContext(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		check(t, parsed, metadata, want)
	})
	t.Run("recent independent roots exclude old origin", func(t *testing.T) {
		parsed, metadata, err := grok.CollectContext(t.Context(), time.Unix(2000, 0).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		check(t, parsed, metadata, map[string][]string{"fork": {}, "worktree": {}})
	})
	t.Run("planned", func(t *testing.T) {
		parsed, metadata, err := grok.ParseFamiliesContext(t.Context(), refs)
		if err != nil {
			t.Fatal(err)
		}
		check(t, parsed, metadata, want)
	})
	t.Run("frozen", func(t *testing.T) {
		parsed := []*vendors.ParsedSession{}
		for _, ref := range refs {
			frozen := t.TempDir()
			frozenDirs := []string{}
			for i, dir := range ref.Dirs {
				dest := filepath.Join(frozen, "members", string(rune('a'+i)))
				if err := os.CopyFS(dest, os.DirFS(dir)); err != nil {
					t.Fatal(err)
				}
				frozenDirs = append(frozenDirs, dest)
			}
			proofs := []string{}
			for i, path := range ref.NonChildSummaryPaths() {
				dest := filepath.Join(frozen, "proofs", string(rune('a'+i)), "summary.json")
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeGrokFile(t, dest, string(body))
				proofs = append(proofs, dest)
			}
			family, err := grok.ParseFrozenFamilyContext(t.Context(), ref, frozenDirs, proofs)
			if err != nil {
				t.Fatal(err)
			}
			parsed = append(parsed, family...)
		}
		check(t, parsed, vendors.EmptySessionMetadata(), want)
	})
	for _, id := range []string{"root", "fork", "worktree"} {
		t.Run("detail "+id, func(t *testing.T) {
			facts, err := grok.GetSessionFacts(id)
			if err != nil || facts == nil || facts.Session.ID != id {
				t.Fatalf("facts=%+v err=%v", facts, err)
			}
			check(t, []*vendors.ParsedSession{facts}, vendors.EmptySessionMetadata(), map[string][]string{id: want[id]})
			family, _, err := grok.GetSessionFamily(id)
			count := 1
			if id == "root" {
				count = 2
			}
			if err != nil || len(family) != count || family[0].Session.ID != id {
				t.Fatalf("family size=%d want=%d err=%v", len(family), count, err)
			}
		})
	}
}
