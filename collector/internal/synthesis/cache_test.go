package synthesis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

type runnerFunc func(context.Context, string) (session.SessionSynthesis, error)

func (run runnerFunc) Run(ctx context.Context, input string) (session.SessionSynthesis, error) {
	return run(ctx, input)
}

func TestLookupLatestIgnoresPreviewRevisionDrift(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	cache := NewCache()
	want := session.SessionSynthesis{Outcome: "shipped"}
	if err := cache.Store("codex", "session", Record{Revision: 42, Synthesis: want}); err != nil {
		t.Fatal(err)
	}
	if got := cache.LookupLatest("codex", "session"); got == nil || got.Outcome != want.Outcome {
		t.Fatalf("LookupLatest() = %#v", got)
	}
}

func TestManagerDoesNotReuseSynthesisAcrossAgents(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	manager := NewManager(runnerFunc(func(_ context.Context, input string) (session.SessionSynthesis, error) {
		agent := "codex"
		if strings.Contains(input, "Agent: claude") {
			agent = "claude"
		}
		return session.SessionSynthesis{Outcome: agent}, nil
	}))
	claude := &session.Session{Agent: "claude", ID: "same", SessionDetails: session.SessionDetails{Turns: 6}}
	if !manager.Ensure(claude, 42) {
		t.Fatal("first synthesis was not started")
	}
	deadline := time.Now().Add(time.Second)
	for manager.Lookup("claude", "same", 42) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.Lookup("claude", "same", 42) == nil {
		t.Fatal("Claude synthesis did not finish")
	}
	codex := &session.Session{Agent: "codex", ID: "same", SessionDetails: session.SessionDetails{Turns: 6}}
	if !manager.Ensure(codex, 42) {
		t.Fatal("Codex synthesis reused Claude's same-ID cache entry")
	}
	deadline = time.Now().Add(time.Second)
	for manager.Lookup("codex", "same", 42) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := manager.Lookup("claude", "same", 42); got == nil || got.Outcome != "claude" {
		t.Fatalf("Claude synthesis = %#v", got)
	}
	if got := manager.Lookup("codex", "same", 42); got == nil || got.Outcome != "codex" {
		t.Fatalf("Codex synthesis = %#v", got)
	}
}
