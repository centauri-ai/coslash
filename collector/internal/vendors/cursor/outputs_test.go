package cursor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const outputsTestID = "00000000-0000-4000-8000-0000000000a1"

func parseShellTranscript(t *testing.T, id string, commands ...string) *vendors.ParsedSession {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>Run things</user_query>"}]}}` + "\n"
	for _, command := range commands {
		input, _ := json.Marshal(map[string]string{"command": command})
		data += `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":` + string(input) + `}]}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// outputsTestHome returns a fresh home and points every variable that Cursor
// paths read on Linux, macOS, and Windows at it, so tests never share a store.
func outputsTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	return home
}

type ideBubble struct {
	createdAt, command, status, result string
}

func writeIDEBubbles(t *testing.T, home, id string, bubbles ...ideBubble) {
	t.Helper()
	path := filepath.Join(GlobalStorage(home), "state.vscdb")
	if _, err := os.Stat(path); err != nil {
		if err := createMetadataTestDB(path); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, bubble := range bubbles {
		params, _ := json.Marshal(map[string]string{"command": bubble.command})
		value, _ := json.Marshal(map[string]any{
			"createdAt": bubble.createdAt,
			"toolFormerData": map[string]string{
				"name": "run_terminal_command_v2", "status": bubble.status,
				"params": string(params), "result": bubble.result,
			},
		})
		key := fmt.Sprintf("bubbleId:%s:bubble-%02d", id, 9-i)
		if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`, key, string(value)); err != nil {
			t.Fatal(err)
		}
	}
}

func ideMetadata(id string) *vendors.SessionMetadata {
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(id).Entrypoint = entrypointIDE
	return metadata
}

func derefOutputs(outputs []*string) []string {
	result := make([]string, len(outputs))
	for i, output := range outputs {
		result[i] = "<nil>"
		if output != nil {
			result[i] = *output
		}
	}
	return result
}

func TestIDECommandOutputsPairByCommandAndOrder(t *testing.T) {
	home := outputsTestHome(t)
	long := strings.Repeat("0123456789abcdef\n", 2000)
	writeIDEBubbles(t, home, outputsTestID,
		ideBubble{"2026-10-23T10:00:01.000Z", "echo hi", "completed", `{"output":"hi\n","rejected":false}`},
		ideBubble{"2026-10-23T10:00:02.000Z", "false", "completed", `{"output":"boom\n","exitCode":1,"rejected":false}`},
		ideBubble{"2026-10-23T10:00:03.000Z", "true", "completed", `{"rejected":false,"notInterrupted":true}`},
		ideBubble{"2026-10-23T10:00:04.000Z", "cat big", "completed", mustJSON(map[string]any{"output": long, "rejected": false})},
		ideBubble{"2026-10-23T10:00:05.000Z", "rm denied", "completed", `{"rejected":true}`},
		ideBubble{"2026-10-23T10:00:06.000Z", "sleep 100", "loading", ``},
		ideBubble{"2026-10-23T10:00:07.000Z", "git commit -m x --trailer y", "completed", `{"output":"[main abc] x\n","rejected":false}`},
	)
	parsed := parseShellTranscript(t, outputsTestID, "echo hi", "false", "true", "cat big", "rm denied", "sleep 100", "git commit -m x")

	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, ideMetadata(outputsTestID)); err != nil {
		t.Fatal(err)
	}

	want := []string{"hi\n", "boom\n", "", session.TruncateOutput(long), "<nil>", "<nil>", "<nil>"}
	got := derefOutputs(parsed.Session.CommandOutputs)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs = %q, want %q", got, want)
	}
	if !strings.Contains(got[3], "bytes truncated") {
		t.Fatalf("long output was not truncated")
	}
	for i, command := range parsed.Commands {
		if (command.Output == nil) != (parsed.Session.CommandOutputs[i] == nil) || (command.Output != nil && *command.Output != want[i]) {
			t.Fatalf("subagent command %d output = %v, want %q", i, command.Output, want[i])
		}
	}
}

