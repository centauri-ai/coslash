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
			command, handoffPath, err := cliCommandWithPrompt(agent, "", NewSession, "context", "fix it")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(handoffPath) })
			if strings.Contains(command, "fix it") || strings.Contains(command, "context") || !strings.Contains(command, "script") {
				t.Fatalf("prompt exposed in command = %q", command)
			}
			contents, err := os.ReadFile(handoffPath)
			if err != nil || !strings.Contains(string(contents), "fix it") {
				t.Fatalf("staged prompt = %q, err = %v", contents, err)
			}
			if agent == vendors.AgentCursor {
				if handoffPath == "" || !strings.Contains(string(contents), "context") || strings.Contains(command, "--print") {
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

func TestSecurePromptRequiresTerminalFeederTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX terminal feeder")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if securePromptAvailable() {
		t.Fatal("feeder available without script and mkfifo")
	}
	for _, name := range []string{"script", "mkfifo"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !securePromptAvailable() {
		t.Fatal("feeder unavailable with script and mkfifo")
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
