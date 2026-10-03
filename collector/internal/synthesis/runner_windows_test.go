package synthesis

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestCursorCmdShimRuns(t *testing.T) {
	localAppData := t.TempDir()
	root := filepath.Join(localAppData, "cursor-agent")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cursor-agent.cmd"), []byte("@echo off\r\necho %1\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", localAppData)
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(powershell))

	output, err := executeCommand(context.Background(), commandSpec{
		bin:  settings.BackendExecutable(settings.BackendCursor),
		args: []string{"--version"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(output)); got != "--version" {
		t.Fatalf("Cursor .cmd output = %q, want --version", got)
	}
}

func TestPiSynthesisRunsThroughCmdShim(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	directory := t.TempDir()
	response := filepath.Join(directory, "response.json")
	if err := os.WriteFile(response, []byte(`{"goals":["Ship"],"outcome":"from cmd shim","keyDecisions":[],"nextStep":"done"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(directory, "pi.cmd")
	script := "@echo off\r\nset /p prompt=\r\n" +
		"if not \"%prompt%\"==\"private facts\" exit /b 7\r\n" +
		"type \"" + response + "\"\r\n"
	if err := os.WriteFile(cli, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &CLIRunner{Backend: settings.BackendPi, Bin: cli, Model: settings.PiDefaultModel, Timeout: 10 * time.Second}
	got, err := runner.Run(context.Background(), "private facts")
	if err != nil || got.Outcome != "from cmd shim" {
		t.Fatalf("Pi .cmd synthesis = %#v, %v", got, err)
	}
}
