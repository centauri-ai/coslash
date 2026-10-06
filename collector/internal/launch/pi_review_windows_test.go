package launch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/review"
)

func TestPiReviewManagedWindowsShim(t *testing.T) {
	home := t.TempDir()
	cli := filepath.Join(home, ".pi", "agent", "bin", "pi.cmd")
	if err := os.MkdirAll(filepath.Dir(cli), 0o700); err != nil {
		t.Fatal(err)
	}
	_, flags := reviewHelpRequirements("pi")
	script := "@echo off\r\n:args\r\nif \"%~1\"==\"\" goto run\r\n" +
		"if \"%~1\"==\"--help\" set pi_probe_help=1\r\n" +
		"if \"%~1\"==\"--append-system-prompt\" (\r\nif not \"%~2\"==\" \" exit /b 21\r\nset pi_probe_append=1\r\n)\r\n" +
		"shift\r\ngoto args\r\n:run\r\nif not defined pi_probe_append exit /b 22\r\n" +
		"if defined pi_probe_help (\r\necho " + strings.Join(flags, " ") + "\r\nexit /b 0\r\n)\r\nmore\r\n"
	if err := os.WriteFile(cli, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	powerShell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", filepath.Dir(powerShell)+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
	if !ReviewCLIAvailable(context.Background(), "pi") {
		t.Fatal("managed Windows Pi shim is unavailable")
	}
	result, err := Review(context.Background(), review.Launch{Reviewer: "pi", WorkingDirectory: t.TempDir(), Prompt: "PRIVATE_STDIN_MARKER"})
	if err != nil || !strings.Contains(result, "PRIVATE_STDIN_MARKER") || !strings.Contains(result, "BEGIN UNTRUSTED WORKTREE DATA") {
		t.Fatalf("Windows Pi review result = %q, %v", result, err)
	}
}
