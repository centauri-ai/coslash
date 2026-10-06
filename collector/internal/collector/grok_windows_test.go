package collector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
)

func TestGrokWindowsWaitingAndEntrypoint(t *testing.T) {
	process := exec.Command(os.Args[0], "-test.run=^$")
	if err := process.Run(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, plan, updates, prompt, entrypoint, status string
		pid                                             int
	}{
		{name: "plan approval", pid: os.Getpid(), plan: `{"awaiting_plan_approval":true}`, entrypoint: "grok-cli", status: "waiting"},
		{name: "tool permission", pid: os.Getpid(), updates: `{"params":{"update":{"sessionUpdate":"tool_call","toolCallId":"p1","title":"run_terminal_cmd","status":"pending"}}}`, entrypoint: "grok-cli", status: "waiting"},
		{name: "metadata tool permission", pid: os.Getpid(), updates: `{"params":{"update":{"sessionUpdate":"tool_call","toolCallId":"p1","title":"run_terminal_command"},"_meta":{"updateParams":{"status":"Pending"}}}}`, entrypoint: "grok-cli", status: "waiting"},
		{name: "running tool", pid: os.Getpid(), updates: `{"params":{"update":{"sessionUpdate":"tool_call","toolCallId":"p1","title":"run_terminal_cmd","status":"pending"}}}` + "\n" + `{"params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"p1","status":"in_progress"}}}`, entrypoint: "grok-cli", status: "busy"},
		{name: "user question", pid: os.Getpid(), updates: `{"params":{"update":{"sessionUpdate":"tool_call","toolCallId":"q1","title":"ask_user_question"}}}`, entrypoint: "grok-cli", status: "waiting"},
		{name: "exited plan approval", pid: process.Process.Pid, plan: `{"awaiting_plan_approval":true}`, entrypoint: "grok-cli"},
		{name: "exited user question", pid: process.Process.Pid, updates: `{"params":{"update":{"sessionUpdate":"tool_call","toolCallId":"q1","title":"ask_user_question"}}}`, entrypoint: "grok-cli", status: "idle"},
		{name: "exited headless", pid: process.Process.Pid, prompt: `{"is_non_interactive":true}`, entrypoint: "grok-headless"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "Grok Home")
			t.Setenv("GROK_HOME", home)
			dir := filepath.Join(home, "sessions", "C%3A%5CWork%5CGrok%20Review", "session")
			writeGrokFile(t, filepath.Join(dir, "summary.json"), `{"info":{"id":"session","cwd":"C:\\Work\\Grok Review"},"chat_format_version":1}`)
			for name, body := range map[string]string{"plan_mode.json": test.plan, "updates.jsonl": test.updates, "prompt_context.json": test.prompt} {
				if body != "" {
					writeGrokFile(t, filepath.Join(dir, name), body)
				}
			}
			writeGrokFile(t, filepath.Join(home, "active_sessions.json"), fmt.Sprintf(`[{"session_id":"session","pid":%d}]`, test.pid))
			parsed, metadata, err := grok.CollectContext(context.Background(), 0)
			if err != nil {
				t.Fatal(err)
			}
			roots := finalizeSessions(parsed, map[string]*vendors.SessionMetadata{vendors.AgentGrok: metadata})
			if len(roots) != 1 {
				t.Fatalf("collected %d roots, want 1", len(roots))
			}
			s := roots[0].Session
			if deref(s.Status) != test.status || deref(s.Entrypoint) != test.entrypoint || s.WorkingDirectory != `C:\Work\Grok Review` {
				t.Fatalf("status=%q entrypoint=%q cwd=%q", deref(s.Status), deref(s.Entrypoint), s.WorkingDirectory)
			}
		})
	}
}
