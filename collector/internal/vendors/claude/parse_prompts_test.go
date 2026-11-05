package claude

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestPromptTextSkipsHarnessBlocks(t *testing.T) {
	for _, test := range []struct {
		name, row, want string
	}{
		{"plain", `{"message":{"content":"debug sharing"}}`, "debug sharing"},
		{"worktree", `{"message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":""}},{"type":"text","text":"<system-reminder>You are operating in a git worktree.</system-reminder>"},{"type":"text","text":"debug sharing"}]}}`, "debug sharing"},
		{"multiple-wrappers", `{"message":{"content":[{"type":"text","text":"<system-reminder>worktree</system-reminder>"},{"type":"text","text":"<local-command-stdout>ready</local-command-stdout>"},{"type":"text","text":"debug sharing"}]}}`, "debug sharing"},
		{"first-real-block", `{"message":{"content":[{"type":"text","text":"debug sharing"},{"type":"text","text":"<system-reminder>worktree</system-reminder>"},{"type":"text","text":"later text"}]}}`, "debug sharing"},
		{"empty-block", `{"message":{"content":[{"type":"text","text":""},{"type":"text","text":"debug sharing"}]}}`, "debug sharing"},
		{"harness-only", `{"message":{"content":[{"type":"image"},{"type":"text","text":"<system-reminder>worktree</system-reminder>"}]}}`, ""},
		{"plain-harness", `{"message":{"content":"<system-reminder>worktree</system-reminder>"}}`, ""},
		{"image-only", `{"message":{"content":[{"type":"image"}]}}`, ""},
		{"meta", `{"isMeta":true,"message":{"content":[{"type":"text","text":"<system-reminder>worktree</system-reminder>"},{"type":"text","text":"debug sharing"}]}}`, ""},
		{"compaction", `{"isCompactSummary":true,"message":{"content":[{"type":"text","text":"<system-reminder>worktree</system-reminder>"},{"type":"text","text":"summary"}]}}`, ""},
		{"tool-feedback", `{"message":{"content":[{"type":"tool_result","is_error":true,"content":"To tell you how to proceed, the user said:\ntry again"}]}}`, "try again"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var row claudeSessionRecord
			if err := json.Unmarshal([]byte(test.row), &row); err != nil {
				t.Fatal(err)
			}
			if got := row.promptText(); got != test.want {
				t.Fatalf("prompt = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWorktreeOpeningPromptStartsTimeline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, bashRows(
		`{"type":"user","message":{"content":[{"type":"image"},{"type":"text","text":"<system-reminder>You are operating in a git worktree.</system-reminder>"},{"type":"text","text":"debug sharing"}]}}`,
		bashUse("a", "go test"),
		bashResult("a", `"PASS"`, false),
		`{"type":"user","message":{"content":[{"type":"text","text":"<system-reminder>injected context</system-reminder>"}]}}`,
		`{"type":"user","message":{"content":"Not now"}}`,
		`{"type":"user","message":{"content":"hi"}}`,
	))
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.FirstPrompt == nil || *parsed.Session.FirstPrompt != "debug sharing" || parsed.Session.Turns != 3 {
		t.Fatalf("first prompt = %v, turns = %d", parsed.Session.FirstPrompt, parsed.Session.Turns)
	}
	want := []session.DigestEntry{
		{Turn: 1, Category: session.DigestFirstPrompt, Description: "debug sharing"},
		{Turn: 2, Category: session.DigestUser, Description: "Not now"},
		{Turn: 3, Category: session.DigestUser, Description: "hi"},
	}
	if !reflect.DeepEqual(parsed.Session.Digest, want) {
		t.Fatalf("digest = %#v, want %#v", parsed.Session.Digest, want)
	}
	if parsed.Session.ToolUses != 1 || len(parsed.Session.CommandOutputs) != 1 || parsed.Session.CommandOutputs[0] == nil || *parsed.Session.CommandOutputs[0] != "PASS" {
		t.Fatalf("tool uses = %d, outputs = %v", parsed.Session.ToolUses, parsed.Session.CommandOutputs)
	}
}

func TestArraySlashCommandStillStartsTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, bashRows(
		`{"type":"user","message":{"content":[{"type":"text","text":"<command-name>/review</command-name><command-args>diff</command-args>"}]}}`,
		`{"type":"user","isMeta":true,"message":{"content":"skill instructions"}}`,
	))
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.FirstPrompt == nil || *parsed.Session.FirstPrompt != "/review diff" || parsed.Session.Turns != 1 {
		t.Fatalf("first prompt = %v, turns = %d", parsed.Session.FirstPrompt, parsed.Session.Turns)
	}
}
