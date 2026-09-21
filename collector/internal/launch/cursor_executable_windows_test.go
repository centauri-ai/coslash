//go:build windows

package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorCLIExecutableFindsDefaultWindowsInstall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "AppData", "Local", "cursor-agent", "agent.cmd")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	shimDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDirectory, "agent.cmd"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDirectory)
	if got := CursorCLIExecutable(home); got != path {
		t.Fatalf("Cursor CLI executable = %q, want installed launcher %q instead of PATH shim", got, path)
	}
}
