package grokcli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrepareHome(t *testing.T) {
	source := t.TempDir()
	t.Setenv("GROK_HOME", source)
	for _, login := range []string{"missing", "present", "oversized", "directory"} {
		t.Run(login, func(t *testing.T) {
			path := filepath.Join(source, "auth.json")
			switch login {
			case "present":
				if err := os.WriteFile(path, []byte(`{"token":"test"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			scratch := t.TempDir()
			home, err := PrepareHome(scratch)
			if login == "oversized" || login == "directory" {
				if err == nil {
					t.Fatal("accepted invalid login")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if home != filepath.Join(scratch, "home") {
				t.Fatal("home escaped scratch")
			}
			if login == "present" {
				body, err := os.ReadFile(filepath.Join(home, "auth.json"))
				if err != nil || string(body) != `{"token":"test"}` {
					t.Fatalf("login copy: %v", err)
				}
				if runtime.GOOS == "windows" {
					if _, err := os.Readlink(filepath.Join(home, "auth.json")); err == nil {
						t.Fatal("Windows login requires a symlink")
					}
				}
			}
			if _, err := os.Stat(filepath.Join(home, "sessions")); !os.IsNotExist(err) {
				t.Fatal("inherited sessions")
			}
		})
	}
}
