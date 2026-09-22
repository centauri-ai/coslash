//go:build windows

package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorCLIExecutableFindsDefaultWindowsInstall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "AppData", "Local", "cursor-agent", "agent.ps1")
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

func TestCursorExecutablesUseRedirectedKnownFolders(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	ide := filepath.Join(local, "Programs", "cursor", "Cursor.exe")
	cli := filepath.Join(local, "cursor-agent", "agent.ps1")
	for _, path := range []string{ide, cli} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := CursorExecutable(home); got != ide {
		t.Fatalf("Cursor executable = %q, want %q", got, ide)
	}
	if got := CursorCLIExecutable(home); got != cli {
		t.Fatalf("Cursor CLI executable = %q, want %q", got, cli)
	}
}

func TestCursorExecutableFindsSystemInstall(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	programFiles := t.TempDir()
	t.Setenv("ProgramW6432", programFiles)
	t.Setenv("ProgramFiles", programFiles)
	path := filepath.Join(programFiles, "cursor", "Cursor.exe")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CursorExecutable(home); got != path {
		t.Fatalf("Cursor executable = %q, want %q", got, path)
	}
}
