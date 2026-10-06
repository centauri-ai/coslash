package launch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestTerminalWithPromptRejectsUnavailableWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, workingDirectory := range []string{"", filepath.Join(root, "missing"), file} {
		err := TerminalWithPrompt(context.Background(), "invalid", vendors.AgentCodex, workingDirectory, "", NewSession, "", "")
		if !errors.Is(err, ErrWorkingDirectoryUnavailable) {
			t.Fatalf("TerminalWithPrompt(%q) error = %v, want unavailable working directory", workingDirectory, err)
		}
	}
}

func TestGrokSendWithoutMessageKeepsTheHandoff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("secure interactive prompts require a POSIX terminal")
	}
	provideFakeExpect(t)
	t.Setenv("COSLASH_HOME", t.TempDir())
	command, path, err := cliCommandWithPrompt(vendors.AgentGrok, "", NewSession, "private prior notes", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeHandoffFile(path) })
	if strings.Contains(command, "unknown agent") || !strings.Contains(command, "expect") || !strings.Contains(command, `{\x1b\[\?2004h}`) {
		t.Fatalf("command = %q", command)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "private prior notes") {
		t.Fatalf("staged prompt = %q, err = %v", contents, err)
	}
}

func TestOpenMacTerminalPropagatesCancellation(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	original := runOSAScript
	t.Cleanup(func() { runOSAScript = original })
	runOSAScript = func(ctx context.Context, _ ...string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := openMacTerminal(ctx, "/repo", "codex"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestCLICommandWithPromptStartsInteractiveTargetWithHandoff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("secure interactive prompts require a POSIX terminal")
	}
	provideFakeExpect(t)
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex, vendors.AgentOpenCode, vendors.AgentCursor, vendors.AgentGrok} {
		t.Run(agent, func(t *testing.T) {
			command, handoffPath, err := cliCommandWithPrompt(agent, "", NewSession, "private prior notes", "fix it")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = removeHandoffFile(handoffPath) })
			if strings.Contains(command, "fix it") || strings.Contains(command, "private prior notes") || !strings.Contains(command, "expect") {
				t.Fatalf("prompt exposed in command = %q", command)
			}
			ready := `{\x1b\[\?2004h}`
			if agent == vendors.AgentOpenCode {
				ready = `{Ask anything}`
			} else if agent == vendors.AgentCursor {
				ready = `{0 in}`
			} else if agent == vendors.AgentGrok {
				ready = `{\x1b\[\?2004h}`
			}
			submit := `after 300; send -- "\r"`
			if agent == vendors.AgentCursor || agent == vendors.AgentCodex || agent == vendors.AgentGrok {
				submit = `after 2000; send -- "\r"`
			}
			if !strings.Contains(command, ready) || !strings.Contains(command, submit) || strings.Contains(command, "/dev/tty") {
				t.Fatalf("interactive relay is not ready-gated for %s: %q", agent, command)
			}
			contents, err := os.ReadFile(handoffPath)
			if err != nil || !strings.HasPrefix(string(contents), "\x1b[200~") ||
				!strings.HasSuffix(string(contents), "\x1b[201~") || !strings.Contains(string(contents), "fix it") {
				t.Fatalf("staged prompt = %q, err = %v", contents, err)
			}
			if agent == vendors.AgentClaude {
				if !strings.Contains(command, "unset CLAUDE_CODE_CHILD_SESSION") {
					t.Fatalf("Claude handoff can inherit a child-session marker: %q", command)
				}
				if strings.Contains(string(contents), "private prior notes") {
					t.Fatalf("Claude user prompt contains prior context: %q", contents)
				}
				context, err := os.ReadFile(handoffPath + ".context")
				if err != nil || !strings.Contains(string(context), "private prior notes") || !strings.Contains(command, "--append-system-prompt-file") {
					t.Fatalf("Claude system context = %q, command = %q, err = %v", context, command, err)
				}
			} else if !strings.Contains(string(contents), "private prior notes") {
				t.Fatalf("missing prior context in %s prompt", agent)
			}
			if agent == vendors.AgentCursor {
				if handoffPath == "" || !strings.Contains(string(contents), "private prior notes") || strings.Contains(command, "--print") {
					t.Fatalf("Cursor first prompt = %q, handoff = %q", command, handoffPath)
				}
				return
			}
			if handoffPath == "" {
				t.Fatal("missing handoff file")
			}
		})
	}
}

