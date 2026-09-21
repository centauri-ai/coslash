//go:build windows

package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorCLIExecutableFindsDefaultWindowsInstall(t *testing.T) {
	t.Setenv("PATH", "")
	home := t.TempDir()
	path := filepath.Join(home, "AppData", "Local", "cursor-agent", "agent.cmd")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CursorCLIExecutable(home); got != path {
		t.Fatalf("Cursor CLI executable = %q, want %q", got, path)
	}
}