func TestIDECommandOutputsRepeatedCommandsNeedMatchingCounts(t *testing.T) {
	home := outputsTestHome(t)
	writeIDEBubbles(t, home, outputsTestID,
		ideBubble{"2026-10-23T10:00:01.000Z", "git status", "completed", `{"output":"one","rejected":false}`},
		ideBubble{"2026-10-23T10:00:02.000Z", "git status", "completed", `{"output":"two","rejected":false}`},
		ideBubble{"2026-10-23T10:00:03.000Z", "ls", "completed", `{"output":"a","rejected":false}`},
	)
	parsed := parseShellTranscript(t, outputsTestID, "git status", "ls", "git status")
	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, ideMetadata(outputsTestID)); err != nil {
		t.Fatal(err)
	}
	if got, want := derefOutputs(parsed.Session.CommandOutputs), []string{"one", "a", "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs = %q, want %q", got, want)
	}

	other := "00000000-0000-4000-8000-0000000000a2"
	writeIDEBubbles(t, home, other,
		ideBubble{"2026-10-23T10:00:01.000Z", "git status", "completed", `{"output":"one","rejected":false}`},
	)
	parsed = parseShellTranscript(t, other, "git status", "git status")
	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, ideMetadata(other)); err != nil {
		t.Fatal(err)
	}
	if parsed.Session.CommandOutputs != nil {
		t.Fatalf("outputs = %q, want none when the repeat counts differ", derefOutputs(parsed.Session.CommandOutputs))
	}
}

func TestCommandOutputsStayAbsentWithoutSideStoreOrLane(t *testing.T) {
	home := outputsTestHome(t)
	parsed := parseShellTranscript(t, outputsTestID, "echo hi")
	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, ideMetadata(outputsTestID)); err != nil {
		t.Fatal(err)
	}
	if parsed.Session.CommandOutputs != nil || parsed.Commands[0].Output != nil {
		t.Fatalf("outputs = %v, want none for a session without a side store", parsed.Session.CommandOutputs)
	}

	writeIDEBubbles(t, home, outputsTestID, ideBubble{"2026-10-23T10:00:01.000Z", "echo hi", "completed", `{"output":"hi","rejected":false}`})
	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, vendors.EmptySessionMetadata()); err != nil {
		t.Fatal(err)
	}
	if parsed.Session.CommandOutputs != nil {
		t.Fatalf("outputs = %v, want none when the lane is unknown", parsed.Session.CommandOutputs)
	}
}

func TestIDECommandOutputsSupportSubagentSessions(t *testing.T) {
	home := outputsTestHome(t)
	childID := "00000000-0000-4000-8000-0000000000b1"
	writeIDEBubbles(t, home, childID, ideBubble{"2026-10-23T10:00:01.000Z", "ls", "completed", `{"output":"child\n","rejected":false}`})
	child := parseShellTranscript(t, childID, "ls")
	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{child}, ideMetadata(childID)); err != nil {
		t.Fatal(err)
	}
	if got := child.Commands[0].Output; got == nil || *got != "child\n" {
		t.Fatalf("subagent command output = %v, want child output", got)
	}
}

func writeCLIStore(t *testing.T, home, id string, blobs ...string) {
	t.Helper()
	path := filepath.Join(home, ".cursor", "chats", "workspace", id, "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i, blob := range blobs {
		if _, err := db.Exec(`INSERT INTO blobs(id, data) VALUES (?, ?)`, fmt.Sprintf("blob-%02d", 9-i), []byte(blob)); err != nil {
			t.Fatal(err)
		}
	}
}

func cliCall(callID, command string) string {
	args, _ := json.Marshal(map[string]string{"command": command})
	return `{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"tool-call","toolCallId":"` + callID + `","toolName":"Shell","args":` + string(args) + `}]}`
}

func cliResult(callID, result string) string {
	return `{"role":"tool","content":[{"type":"tool-result","toolCallId":"` + callID + `","toolName":"Shell","result":` + mustJSON(result) + `}]}`
}

