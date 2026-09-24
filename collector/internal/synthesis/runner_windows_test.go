package synthesis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	t.Setenv("PATH", t.TempDir())

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
