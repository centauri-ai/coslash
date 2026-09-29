//go:build windows

package launch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCursorReviewerUsesInstalledPowerShellLauncher(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	path := filepath.Join(local, "cursor-agent", "agent.ps1")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "powershell.exe"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim)
	if got := CursorCLIExecutable(home); got != path {
		t.Fatalf("installed Cursor CLI = %q, want %q", got, path)
	}
	if !ReviewerAvailable("cursor") {
		t.Fatal("Cursor reviewer is unavailable despite the installed launcher")
	}
	spec, err := reviewCLICommand("cursor", `C:\repo`, "review", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path, "--print", "--mode=ask", "--sandbox", "disabled", "--trust", "--add-dir", `C:\repo`, "--output-format", "text"}
	if spec.bin != "powershell.exe" || !reflect.DeepEqual(spec.args, want) {
		t.Fatalf("Cursor review command = %q %#v, want powershell.exe %#v", spec.bin, spec.args, want)
	}
	if err := os.Remove(filepath.Join(shim, "powershell.exe")); err != nil {
		t.Fatal(err)
	}
	if ReviewerAvailable("cursor") {
		t.Fatal("Cursor reviewer is available without PowerShell")
	}
}

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
