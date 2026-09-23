package codex

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCompletedPlanItemsBecomeTimelinePlans(t *testing.T) {
	plan := "# Plan\n" + strings.Repeat("Keep the full plan text. ", 20) + "Done."
	rows := []string{
		lifecycleRow("2026-09-21T17:55:15Z", "task_started"),
		`{"type":"turn_context","payload":{"collaboration_mode":{"mode":"default"}}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"First request"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"Ordinary answer"}}`,
		lifecycleRow("2026-09-21T17:55:16Z", "task_complete"),
		lifecycleRow("2026-09-21T17:55:17Z", "task_started"),
		`{"type":"turn_context","payload":{"collaboration_mode":{"mode":"plan"}}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"Plan the work"}}`,
		fmt.Sprintf(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Plan","id":"plan-1","text":%q}}}`, plan),
		`{"type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"<proposed_plan>Duplicate text</proposed_plan>"}}`,
		lifecycleRow("2026-09-21T17:55:18Z", "task_complete"),
		lifecycleRow("2026-09-21T17:55:19Z", "task_started"),
		`{"type":"turn_context","payload":{"collaboration_mode":{"mode":"plan"}}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"I need more information."}}`,
		lifecycleRow("2026-09-21T17:55:20Z", "task_complete"),
		lifecycleRow("2026-09-21T17:55:21Z", "task_started"),
		`{"type":"turn_context","payload":{"collaboration_mode":{"mode":"plan"}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Plan","text":"Aborted plan"}}}`,
		lifecycleRow("2026-09-21T17:55:22Z", "turn_aborted"),
		lifecycleRow("2026-09-21T17:55:23Z", "task_started"),
		`{"type":"turn_context","payload":{"collaboration_mode":{"mode":"plan"}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Plan","text":"Second plan"}}}`,
		lifecycleRow("2026-09-21T17:55:24Z", "task_complete"),
		lifecycleRow("2026-09-21T17:55:25Z", "task_started"),
		`{"type":"turn_context","payload":{"collaboration_mode":{"mode":"plan"}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Plan","text":"Unfinished plan"}}}`,
	}
	parsed := parseLifecycleRows(t, rows)
	var plans, recaps []string
	for _, entry := range parsed.Session.Digest {
		switch entry.Category {
		case "plan":
			plans = append(plans, entry.Description)
		case "recap":
			recaps = append(recaps, entry.Description)
		}
	}
	if !reflect.DeepEqual(plans, []string{plan, "Second plan"}) {
		t.Fatalf("plans = %#v", plans)
	}
	if !reflect.DeepEqual(recaps, []string{"Ordinary answer", "I need more information."}) {
		t.Fatalf("recaps = %#v", recaps)
	}
	if parsed.Session.Summary == nil || *parsed.Session.Summary != "I need more information." {
		t.Fatalf("summary = %v", parsed.Session.Summary)
	}
}
