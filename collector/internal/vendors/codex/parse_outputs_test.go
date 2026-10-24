package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func execCall(kind, id, cmd string) string {
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	if kind == "custom_tool_call" {
		return `{"timestamp":"2026-10-23T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"` + id + `","name":"exec","input":` + string(mustJSON(`tools.exec_command({cmd: `+string(mustJSON(cmd))+`})`)) + `}}`
	}
	return `{"timestamp":"2026-10-23T10:00:01Z","type":"response_item","payload":{"type":"function_call","call_id":"` + id + `","name":"exec_command","arguments":` + string(mustJSON(string(args))) + `}}`
}

func execResult(kind, id string, output any) string {
	return `{"timestamp":"2026-10-23T10:00:02Z","type":"response_item","payload":{"type":"` + kind + `","call_id":"` + id + `","output":` + string(mustJSON(output)) + `}}`
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func ptr(s string) *string { return &s }

func wantOutputs(t *testing.T, got, want []*string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("outputs = %v, want %v", got, want)
	}
	for i, w := range want {
		if (w == nil) != (got[i] == nil) || (w != nil && *w != *got[i]) {
			t.Fatalf("output %d = %v, want %v", i, got[i], w)
		}
	}
}

func TestCommandOutputsPairByCallID(t *testing.T) {
	long := strings.Repeat("x", session.CommandOutputLimit*2)
	parsed := parseLifecycleRows(t, []string{
		execCall("function_call", "a", "go test"),
		execCall("function_call", "b", "sleep 9"),
		execCall("function_call", "c", "false"),
		execCall("function_call", "d", "true"),
		execCall("function_call", "e", "cat big"),
		execCall("custom_tool_call", "f", "ls"),
		execResult("function_call_output", "d", ""),
		execResult("function_call_output", "a", "PASS\n"),
		execResult("function_call_output", "c", `{"output":"boom","metadata":{"exit_code":1}}`),
		execResult("function_call_output", "e", long),
		execResult("custom_tool_call_output", "f", []map[string]string{{"type": "input_text", "text": "Script completed"}, {"type": "input_text", "text": "a.txt"}}),
	})
	if len(parsed.Session.Commands) != 6 {
		t.Fatalf("commands = %v", parsed.Session.Commands)
	}
	wantOutputs(t, parsed.Session.CommandOutputs, []*string{
		ptr("PASS\n"), nil, ptr("boom"), ptr(""), ptr(session.TruncateOutput(long)), ptr("Script completed\na.txt"),
	})
	if len(*parsed.Session.CommandOutputs[4]) >= len(long) {
		t.Fatal("long output was not truncated")
	}
	for i, c := range parsed.Commands {
		if (c.Output == nil) != (parsed.Session.CommandOutputs[i] == nil) {
			t.Fatalf("labelled command %d output drifted from CommandOutputs", i)
		}
	}
}

func TestEmptyOutputInWrapperStaysEmpty(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{
		execCall("function_call", "a", "true"),
		execResult("function_call_output", "a", `{"output":"","metadata":{"exit_code":0}}`),
	})
	wantOutputs(t, parsed.Session.CommandOutputs, []*string{ptr("")})
}

func TestSubagentCommandsCarryOutputs(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{
		execCall("function_call", "a", "git status"),
		execResult("function_call_output", "a", "clean"),
	})
	if parsed.ParentID == "" || len(parsed.Commands) != 1 || parsed.Commands[0].Output == nil || *parsed.Commands[0].Output != "clean" {
		t.Fatalf("subagent commands = %+v", parsed.Commands)
	}
}

func TestSessionWithoutResultsStillParses(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{execCall("function_call", "a", "ls")})
	if len(parsed.Session.Commands) != 1 {
		t.Fatalf("commands = %v", parsed.Session.Commands)
	}
	wantOutputs(t, parsed.Session.CommandOutputs, []*string{nil})
}
