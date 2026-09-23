package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestForkedLifecycleUsesLatestTaskEvent(t *testing.T) {
	rows := []string{
		lifecycleRow("2026-09-21T17:55:15Z", "task_complete"),
		lifecycleRow("2026-09-21T17:55:16Z", "task_started"),
		lifecycleRow("2026-09-21T17:55:17Z", "task_complete"),
		lifecycleRow("2026-09-21T17:55:20Z", "task_started"),
		lifecycleRow("2026-09-21T17:55:21Z", "task_started"),
		`{"timestamp":"2026-09-21T17:55:22Z","type":"event_msg","payload":{"type":"user_message","message":"review the fix"}}`,
		`{"timestamp":"2026-09-21T17:55:23Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"review approved"}}`,
		lifecycleRow("2026-09-21T17:55:30Z", "task_complete"),
	}
	parsed := parseLifecycleRows(t, rows)
	if parsed.InTurn {
		t.Fatal("settled child remained in turn")
	}
	if parsed.Session.Turns != 2 {
		t.Fatalf("turns = %d; want 2", parsed.Session.Turns)
	}
	if parsed.Session.DurationMs == nil || *parsed.Session.DurationMs != 11_000 {
		t.Fatalf("duration = %v; want 11000ms", parsed.Session.DurationMs)
	}
	if parsed.Session.Summary == nil || *parsed.Session.Summary != "review approved" {
		t.Fatalf("summary = %v; want review approved", parsed.Session.Summary)
	}
}

func TestForkedLifecycleDoesNotCountInheritedOpenTurn(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{
		lifecycleRow("2026-09-21T17:55:15Z", "task_started"),
		lifecycleRow("2026-09-21T17:55:16Z", "task_started"),
		`{"timestamp":"2026-09-21T17:55:17Z","type":"event_msg","payload":{"type":"user_message","message":"review the fix"}}`,
		`{"timestamp":"2026-09-21T17:55:18Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"review approved"}}`,
		lifecycleRow("2026-09-21T17:55:19Z", "task_complete"),
	})
	if parsed.InTurn || parsed.Session.Turns != 1 || parsed.Session.Summary == nil || *parsed.Session.Summary != "review approved" {
		t.Fatalf("completed child = in turn %t, turns %d, summary %v; want false, 1, review approved",
			parsed.InTurn, parsed.Session.Turns, parsed.Session.Summary)
	}
}

func TestForkedLifecycleFinalStartIsActive(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{
		lifecycleRow("2026-09-21T17:55:15Z", "task_complete"),
		lifecycleRow("2026-09-21T17:55:16Z", "task_started"),
	})
	if !parsed.InTurn {
		t.Fatal("child whose latest task event is task_started is not in turn")
	}
}

func lifecycleRow(timestamp, event string) string {
	return `{"timestamp":"` + timestamp + `","type":"event_msg","payload":{"type":"` + event + `"}}`
}

func parseLifecycleRows(t *testing.T, rows []string) *vendors.ParsedSession {
	t.Helper()
	id := "01a0c51b-94c2-72b0-90ef-b23ea631af2f"
	path := filepath.Join(t.TempDir(), "rollout-2026-09-21T10-55-14-"+id+".jsonl")
	content := `{"timestamp":"2026-09-21T17:55:14Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"parent","parent_thread_id":"parent"}}` + "\n"
	for _, row := range rows {
		content += row + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseTranscriptSource(vendors.LocalReadSource, path, func(string, string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
