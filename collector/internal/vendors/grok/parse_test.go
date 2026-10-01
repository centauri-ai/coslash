package grok

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
		t.Fatalf("first prompt = %v", got)
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
	if len(parsed) != 1 || parsed[0].Session.ID != "01a0f48a-42bb-7802-b584-f5def46d1e75" || parsed[0].Session.Agent != "grok" {
		t.Fatalf("parsed = %+v", parsed)
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
