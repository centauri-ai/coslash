package grokcli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutableWithoutPATH(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("PATH", t.TempDir())
	custom := t.TempDir()
	t.Setenv("GROK_HOME", custom)
	for _, root := range []string{filepath.Join(userHome, ".grok"), custom} {
		bin := filepath.Join(root, "bin")
		if err := os.MkdirAll(bin, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(bin, "grok.exe")
		if err := os.WriteFile(path, []byte("stub"), 0o700); err != nil {
			t.Fatal(err)
		}
		if got := Executable(); got != path {
			t.Fatalf("executable = %q, want %q", got, path)
		}
	}
	path := filepath.Join(custom, "bin", "grok.exe")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := Executable(); got != filepath.Join(userHome, ".grok", "bin", "grok.exe") {
		t.Fatalf("accepted directory: %q", got)
	}
}
