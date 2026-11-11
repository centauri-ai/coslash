package launch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "--send-runner" {
		os.Exit(HeadlessMain(os.Args[2], os.Args[3]))
	}
	if len(os.Args) == 2 && os.Args[1] == "--headless-parent" {
		_, _ = Headless(context.Background(), "claude", os.Getenv("COSLASH_TEST_CWD"), "context", "task", "read-only")
		os.Exit(0)
	}
	if target := filepath.Base(os.Args[0]); target == "claude" || target == "codex" {
		if slices.Contains(os.Args, "--help") {
			fmt.Println("--safe-mode --restricted --strict-mcp-config --tools --ignore-user-config --ignore-rules --disable --sandbox")
			os.Exit(0)
		}
		if os.Getenv("COSLASH_TEST_MODE") == "block" {
			for {
				syscall.Pause()
			}
		}
		if os.Getenv("COSLASH_TEST_MODE") == "children" {
			ready, _ := os.OpenFile(os.Getenv("COSLASH_TEST_READY"), os.O_WRONLY, 0)
			child := exec.Command(os.Args[0], "-p")
			child.Env = append(os.Environ(), "COSLASH_TEST_MODE=block")
			child.ExtraFiles = []*os.File{ready}
			if os.Getenv("COSLASH_TEST_DETACHED") == "1" {
				child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			}
			if child.Start() != nil {
				os.Exit(2)
			}
			fmt.Fprintf(ready, "%d %d\n", os.Getpid(), child.Process.Pid)
			if os.Getenv("COSLASH_TEST_FINISH") != "1" {
				for {
					syscall.Pause()
				}
			}
		}
		prompt, _ := io.ReadAll(os.Stdin)
		cwd, _ := os.Getwd()
		data, _ := json.Marshal(map[string]any{"prompt": string(prompt), "args": os.Args, "cwd": cwd, "pwd": os.Getenv("PWD"), "marker": os.Getenv("CLAUDE_CODE_SESSION_ID")})
		_ = os.WriteFile(os.Getenv("COSLASH_TEST_CAPTURE"), data, 0o600)
		if os.Getenv("COSLASH_TEST_MODE") == "large" {
			for range 100 {
				fmt.Printf("{\"type\":\"item.completed\",\"item\":{\"type\":\"command_execution\",\"aggregated_output\":%q}}\n", strings.Repeat("x", 16<<10))
			}
		}
		if target == "codex" {
			fmt.Println("{\"type\":\"thread.started\",\"thread_id\":\"01234567-89ab-4def-8123-456789abcdef\"}\n{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"Findings\"}}\n{\"type\":\"turn.completed\"}")
			os.Exit(0)
		}
		if os.Getenv("COSLASH_TEST_MODE") == "nonzero" {
			fmt.Fprintln(os.Stderr, "private provider payload")
			os.Exit(7)
		}
		fmt.Println(`{"type":"result","subtype":"success","is_error":false,"result":"Findings","session_id":"01234567-89ab-4def-8123-456789abcdef"}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func headlessFixture(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"claude", "codex"} {
		if err := os.Symlink(executable, filepath.Join(bin, target)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	cwd := t.TempDir()
	t.Setenv("COSLASH_TEST_CWD", cwd)
	return cwd
}

func TestHeadlessAcceptsLargeCodexEventStream(t *testing.T) {
	cwd := headlessFixture(t)
	t.Setenv("COSLASH_TEST_MODE", "large")
	result, err := Headless(context.Background(), "codex", cwd, "history", "task", "read-only")
	if err != nil || result.Output != "Findings" {
		t.Fatalf("completed verbose task: %+v %v", result, err)
	}
}

func TestHeadlessReapsDetachedChild(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			cwd := headlessFixture(t)
			fifo := filepath.Join(t.TempDir(), "ready")
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COSLASH_TEST_MODE", "children")
			t.Setenv("COSLASH_TEST_READY", fifo)
			t.Setenv("COSLASH_TEST_DETACHED", "1")
			if success {
				t.Setenv("COSLASH_TEST_FINISH", "1")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := Headless(ctx, "claude", cwd, "history", "task", "read-only"); done <- err }()
			ready, err := os.Open(fifo)
			if err != nil {
				t.Fatal(err)
			}
			defer ready.Close()
			var agent, child int
			if _, err := fmt.Fscanf(ready, "%d %d\n", &agent, &child); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
			if !success {
				cancel()
			}
			if err := <-done; (success && err != nil) || (!success && !errors.Is(err, ErrSendFailed)) {
				t.Fatalf("result: %v", err)
			}
			if err := syscall.Kill(child, 0); err != syscall.ESRCH {
				t.Fatalf("detached child survived cleanup: %v", err)
			}
		})
	}
}

func TestHeadlessKeepsPromptOffArgvAndClearsParentContext(t *testing.T) {
	cwd := headlessFixture(t)
	capture := filepath.Join(t.TempDir(), "capture")
	t.Setenv("COSLASH_TEST_CAPTURE", capture)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent-marker")
	result, err := Headless(context.Background(), "claude", cwd, "untrusted history", "--private task", "workspace-write")
	if err != nil || result.Output != "Findings" {
		t.Fatalf("send: %+v %v", result, err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Prompt           string
		Args             []string
		Cwd, Pwd, Marker string
	}
	if json.Unmarshal(data, &value) != nil {
		t.Fatal("capture")
	}
	if !strings.Contains(value.Prompt, "BEGIN UNTRUSTED HANDOFF") || !strings.Contains(value.Prompt, "CURRENT USER TASK\n--private task") || strings.Contains(strings.Join(value.Args, " "), "private task") || value.Cwd != cwd || value.Pwd != cwd || value.Marker != "" {
		t.Fatalf("launch boundary: %+v", value)
	}
	if _, err := Headless(context.Background(), "claude", cwd, "context", " ", "read-only"); err == nil {
		t.Fatal("empty task ran")
	}
	if _, err := Headless(context.Background(), "claude", cwd, strings.Repeat("x", MaxHandoffBytes+1), "task", "read-only"); err == nil {
		t.Fatal("oversized handoff ran")
	}
}

func TestHeadlessCancellationAndParentDeathStopDescendants(t *testing.T) {
	for _, test := range []struct{ parentDeath, detached, deadline bool }{{false, false, false}, {true, false, false}, {true, true, false}, {false, true, true}} {
		parentDeath := test.parentDeath
		t.Run(fmt.Sprint(test), func(t *testing.T) {
			cwd := headlessFixture(t)
			fifo := filepath.Join(t.TempDir(), "ready")
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COSLASH_TEST_MODE", "children")
			t.Setenv("COSLASH_TEST_READY", fifo)
			if test.detached {
				t.Setenv("COSLASH_TEST_DETACHED", "1")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var requestContext context.Context = ctx
			if test.deadline {
				requestContext = deadlineOnCancel{ctx}
			}
			done := make(chan error, 1)
			var parent *exec.Cmd
			if parentDeath {
				executable, _ := os.Executable()
				parent = exec.Command(executable, "--headless-parent")
				if err := parent.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = parent.Process.Kill() })
			} else {
				go func() { _, err := Headless(requestContext, "claude", cwd, "context", "task", "read-only"); done <- err }()
			}
			ready, err := os.Open(fifo)
			if err != nil {
				t.Fatal(err)
			}
			defer ready.Close()
			reader := bufio.NewReader(ready)
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			var agent, child int
			if _, err := fmt.Sscanf(line, "%d %d", &agent, &child); err != nil {
				t.Fatal(err)
			}
			if parentDeath {
				_ = parent.Process.Kill()
				_ = parent.Wait()
			} else {
				cancel()
				if err := <-done; !errors.Is(err, ErrSendFailed) || test.deadline && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("cancel: %v", err)
				}
			}
			if _, err := io.ReadAll(reader); err != nil {
				t.Fatal(err)
			}
			if !parentDeath {
				for _, pid := range []int{agent, child} {
					if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
						t.Fatalf("child %d not reaped: %v", pid, err)
					}
				}
			}
		})
	}
}

func TestHeadlessPoliciesForBothAgents(t *testing.T) {
	for _, target := range []string{"claude", "codex"} {
		for _, access := range []string{"read-only", "workspace-write"} {
			t.Run(target+access, func(t *testing.T) {
				cwd := headlessFixture(t)
				capture := filepath.Join(t.TempDir(), "capture")
				t.Setenv("COSLASH_TEST_CAPTURE", capture)
				if _, err := Headless(context.Background(), target, cwd, "history", "task", access); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				var got struct{ Args []string }
				if json.Unmarshal(data, &got) != nil {
					t.Fatal("capture")
				}
				if target == "claude" {
					index := slices.Index(got.Args, "--tools")
					if index < 0 {
						t.Fatal("no tool policy")
					}
					expected := "Read,Glob,Grep"
					if access == "workspace-write" {
						expected += ",Edit,Write"
					}
					if got.Args[index+1] != expected {
						t.Fatalf("tools: %v", got.Args)
					}
				} else {
					index := slices.Index(got.Args, "--sandbox")
					if index < 0 || got.Args[index+1] != access || !slices.Contains(got.Args, "--ignore-user-config") {
						t.Fatalf("sandbox: %v", got.Args)
					}
				}
			})
		}
	}
}

func TestHeadlessCapacityAndDeadline(t *testing.T) {
	cwd := headlessFixture(t)
	func() {
		sendSlots <- struct{}{}
		sendSlots <- struct{}{}
		defer func() { <-sendSlots; <-sendSlots }()
		if _, err := Headless(context.Background(), "claude", cwd, "history", "task", "read-only"); !errors.Is(err, ErrSendBusy) {
			t.Fatalf("capacity: %v", err)
		}
	}()
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	if _, err := Headless(ctx, "claude", cwd, "history", "task", "read-only"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := Headless(context.Background(), "claude", cwd, "history", "task", "read-only"); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
	t.Setenv("COSLASH_TEST_MODE", "nonzero")
	if _, err := Headless(context.Background(), "claude", cwd, "history", "task", "read-only"); !errors.Is(err, ErrSendFailed) || !strings.Contains(err.Error(), "exit status 7") || strings.Contains(err.Error(), "private provider payload") {
		t.Fatalf("lost exit cause: %v", err)
	}
}

type deadlineOnCancel struct{ context.Context }

func (c deadlineOnCancel) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}
func (c deadlineOnCancel) Value(any) any { return nil }

func TestSendOutputBoundsAndFraming(t *testing.T) {
	text := strings.Repeat("é", maxSendOutputBytes)
	data, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": text}})
	output := sendOutput{target: "codex"}
	for _, b := range data {
		_, _ = output.Write([]byte{b})
	}
	_, _ = output.Write([]byte("\n{\"type\":\"turn.completed\"}"))
	r, err := output.finish()
	if err != nil || !utf8.ValidString(r.Output) || len(r.Output) > maxSendOutputBytes || !strings.HasSuffix(r.Output, "[output truncated]") {
		t.Fatalf("output bound: %d %v", len(r.Output), err)
	}
	oversized := sendOutput{target: "codex"}
	n, err := oversized.Write([]byte(strings.Repeat("x", maxSendEventBytes+1) + "\n{\"type\":\"turn.completed\"}\n"))
	if err != nil || n <= maxSendEventBytes {
		t.Fatal("oversized stream not drained")
	}
	if _, err := oversized.finish(); !errors.Is(err, ErrSendFailed) {
		t.Fatal("oversized event accepted")
	}
	for _, bad := range []string{"invalid\n{\"type\":\"turn.completed\"}", "{\"type\":\"turn.completed\"}\n{\"type\":\"turn.completed\"}", "{\"type\":\"error\"}\n{\"type\":\"turn.completed\"}"} {
		if _, err := parseSendResult("codex", []byte(bad)); !errors.Is(err, ErrSendFailed) {
			t.Fatal("invalid completion accepted")
		}
	}
}

func TestCodexTrustUsesWholeTableForQuotedDottedPaths(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "dir.with.dots and \"quotes\"")
	args, err := headlessArgs("codex", "read-only", cwd)
	if err != nil {
		t.Fatal(err)
	}
	var table string
	for _, arg := range args {
		if strings.HasPrefix(arg, "projects=") {
			table = arg
		}
		if strings.HasPrefix(arg, "projects.") {
			t.Fatal("literal dotted override used")
		}
	}
	for path := cwd; ; path = filepath.Dir(path) {
		key, _ := json.Marshal(path)
		if !strings.Contains(table, string(key)+`={trust_level="untrusted"}`) {
			t.Fatalf("missing ancestor %q", path)
		}
		if path == filepath.Dir(path) {
			break
		}
	}
}

func TestCodexTrustSettingsRejectAnOversizedArgument(t *testing.T) {
	cwd := string(filepath.Separator)
	for range 500 {
		cwd = filepath.Join(cwd, "x")
	}
	if len(cwd) >= 4096 {
		t.Fatalf("test path exceeds PATH_MAX: %d", len(cwd))
	}
	if _, err := codexProjectsSetting(cwd); !errors.Is(err, ErrSendConfigTooLarge) {
		t.Fatalf("oversized trust settings error = %v", err)
	}
}

func TestHeadlessRequiresStructuredCompletion(t *testing.T) {
	for _, test := range []struct {
		target, data string
		err          error
	}{
		{"claude", `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`, nil},
		{"claude", `{"type":"result","subtype":"error","is_error":true}`, ErrSendFailed},
		{"claude", `{"type":"result","subtype":"success","is_error":false,"permission_denials":[{}]}`, ErrSendPermission},
		{"codex", "{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"ok\"}}\n{\"type\":\"turn.completed\"}", nil},
		{"codex", `{"type":"turn.failed"}`, ErrSendFailed}, {"codex", `{"type":"turn.started"}`, ErrSendFailed}, {"codex", "broken", ErrSendFailed},
	} {
		_, err := parseSendResult(test.target, []byte(test.data))
		if !errors.Is(err, test.err) {
			t.Fatalf("%s %s: %v", test.target, test.data, err)
		}
	}
}

func parseSendResult(target string, data []byte) (SendResult, error) {
	output := sendOutput{target: target}
	_, _ = output.Write(data)
	return output.finish()
}
