package grok

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestParseFinishedSessionUsesUsageTokensAndSignalsContextFill(t *testing.T) {
	parsed, err := parseSession(filepath.Join("testdata", "finished"))
	if err != nil {
		t.Fatal(err)
	}
	s := parsed.Session
	if got := s.Tokens["grok-4.7"].InputTokens; got != 100 {
		t.Fatalf("input tokens = %d, want 100", got)
	}
	if s.ContextTokens == nil || *s.ContextTokens != 40 {
		t.Fatalf("context tokens = %v, want 40", s.ContextTokens)
	}
	if s.ContextWindow == nil || *s.ContextWindow != 256000 {
		t.Fatalf("context window = %v, want 256000", s.ContextWindow)
	}
	if parsed.RecordedCost == nil || *parsed.RecordedCost != 0.754106 {
		t.Fatalf("recorded cost = %v, want 0.754106", parsed.RecordedCost)
	}
	if s.Repository == nil || *s.Repository != "github.com/centauri-ai/coslash" {
		t.Fatalf("repository = %v", s.Repository)
	}
	if s.Name == nil || *s.Name != "Remove Grok alias" || s.Summary == nil || *s.Summary != "`agent` now launches Cursor" {
		t.Fatalf("name = %v, summary = %v", s.Name, s.Summary)
	}
	if s.ToolUses != 1 || parsed.InTurn {
		t.Fatalf("tool uses = %d, in turn = %v", s.ToolUses, parsed.InTurn)
	}
}

func TestParseOpenTurnCountsToolCallsAndLeavesTokensUnknown(t *testing.T) {
	parsed, err := parseSession(filepath.Join("testdata", "open"))
	if err != nil {
		t.Fatal(err)
	}
	s := parsed.Session
	if s.ToolUses != 1 {
		t.Fatalf("tool uses = %d, want 1", s.ToolUses)
	}
	if s.Tokens != nil {
		t.Fatalf("tokens = %v, want nil", s.Tokens)
	}
	if s.ContextTokens != nil {
		t.Fatalf("context tokens = %d, want nil", *s.ContextTokens)
	}
	if !parsed.InTurn {
		t.Fatal("open turn is not in turn")
	}
}

func TestParseFirstPromptSkipsSyntheticSystemReminder(t *testing.T) {
	parsed, err := parseSession(filepath.Join("testdata", "open"))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Session.FirstPrompt; got == nil || *got != "plan local grok support" {
		t.Fatalf("first prompt = %q, want the real prompt", stringValue(got))
	}
}

