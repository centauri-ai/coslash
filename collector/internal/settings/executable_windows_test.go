package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorExecutableFindsWindowsInstallWithoutPATH(t *testing.T) {
	localAppData := t.TempDir()
	root := filepath.Join(localAppData, "cursor-agent")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("PATH", t.TempDir())

	for _, extension := range []string{".cmd", ".exe"} {
		for _, name := range []string{"agent", "cursor-agent"} {
			path := filepath.Join(root, name+extension)
			if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
				t.Fatal(err)
			}
			if got := CursorExecutable(); got != path {
				t.Fatalf("CursorExecutable() = %q, want %q", got, path)
			}
			if !isCursorInstall(path) {
				t.Fatalf("isCursorInstall(%q) = false", path)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}
	installedAgent := filepath.Join(root, "agent.cmd")
	if err := os.WriteFile(installedAgent, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	if got := CursorExecutable(); got != "agent" {
		t.Fatalf("CursorExecutable() = %q, want agent on PATH", got)
	}
	if err := os.Remove(installedAgent); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "agent.exe")
	if err := os.WriteFile(foreign, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(foreign))
	if got := CursorExecutable(); got != "cursor-agent" {
		t.Fatalf("CursorExecutable() = %q, want cursor-agent for an unrelated agent", got)
	}
}
