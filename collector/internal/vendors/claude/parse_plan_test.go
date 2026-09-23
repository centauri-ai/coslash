package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestApprovedExitPlanModeBecomesTimelinePlan(t *testing.T) {
	plan := "# Plan\n" + strings.Repeat("Keep the full plan text. ", 20) + "Done."
	planFile := "/home/user/.claude/plans/plan.md"
	approved, _ := json.Marshal(map[string]any{"plan": plan, "isAgent": false, "filePath": planFile})
	rows := []string{
		`{"type":"user","timestamp":"2026-09-23T10:00:00Z","message":{"content":"Plan the work"}}`,
		`{"type":"assistant","timestamp":"2026-09-23T10:00:01Z","message":{"stop_reason":"tool_use","content":[{"type":"text","text":"Here is the plan."},{"type":"tool_use","id":"exit-1","name":"ExitPlanMode","input":{"plan":"ignored"}}]}}`,
		`{"type":"user","timestamp":"2026-09-23T10:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"exit-1","content":"User has approved your plan."}]},"toolUseResult":` + string(approved) + `}`,
		`{"type":"user","timestamp":"2026-09-23T10:00:03Z","message":{"content":"Plan again"}}`,
		`{"type":"assistant","timestamp":"2026-09-23T10:00:04Z","message":{"stop_reason":"tool_use","content":[{"type":"text","text":"Rejected plan intro."},{"type":"tool_use","id":"exit-2","name":"ExitPlanMode","input":{"plan":"Rejected plan"}}]}}`,
		`{"type":"user","timestamp":"2026-09-23T10:00:05Z","message":{"content":[{"type":"tool_result","tool_use_id":"exit-2","is_error":true,"content":"<tool_use_error>Error: No such tool available: ExitPlanMode.</tool_use_error>"}]},"toolUseResult":"Error: No such tool available: ExitPlanMode."}`,
		`{"type":"assistant","timestamp":"2026-09-23T10:00:06Z","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"Ordinary answer"}]}}`,
	}
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	var plans, recaps []string
	for _, entry := range parsed.Session.Digest {
		switch entry.Category {
		case "plan":
			plans = append(plans, entry.Description)
		case "recap":
			recaps = append(recaps, entry.Description)
		}
	}
	if !reflect.DeepEqual(plans, []string{plan}) {
		t.Fatalf("plans = %#v", plans)
	}
	if !reflect.DeepEqual(recaps, []string{"Ordinary answer"}) {
		t.Fatalf("recaps = %#v", recaps)
	}
	if parsed.Session.Summary == nil || *parsed.Session.Summary != "Ordinary answer" {
		t.Fatalf("summary = %v", parsed.Session.Summary)
	}
	if len(parsed.Session.FileEdits) != 0 {
		t.Fatalf("file edits = %#v, want none for %s", parsed.Session.FileEdits, planFile)
	}
}