func TestCLICommandWithPromptStopsOptionParsing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("secure interactive prompts require a POSIX terminal")
	}
	provideFakeExpect(t)
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex, vendors.AgentOpenCode} {
		for _, handoff := range []string{"", "context"} {
			t.Run(agent+handoff, func(t *testing.T) {
				command, handoffPath, err := cliCommandWithPrompt(
					agent, "", NewSession, handoff, "--dangerously-skip-permissions",
				)
				if err != nil {
					t.Fatal(err)
				}
				if handoffPath != "" {
					t.Cleanup(func() { _ = os.Remove(handoffPath) })
				}
				contents, err := os.ReadFile(handoffPath)
				if err != nil || !strings.Contains(string(contents), "--dangerously-skip-permissions") || strings.Contains(command, "--dangerously-skip-permissions") {
					t.Fatalf("prompt exposed or missing: %q, %q, %v", command, contents, err)
				}
			})
		}
	}
}

func TestClaudeMultilineRequestWaitsBeforeSubmitting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("secure interactive prompts require a POSIX terminal")
	}
	provideFakeExpect(t)
	t.Setenv("COSLASH_HOME", t.TempDir())
	command, path, err := cliCommandWithPrompt(vendors.AgentClaude, "", NewSession, "prior notes", "coSlash handoff ID: test\n\nrequest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeHandoffFile(path) })
	if !strings.Contains(command, `after 2000; send -- "\r"`) {
		t.Fatalf("multiline Claude request submits before the paste settles: %q", command)
	}
}

func provideFakeExpect(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "expect"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSecurePromptRequiresExpect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX terminal relay")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if securePromptAvailable() {
		t.Fatal("relay available without expect")
	}
	if err := os.WriteFile(filepath.Join(dir, "expect"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !securePromptAvailable() {
		t.Fatal("relay unavailable with expect")
	}
}

func TestFirstPromptRejectsResume(t *testing.T) {
	if _, _, err := cliCommandWithPrompt(vendors.AgentClaude, "01234567-89ab-cdef-0123-456789abcdef", ResumeSession, "", "request"); err == nil {
		t.Fatal("local resume accepted first prompt")
	}
	if err := RemoteTerminalWithPrompt(context.Background(), "terminal", "agent-box", vendors.AgentClaude, "/work", "01234567-89ab-cdef-0123-456789abcdef", ResumeSession, "", "request"); err == nil {
		t.Fatal("remote resume accepted first prompt")
	}
}

func TestGrokRelayDeliversMultilinePromptOnce(t *testing.T) {
	if os.Getenv("COSLASH_TEST_GROK_INPUT") == "1" {
		command := exec.Command("stty", "raw", "-echo")
		command.Stdin = os.Stdin
		if err := command.Run(); err != nil {
			panic(err)
		}
		fmt.Fprint(os.Stdout, "\x1b[?2004h\x1b[32m❯ \x1b[0m")
		var received []byte
		for {
			var b [1]byte
			if _, err := os.Stdin.Read(b[:]); err != nil {
				panic(err)
			}
			received = append(received, b[0])
			if b[0] == '\r' {
				break
			}
		}
		fmt.Fprintf(os.Stdout, "RECEIVED:%x\n", received)
		os.Exit(0)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX terminal relay")
	}
	if _, err := exec.LookPath("expect"); err != nil {
		t.Skip("system expect unavailable")
	}
	t.Setenv("COSLASH_HOME", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	prompt := "--private-first-line\nsecond line ' $ `"
	base := shellJoin("env", "COSLASH_TEST_GROK_INPUT=1", executable, "-test.run=^TestGrokRelayDeliversMultilinePromptOnce$")
	command, path, err := secureTerminalInputCommand(base, prompt, vendors.AgentGrok, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeHandoffFile(path) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	configureReviewProcess(child)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	received, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("relay: %v, output=%s, stderr=%s", err, received, &stderr)
	}
	want := fmt.Sprintf("RECEIVED:%x", "\x1b[200~"+prompt+"\x1b[201~\r")
	if strings.Count(string(received), want) != 1 {
		t.Fatalf("delivery missing or duplicated: %q", received)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("staged prompt remains: %v", err)
	}
}