func TestCollectSkipsSubagentsAndOtherChatFormats(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	root := filepath.Join(home, "sessions", "%2Fwork%2Frepo")
	copyFixture(t, filepath.Join("testdata", "finished"), filepath.Join(root, "01a0f48a-42bb-7802-b584-f5def46d1e75"))
	writeSummary(t, filepath.Join(root, "child"), `{"info":{"id":"child","cwd":"/work/repo"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"01a0f48a-42bb-7802-b584-f5def46d1e75"}`)
	writeSummary(t, filepath.Join(root, "legacy"), `{"info":{"id":"legacy","cwd":"/work/repo"},"chat_format_version":0}`)

	parsed, _, err := CollectContext(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 {
		t.Fatalf("parsed = %+v", parsed)
	}
	roots := 0
	for _, item := range parsed {
		if item.ParentID == "" {
			roots++
			if item.Session.ID != "01a0f48a-42bb-7802-b584-f5def46d1e75" || item.Session.Agent != "grok" {
				t.Fatalf("root = %+v", item.Session)
			}
		} else if item.Session.ID != "child" || item.ParentID != "01a0f48a-42bb-7802-b584-f5def46d1e75" {
			t.Fatalf("child = %+v", item)
		}
	}
	if roots != 1 {
		t.Fatalf("roots = %d, want 1", roots)
	}
	if health := Health(); health.Missing || health.Err != nil || health.Sessions != 1 || health.Entries != 3 {
		t.Fatalf("health = %+v", health)
	}
	if facts, err := GetSessionFacts("01a0f48a-42bb-7802-b584-f5def46d1e75"); err != nil || facts == nil {
		t.Fatalf("facts = %v, err = %v", facts, err)
	}
}

func TestHealthReportsMissingRoot(t *testing.T) {
	t.Setenv("GROK_HOME", filepath.Join(t.TempDir(), "absent"))
	if health := Health(); !health.Missing || health.Err != nil {
		t.Fatalf("health = %+v", health)
	}
	parsed, _, err := CollectContext(context.Background(), 0)
	if err != nil || len(parsed) != 0 {
		t.Fatalf("parsed = %v, err = %v", parsed, err)
	}
}

func TestPartialUsageJSONDoesNotRecordCost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	body := []byte(`{"session":{"costUsdTicks":10000000000,"costIsPar`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	parsed := &vendors.ParsedSession{Session: &session.Session{}}
	applyUsage(parsed, path)
	if parsed.RecordedCost != nil || parsed.Session.Tokens != nil || parsed.Session.UnattributedTokens != nil {
		t.Fatalf("partial usage applied cost %v tokens %v", parsed.RecordedCost, parsed.Session.Tokens)
	}
}

func TestUsageWithoutModelBreakdownKeepsSessionTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	body := []byte(`{"session":{"inputTokens":12,"outputTokens":3,"cachedReadTokens":4,"cacheCreationTokens":0}}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	parsed := &vendors.ParsedSession{Session: &session.Session{}}
	applyUsage(parsed, path)
	got := parsed.Session.UnattributedTokens
	if got == nil || got.InputTokens != 12 || got.OutputTokens != 3 || got.CacheReadInputTokens != 4 || len(parsed.Session.Tokens) != 0 {
		t.Fatalf("unattributed = %+v, tokens = %+v", got, parsed.Session.Tokens)
	}
}

func TestReadJSONRejectsFileOverTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.json")
	if err := os.WriteFile(path, []byte(`{"info":{"id":"too-big"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readBounded(path, 8); err == nil {
		t.Fatal("readBounded accepted a file over the limit")
	}
	var summary summaryFile
	if err := readJSON(path, &summary); err != nil || summary.Info.ID != "too-big" {
		t.Fatalf("summary = %+v, err = %v", summary, err)
	}
}

type cancelOnCheck struct {
	context.Context
	cancel context.CancelFunc
	after  int
	seen   int
}

func (c *cancelOnCheck) Err() error {
	c.seen++
	if c.seen > c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestReadUpdatesStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := &cancelOnCheck{Context: ctx, cancel: cancel, after: 1}
	got, err := readUpdates(wrapped, filepath.Join("testdata", "finished", "updates.jsonl"))
	if !errors.Is(err, context.Canceled) || got.firstPrompt != "" {
		t.Fatalf("updates = %+v, err = %v", got, err)
	}
}

func TestCancelledCollectStopsBeforeParsing(t *testing.T) {
	t.Setenv("GROK_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parsed, _, err := CollectContext(ctx, 0)
	if !errors.Is(err, context.Canceled) || parsed != nil {
		t.Fatalf("parsed = %v, err = %v", parsed, err)
	}
}

func TestSinceIncludesSignalAndUsageUpdates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	dir := filepath.Join(home, "sessions", "%2Fwork%2Frepo", "01a0f48a-42bb-7802-b584-f5def46d1e75")
	copyFixture(t, filepath.Join("testdata", "finished"), dir)
	old := time.Now().Add(-72 * time.Hour)
	for _, name := range []string{"summary.json", "updates.jsonl"} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(dir, "usage.json"), now, now); err != nil {
		t.Fatal(err)
	}
	parsed, _, err := CollectContext(context.Background(), now.Add(-time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 || parsed[0].Session.ID != "01a0f48a-42bb-7802-b584-f5def46d1e75" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func copyFixture(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(from, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, entry.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeSummary(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}
