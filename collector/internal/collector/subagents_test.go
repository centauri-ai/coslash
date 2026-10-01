package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
