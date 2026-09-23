package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
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
	assertPrivateSynthesisPath(t, filepath.Dir(SummariesDir()), true)
	assertPrivateSynthesisPath(t, SummariesDir(), true)
	assertPrivateSynthesisPath(t, filepath.Join(SummariesDir(), "codex"), true)
	assertPrivateSynthesisPath(t, filepath.Join(SummariesDir(), "codex", "session.json"), false)
	if got := NewCache().LookupLatest("codex", "session"); got == nil || got.Outcome != want.Outcome {
		t.Fatalf("LookupLatest() = %#v", got)
	}
}

func TestStoreClearsCachedMiss(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	cache := NewCache()
	if _, err := cache.Load("codex", "session"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() error = %v, want not found", err)
	}
	want := Record{Revision: 42, Synthesis: session.SessionSynthesis{Outcome: "shipped"}}
	if err := cache.Store("codex", "session", want); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Load("codex", "session")
	if err != nil {
		t.Fatal(err)
	}
	if got.Synthesis.Outcome != want.Synthesis.Outcome {
		t.Fatalf("Load() outcome = %q, want %q", got.Synthesis.Outcome, want.Synthesis.Outcome)
	}
}

func TestMissingAgentDirectoryIsMemoizedAndExpires(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := os.MkdirAll(SummariesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	cache := NewCache()
	cache.missingAgents.now = func() time.Time { return now }
	if _, err := cache.Load("codex", "session"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() error = %v, want not found", err)
	}
	record := Record{Revision: 42, Synthesis: session.SessionSynthesis{Outcome: "restored"}}
	directory := filepath.Join(SummariesDir(), "codex")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "session.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load("codex", "session"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("memoized Load() error = %v, want not found", err)
	}
	now = now.Add(2 * time.Minute)
	if got, err := cache.Load("codex", "session"); err != nil || got.Synthesis.Outcome != "restored" {
		t.Fatalf("Load() after expiry = %#v, %v", got, err)
	}
}

func TestNegativeCacheIsBoundedAndExpiring(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newNegativeCache[string](2, time.Minute)
	cache.now = func() time.Time { return now }
	cache.add("one")
	now = now.Add(time.Second)
	cache.add("two")
	now = now.Add(time.Second)
	cache.add("three")
	if len(cache.entries) != 2 || cache.contains("one") {
		t.Fatalf("bounded entries = %#v", cache.entries)
	}
	now = now.Add(2 * time.Minute)
	if cache.contains("two") || cache.contains("three") {
		t.Fatalf("expired entries remain active: %#v", cache.entries)
	}
}

func TestRecordMissCacheCoversSupportedSessionSet(t *testing.T) {
	cache := NewCache()
	for agentIndex, agent := range []string{"claude", "codex", "cursor", "opencode"} {
		for sessionIndex := range vendors.MaxCandidateFilesPerAgent {
			cache.missingRecords.add(cacheKey{agent: agent, id: fmt.Sprintf("%d-%d", agentIndex, sessionIndex)})
		}
	}
	want := 4 * vendors.MaxCandidateFilesPerAgent
	if got := len(cache.missingRecords.entries); got != want {
		t.Fatalf("record misses = %d, want %d", got, want)
	}
	if !cache.missingRecords.contains(cacheKey{agent: "claude", id: "0-0"}) {
		t.Fatal("supported working set evicted its earliest miss")
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

func TestMigrateLegacyCachePreservesUnresolvedSession(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	legacy := writeLegacyRecord(t, "unresolved")
	if err := MigrateLegacyCache(func(string, string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("unresolved legacy cache was removed: %v", err)
	}
}

func TestCacheRejectsSessionIDPathTraversal(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("COSLASH_HOME", home)
	outside := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"../outside", `..\outside`, "nested/session", `nested\session`, ".", ""} {
		t.Run(strings.ReplaceAll(id, "/", "_"), func(t *testing.T) {
			if err := NewCache().Store("codex", id, Record{Revision: 42}); err == nil {
				t.Fatalf("Store(%q) accepted traversal", id)
			}
			if _, err := NewCache().Load("codex", id); err == nil {
				t.Fatalf("Load(%q) accepted traversal", id)
			}
		})
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "unchanged" {
		t.Fatalf("outside file changed: %q", content)
	}
}

func TestWriteSchemaFileIsPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	path, err := writeSchemaFile()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != synthesisSchema {
		t.Fatal("schema file content does not match embedded schema")
	}
	assertPrivateSynthesisPath(t, home, true)
	assertPrivateSynthesisPath(t, SynthesisCwd(), true)
	assertPrivateSynthesisPath(t, path, false)
}