func cliWrapped(body string, code int) string {
	return fmt.Sprintf("Exit code: %d\n\nCommand output:\n\n```\n%s\n```\n\nCommand completed in 12 ms.\n\nShell state (cwd, env vars) persists for subsequent calls.", code, body)
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestCLICommandOutputsPairByCommandAndOrder(t *testing.T) {
	home := outputsTestHome(t)
	long := strings.Repeat("0123456789abcdef\n", 2000)
	writeCLIStore(t, home, outputsTestID,
		`{"role":"user","content":"hello"}`,
		cliCall("c1", "echo hi"), cliResult("c1", cliWrapped("hi\n", 0)),
		cliCall("c2", "false"), cliResult("c2", cliWrapped("boom\n", 1)),
		cliCall("c3", "true"), cliResult("c3", cliWrapped("", 0)),
		cliCall("c4", "cat big"), cliResult("c4", cliWrapped(long, 0)),
		cliCall("c5", "wait"), cliResult("c5", "The command did not complete in 5000ms and was sent to the background.\nShell ID: 1"),
		cliCall("c6", "denied"), cliResult("c6", "Permission denied: Command blocked by permissions configuration"),
		cliCall("c7", "killed"), cliResult("c7", "Error: Shell command was interrupted by the user after 100ms before it completed."),
		cliCall("c8", "pending"),
		cliCall("c9", "big file"), cliResult("c9", "Exit code: 0\n\nCommand output has been written to: /tmp/x.txt (30 KB, 900 lines)\n\nCommand completed in 3 ms."),
		cliCall("c10", "fenced"), cliResult("c10", cliWrapped("a\n```\nb\n", 0)),
		"not json",
	)
	parsed := parseShellTranscript(t, outputsTestID, "echo hi", "false", "true", "cat big", "wait", "denied", "killed", "pending", "big file", "fenced")
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(outputsTestID).Entrypoint = entrypointCLI

	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, metadata); err != nil {
		t.Fatal(err)
	}

	want := []string{"hi\n", "boom\n", "", session.TruncateOutput(long), "<nil>", "<nil>", "<nil>", "<nil>", "<nil>", "a\n```\nb\n"}
	if got := derefOutputs(parsed.Session.CommandOutputs); !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs = %q, want %q", got, want)
	}
	if len(parsed.Session.CommandOutputs) != len(parsed.Session.Commands) || len(parsed.Commands) != len(parsed.Session.Commands) {
		t.Fatal("outputs drifted from commands")
	}
}

func TestCLICommandOutputsIgnoreStoreInIDELane(t *testing.T) {
	home := outputsTestHome(t)
	writeCLIStore(t, home, outputsTestID, cliCall("c1", "echo hi"), cliResult("c1", cliWrapped("hi\n", 0)))
	parsed := parseShellTranscript(t, outputsTestID, "echo hi")
	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, ideMetadata(outputsTestID)); err != nil {
		t.Fatal(err)
	}
	if parsed.Session.CommandOutputs != nil {
		t.Fatalf("outputs = %v, want none: the session is in the IDE lane", derefOutputs(parsed.Session.CommandOutputs))
	}
}

func TestCommandOutputsStopWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parsed := parseShellTranscript(t, outputsTestID, "echo hi")
	if err := applyCommandOutputs(ctx, t.TempDir(), []*vendors.ParsedSession{parsed}, ideMetadata(outputsTestID)); err == nil {
		t.Fatal("error = nil, want context.Canceled")
	}
}

func TestCLICommandOutputsSkipRepeatedCommands(t *testing.T) {
	home := outputsTestHome(t)
	writeCLIStore(t, home, outputsTestID,
		cliCall("c1", "git status"), cliResult("c1", cliWrapped("one\n", 0)),
		cliCall("c2", "git status"), cliResult("c2", cliWrapped("two\n", 0)),
		cliCall("c3", "ls"), cliResult("c3", cliWrapped("a\n", 0)),
	)
	parsed := parseShellTranscript(t, outputsTestID, "git status", "ls", "git status")
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(outputsTestID).Entrypoint = entrypointCLI
	if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, metadata); err != nil {
		t.Fatal(err)
	}
	if got, want := derefOutputs(parsed.Session.CommandOutputs), []string{"<nil>", "a\n", "<nil>"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs = %q, want %q: CLI blobs have no order that survives a backup", got, want)
	}
}

func TestParseFilesContextAppliesCommandOutputs(t *testing.T) {
	home := outputsTestHome(t)
	writeIDEBubbles(t, home, outputsTestID, ideBubble{"2026-10-23T10:00:01.000Z", "echo hi", "completed", `{"output":"hi\n","rejected":false}`})
	db, err := sql.Open("sqlite", filepath.Join(GlobalStorage(home), "state.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value) VALUES (?, '{"name":"IDE"}')`, outputsTestID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	path := filepath.Join(home, ".cursor", "projects", "p", "agent-transcripts", outputsTestID, outputsTestID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"echo hi"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, _, err := ParseFilesContext(context.Background(), []string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := derefOutputs(parsed[0].Session.CommandOutputs); !reflect.DeepEqual(got, []string{"hi\n"}) {
		t.Fatalf("outputs = %q, want the stored output", got)
	}
}
