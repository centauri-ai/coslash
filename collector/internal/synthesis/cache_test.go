package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func writeLegacyRecord(t *testing.T, id string) string {
	t.Helper()
	if err := os.MkdirAll(SummariesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		SessionID string                   `json:"sessionId"`
		Revision  int64                    `json:"mtime"`
		Synthesis session.SessionSynthesis `json:"synthesis"`
	}{SessionID: id, Revision: 42, Synthesis: session.SessionSynthesis{Outcome: id}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(SummariesDir(), id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

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

func TestMigrateLegacyCacheMovesUniqueSession(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	legacy := writeLegacyRecord(t, "unique")
	if err := MigrateLegacyCache(func(agent, id string) (bool, error) {
		return agent == "codex" && id == "unique", nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := NewCache().LookupLatest("codex", "unique"); got == nil || got.Outcome != "unique" {
		t.Fatalf("migrated synthesis = %#v", got)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy cache remains: %v", err)
	}
}

func TestMigrateLegacyCacheDiscardsAmbiguousSession(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	legacy := writeLegacyRecord(t, "same")
	if err := MigrateLegacyCache(func(agent, id string) (bool, error) {
		return (agent == "claude" || agent == "codex") && id == "same", nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		if got := NewCache().LookupLatest(agent, "same"); got != nil {
			t.Fatalf("%s received ambiguous synthesis %#v", agent, got)
		}
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ambiguous legacy cache remains: %v", err)
	}
}
