package grok

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
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

func TestParseFileEditCountsCompletedDiffOnce(t *testing.T) {
	parsed, err := parseSession(filepath.Join("testdata", "finished"))
	if err != nil {
		t.Fatal(err)
	}
	s := parsed.Session
	if len(s.FileEdits) != 1 || s.EditedFileCount != 1 {
		t.Fatalf("file edits = %+v", s.FileEdits)
	}
	edit := s.FileEdits[0]
	if edit.Path != "/work/repo/alias.sh" || edit.Additions != 2 || edit.Deletions != 1 || edit.Edits != 1 || edit.IsNew {
		t.Fatalf("file edit = %+v", edit)
	}
	if changes := edit.Changes(); len(changes) != 1 || changes[0].Text != "@@\n-alias agent=grok\n+alias agent=cursor\n+alias g=grok\n" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestReadTodosKeepsOrderDropsCancelledAndMarksOnlyCompletedDone(t *testing.T) {
	got := readTodos(filepath.Join("testdata", "plan", "plan.json"))
	want := []session.Todo{{Text: "Ship", Done: true}, {Text: "Review", Done: false}}
	if !slices.Equal(got, want) {
		t.Fatalf("todos = %+v, want %+v", got, want)
	}
	if got := readTodos(filepath.Join("testdata", "absent", "plan.json")); got == nil || len(got) != 0 {
		t.Fatalf("missing plan todos = %#v, want empty", got)
	}
}

func TestParseDeclaredGoalFromGoalState(t *testing.T) {
	parsed, err := parseSession(filepath.Join("testdata", "plan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Session.DeclaredGoal; got == nil || *got != "Ship Grok" {
		t.Fatalf("declared goal = %q, want Ship Grok", stringValue(got))
	}
	if parsed, err = parseSession(filepath.Join("testdata", "finished")); err != nil {
		t.Fatal(err)
	}
	if got := parsed.Session.DeclaredGoal; got != nil {
		t.Fatalf("declared goal without goal/state.json = %q, want nil", *got)
	}
}

func TestParseCommandsCommitsAndPullRequestsSkipDryRunsAndFailures(t *testing.T) {
	parsed, err := parseSession(filepath.Join("testdata", "commands"))
	if err != nil {
		t.Fatal(err)
	}
	s := parsed.Session
	if len(s.Commands) != 5 || s.Commands[0] != `git commit -m "ship"` || len(parsed.Commands) != 5 {
		t.Fatalf("commands = %q", s.Commands)
	}
	want := []session.CommitObservation{{Hash: "abc1234", Subject: "ship"}}
	if !slices.Equal(s.CommitLog, want) {
		t.Fatalf("commit log = %+v, want %+v", s.CommitLog, want)
	}
	if s.PullRequests != 1 {
		t.Fatalf("pull requests = %d, want 1", s.PullRequests)
	}
}

func TestParseCompactionSeedFromNewestCheckpoint(t *testing.T) {
	parsed, err := parseSession(filepath.Join("testdata", "compacted"))
	if err != nil {
		t.Fatal(err)
	}
	s := parsed.Session
	if s.Compactions != 1 || !strings.Contains(s.CompactionSeed, "Earlier work") || strings.Contains(s.CompactionSeed, "Oldest work") {
		t.Fatalf("compactions = %d, seed = %q", s.Compactions, s.CompactionSeed)
	}
	if parsed, err = parseSession(filepath.Join("testdata", "finished")); err != nil {
		t.Fatal(err)
	}
	if parsed.Session.CompactionSeed != "" {
		t.Fatalf("seed without checkpoints = %q, want empty", parsed.Session.CompactionSeed)
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

func TestSubagentLinksWhenChildSummaryOmitsParent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	root := filepath.Join(home, "sessions", "%2Fwork%2Frepo")
	writeSummary(t, filepath.Join(root, "parent"), `{"info":{"id":"parent","cwd":"/work/repo"},"chat_format_version":1,"generated_title":"Create subagent for fake work"}`)
	writeSummary(t, filepath.Join(root, "child"), `{"info":{"id":"child","cwd":"/work/repo"},"chat_format_version":1,"session_kind":"subagent"}`)
	meta := filepath.Join(root, "parent", "subagents", "child")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, "meta.json"), []byte(`{"child_session_id":"child","parent_session_id":"parent","description":"Fake work demo","prompt":"do the fake work","status":"completed","duration_ms":4176}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, "output.json"), []byte(`{"schema_version":1,"output":"done"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	facts, err := GetSessionFacts("parent")
	if err != nil {
		t.Fatal(err)
	}
	if facts == nil || len(facts.Session.Subagents) != 1 {
		t.Fatalf("subagents = %+v", facts)
	}
	got := facts.Session.Subagents[0]
	if got.ID != "child" || got.Name != "Fake work demo" || got.Status != session.SubagentReturned || got.Task != "do the fake work" || got.Result != "done" {
		t.Fatalf("subagent = %+v", got)
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
