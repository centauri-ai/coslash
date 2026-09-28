package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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

func TestOpenMacTerminalPropagatesCancellation(t *testing.T) {
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
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex, vendors.AgentOpenCode, vendors.AgentCursor} {
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
			}
			submit := `after 300; send -- "\r"`
			if agent == vendors.AgentCursor {
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
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex} {
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
	if !securePromptAvailable() {
		t.Skip("secure terminal relay unavailable")
	}
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
