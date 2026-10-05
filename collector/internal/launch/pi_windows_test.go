package launch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestPiWindowsAvailabilityResumeAndFailedSendCleanup(t *testing.T) {
	home := t.TempDir()
	cli := filepath.Join(home, ".pi", "agent", "bin", "pi.cmd")
	if err := os.MkdirAll(filepath.Dir(cli), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("@echo off\r\nif \"%~1\"==\"--version\" echo 1.0.0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", filepath.Join(os.Getenv("SystemRoot"), "System32"))
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("COSLASH_HOME", t.TempDir())
	if !PiAvailable() {
		t.Fatal("installed Pi CLI is unavailable")
	}
	available := false
	for _, option := range HandoffTargetOptions(context.Background()) {
		if option.Agent == vendors.AgentPi {
			available = option.Available
		}
	}
	if !available {
		t.Fatal("Windows Pi handoff target is unavailable")
	}
	cwd := filepath.Join(t.TempDir(), "project's work")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "session's transcript.jsonl")
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "session-id", "cwd": cwd})
	if err := os.WriteFile(transcript, append(header, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	original := localTerminalOpener
	t.Cleanup(func() { localTerminalOpener = original })
	launched := ""
	localTerminalOpener = func(_ context.Context, terminal, agent, directory, command string) error {
		if terminal != settings.TerminalWindows || agent != vendors.AgentPi || directory != cwd {
			t.Fatalf("launch destination = %q %q %q", terminal, agent, directory)
		}
		launched = command
		return nil
	}
	if err := Terminal(context.Background(), settings.TerminalWindows, vendors.AgentPi, cwd, "session-id", ResumeSession, "", transcript); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(launched, powerShellQuote(transcript)) || !strings.Contains(launched, "--session") {
		t.Fatalf("resume did not select exact transcript: %s", launched)
	}
	launched = ""
	if err := Terminal(context.Background(), settings.TerminalWindows, vendors.AgentPi, cwd, "wrong-id", ResumeSession, "", transcript); err == nil || launched != "" {
		t.Fatal("changed transcript identity reached terminal")
	}
	localTerminalOpener = func(context.Context, string, string, string, string) error { return errors.New("terminal failed") }
	if err := TerminalWithPrompt(context.Background(), settings.TerminalWindows, vendors.AgentPi, cwd, "", NewSession, "private handoff", "private prompt"); err == nil {
		t.Fatal("terminal failure was hidden")
	}
	if entries, err := os.ReadDir(handoffDir()); err != nil || len(entries) != 0 {
		t.Fatalf("private files remain after failed Send: %v, %v", entries, err)
	}
}
