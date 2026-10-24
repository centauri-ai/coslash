package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func shellCall(id, command string) string {
	quoted, _ := json.Marshal(command)
	return `{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"` + id + `","title":"run_terminal_command","rawInput":{"command":` + string(quoted) + `}}}}`
}

func shellResult(id, command, output string, exitCode int) string {
	cmd, _ := json.Marshal(command)
	out, _ := json.Marshal(output)
	return `{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"completed","rawOutput":{"type":"Bash","command":` +
		string(cmd) + `,"output_for_prompt":` + string(out) + `,"exit_code":` + strconv.Itoa(exitCode) + `,"timed_out":false}}}}`
}

func parseOutputSession(t *testing.T, rows ...string) (*session.Session, []session.SubagentCommand) {
	t.Helper()
	return parseOutputSessionKind(t, "", rows...)
}

func parseOutputSessionKind(t *testing.T, kind string, rows ...string) (*session.Session, []session.SubagentCommand) {
	t.Helper()
	dir := t.TempDir()
	summary := `{"info":{"id":"01a0f8c5-1ce9-7201-bb82-7db1e351bfd4","cwd":"/work/synthetic-alpha"},"chat_format_version":1` + kind + `}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Session, parsed.Commands
}

func TestShellOutputsPairWithCommands(t *testing.T) {
	long := strings.Repeat("x", session.CommandOutputLimit*2)
	s, labelled := parseOutputSession(t,
		shellCall("a", "go test"),
		shellCall("b", "sleep 9"),
		shellCall("c", "false"),
		shellCall("d", "true"),
		shellCall("e", "cat big"),
		shellResult("d", "true", "", 0),
		shellResult("a", "go test", "PASS\n", 0),
		shellResult("c", "false", "exit status 1: boom", 1),
		shellResult("e", "cat big", long, 0),
	)
	if want := []string{"true", "go test", "false", "cat big"}; strings.Join(s.Commands, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q", s.Commands)
	}
	if len(s.CommandOutputs) != len(s.Commands) {
		t.Fatalf("outputs = %v", s.CommandOutputs)
	}
	for i, want := range []string{"", "PASS\n", "exit status 1: boom", session.TruncateOutput(long)} {
		if s.CommandOutputs[i] == nil || *s.CommandOutputs[i] != want {
			t.Fatalf("output %d = %v, want %q", i, s.CommandOutputs[i], want)
		}
		if labelled[i].Output == nil || *labelled[i].Output != want {
			t.Fatalf("labelled output %d = %v", i, labelled[i].Output)
		}
	}
	if got := *s.CommandOutputs[3]; len(got) >= len(long) || !strings.Contains(got, "bytes truncated") {
		t.Fatalf("long output was not truncated: %d bytes", len(got))
	}
}

func TestShellWithoutResultIsNotRecorded(t *testing.T) {
	s, _ := parseOutputSession(t, shellCall("a", "sleep 9"))
	if len(s.Commands) != 0 || s.CommandOutputs != nil {
		t.Fatalf("commands = %q outputs = %v", s.Commands, s.CommandOutputs)
	}
}

func TestShellOutputWithoutToolCallID(t *testing.T) {
	s, _ := parseOutputSession(t, shellResult("", "ls", "a.txt\n", 0), shellResult("", "pwd", "/work\n", 0))
	if len(s.CommandOutputs) != 2 || *s.CommandOutputs[0] != "a.txt\n" || *s.CommandOutputs[1] != "/work\n" {
		t.Fatalf("outputs = %v", s.CommandOutputs)
	}
}

func TestSessionWithoutShellResultsParses(t *testing.T) {
	s, labelled := parseOutputSession(t, `{"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}`)
	if len(s.Commands) != 0 || s.CommandOutputs != nil || len(labelled) != 0 {
		t.Fatalf("commands = %q outputs = %v", s.Commands, s.CommandOutputs)
	}
}

func TestSubagentCommandsCarryOutputs(t *testing.T) {
	_, labelled := parseOutputSessionKind(t, `,"session_kind":"subagent","parent_session_id":"parent"`,
		shellCall("k1", "echo child"), shellResult("k1", "echo child", "child output\n", 0))
	if len(labelled) != 1 || labelled[0].Output == nil || *labelled[0].Output != "child output\n" {
		t.Fatalf("subagent commands = %+v", labelled)
	}
}
