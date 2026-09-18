package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex} {
		t.Run(agent, func(t *testing.T) {
			command, handoffPath, err := cliCommandWithPrompt(agent, "", NewSession, "context", "fix it")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(handoffPath) })
			if handoffPath == "" || !strings.Contains(command, shellQuote("fix it")) {
				t.Fatalf("command = %q, handoff = %q", command, handoffPath)
			}
			if agent == vendors.AgentClaude && !strings.Contains(command, "--append-system-prompt-file") {
				t.Fatalf("Claude command lacks handoff flag: %q", command)
			}
			if agent == vendors.AgentCodex && !strings.Contains(command, "developer_instructions=") {
				t.Fatalf("Codex command lacks handoff override: %q", command)
			}
		})
	}
}

func TestCLICommandWithPromptStopsOptionParsing(t *testing.T) {
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
				if !strings.Contains(command, "'--' '--dangerously-skip-permissions'") {
					t.Fatalf("command does not terminate option parsing: %q", command)
				}
			})
		}
	}
}
