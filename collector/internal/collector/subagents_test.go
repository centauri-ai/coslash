package collector

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
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
