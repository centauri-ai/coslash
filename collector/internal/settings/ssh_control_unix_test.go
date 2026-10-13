//go:build !windows

package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSSHControlPathIsolatesLocalHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	first, err := SSHControlPath()
	if err != nil {
		t.Fatal(err)
	}
	again, err := SSHControlPath()
	if err != nil || again != first {
		t.Fatalf("same Local home does not share its socket: %q, %v", again, err)
	}
	t.Setenv("COSLASH_HOME", filepath.Join(home, "other"))
	other, err := SSHControlPath()
	if err != nil || other == first {
		t.Fatalf("different Local homes share a socket: %q, %v", other, err)
	}
}

func TestSSHSocketDirectoryRejectsUnsafeExistingPaths(t *testing.T) {
	for _, kind := range []string{"private", "public", "foreign", "symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "ssh")
			uid := os.Getuid()
			switch kind {
			case "private", "public", "foreign":
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "public" {
					if err := os.Chmod(dir, 0o777); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "foreign" {
					uid++
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(dir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := ensureSSHSocketDir(dir, uid)
			if (err == nil) != (kind == "private") {
				t.Fatalf("directory %s: %v", kind, err)
			}
		})
	}
}
