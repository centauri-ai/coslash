package claude

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func bashRows(calls ...string) string {
	return strings.Join(calls, "\n") + "\n"
}

func bashUse(id, command string) string {
	return `{"type":"assistant","timestamp":"2026-09-23T10:00:01Z","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"` + command + `","description":"run ` + id + `"}}]}}`
}

func bashResult(id, content string, isError bool) string {
	flag := ""
	if isError {
		flag = `"is_error":true,`
	}
	return `{"type":"user","timestamp":"2026-09-23T10:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `",` + flag + `"content":` + content + `}]}}`
}

func writeOutputTranscript(t *testing.T, path, rows string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBashOutputsPairByToolUseID(t *testing.T) {
	long := strings.Repeat("x", session.CommandOutputLimit*2)
	rows := bashRows(
		`{"type":"user","timestamp":"2026-09-23T10:00:00Z","message":{"content":"go"}}`,
		bashUse("a", "go test"),
		bashUse("b", "sleep 9"),
		bashUse("c", "false"),
		bashUse("d", "true"),
		bashUse("e", "cat big"),
		bashResult("d", `""`, false),
		bashResult("a", `"PASS\n"`, false),
		bashResult("c", `[{"type":"text","text":"exit 1"},{"type":"text","text":"boom"}]`, true),
		bashResult("e", `"`+long+`"`, false),
	)
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, rows)
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	outputs := parsed.Session.CommandOutputs
	if len(outputs) != 5 {
		t.Fatalf("outputs = %v", outputs)
	}
	want := []*string{ptr("PASS\n"), nil, ptr("exit 1\nboom"), ptr(""), ptr(session.TruncateOutput(long))}
	for i, w := range want {
		if (w == nil) != (outputs[i] == nil) || (w != nil && *w != *outputs[i]) {
			t.Fatalf("output %d = %v, want %v", i, outputs[i], w)
		}
	}
	if len(*outputs[4]) >= len(long) {
		t.Fatalf("long output was not truncated: %d bytes", len(*outputs[4]))
	}
}

func TestSessionWithoutCommandsHasNoOutputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, bashRows(`{"type":"user","timestamp":"2026-09-23T10:00:00Z","message":{"content":"hi"}}`))
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.CommandOutputs != nil {
		t.Fatalf("outputs = %v", parsed.Session.CommandOutputs)
	}
}

func TestSubagentCommandsCarryOutputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "22222222-2222-2222-2222-222222222222", "subagents", "agent-a1.jsonl")
	writeOutputTranscript(t, path, bashRows(
		bashUse("s1", "ls"),
		bashUse("s2", "pwd"),
		bashResult("s1", `"file.txt\n"`, false),
	))
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	commands := parsed.Commands
	if len(commands) != 2 || commands[0].Output == nil || *commands[0].Output != "file.txt\n" || commands[1].Output != nil {
		t.Fatalf("subagent commands = %+v", commands)
	}
}

func ptr(value string) *string { return &value }

func TestPullRequestLinksComeFromSuccessfulCreates(t *testing.T) {
	mcpUse := `{"type":"assistant","timestamp":"2026-09-23T10:00:01Z","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"m","name":"mcp__github__create_pull_request","input":{"title":"t"}}]}}`
	rows := bashRows(
		`{"type":"user","timestamp":"2026-09-23T10:00:00Z","message":{"content":"go"}}`,
		`{"type":"pr-link","prNumber":9,"prUrl":"https://github.com/acme/viewed/pull/9"}`,
		bashUse("a", "gh pr create --fill"),
		bashUse("b", "gh pr create --fill"),
		bashUse("c", "rtk gh pr create --fill"),
		bashUse("d", "gh pr view 5"),
		mcpUse,
		bashResult("a", `"https://github.com/acme/api/pull/2\n"`, false),
		bashResult("b", `"https://github.com/acme/failed/pull/3"`, true),
		bashResult("c", `"https://github.com/acme/api/pull/2\n"`, false),
		bashResult("d", `"https://github.com/acme/api/pull/5"`, false),
		bashResult("m", `[{"type":"text","text":"{\"url\":\"https://github.com/acme/web/pull/4\"}"}]`, false),
	)
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, rows)
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []session.PullRequestLink{
		{URL: "https://github.com/acme/api/pull/2", Repository: "acme/api", Number: 2},
		{URL: "https://github.com/acme/web/pull/4", Repository: "acme/web", Number: 4},
	}
	if got := parsed.Session.PullRequestLinks; !reflect.DeepEqual(got, want) || parsed.Session.PullRequests != 2 {
		t.Fatalf("links = %#v, count = %d, want %#v", got, parsed.Session.PullRequests, want)
	}
}
