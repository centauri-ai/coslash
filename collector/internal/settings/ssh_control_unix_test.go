//go:build !windows

package settings

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHControlDirectorySelection(t *testing.T) {
	short, err := os.MkdirTemp("/tmp", "f25-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	for _, kind := range []string{"private", "long", "public", "missing"} {
		t.Run(kind, func(t *testing.T) {
			preferred := short
			if kind == "long" || kind == "missing" {
				preferred = filepath.Join(t.TempDir(), strings.Repeat("long", 30))
				if kind != "missing" {
					if err := os.Mkdir(preferred, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "public" {
				if err := os.Chmod(preferred, 0o777); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Chmod(preferred, 0o700) }()
			}
			path, err := sshControlPath(t.TempDir(), preferred)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(path)
			if (dir == preferred) != (kind == "private") {
				t.Fatalf("directory selection for %s: %s", kind, dir)
			}
			if kind != "private" {
				t.Cleanup(func() { _ = os.Remove(dir) })
			}
			if len(strings.Replace(path, "%C", strings.Repeat("a", 40), 1))+17+1 > 104 {
				t.Fatalf("OpenSSH socket path exceeds macOS limit: %s", path)
			}
		})
	}
}

func TestSSHControlPathReplacesInvalidSavedDirectory(t *testing.T) {
	for _, kind := range []string{"missing", "public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			path, err := sshControlPath(home, "")
			if err != nil {
				t.Fatal(err)
			}
			old := filepath.Dir(path)
			t.Cleanup(func() { _ = os.Remove(old) })
			switch kind {
			case "missing", "symlink":
				if err := os.Remove(old); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink(t.TempDir(), old); err != nil {
						t.Fatal(err)
					}
				}
			case "public":
				if err := os.Chmod(old, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			replacement, err := sshControlPath(home, "")
			if err != nil || replacement == path {
				t.Fatalf("unsafe saved directory was not replaced: %s, %v", replacement, err)
			}
			t.Cleanup(func() { _ = os.Remove(filepath.Dir(replacement)) })
			again, err := sshControlPath(home, "")
			if err != nil || again != replacement {
				t.Fatalf("replacement was not persisted: %s, %v", again, err)
			}
		})
	}
}

func TestSSHControlPathSharedAcrossProcesses(t *testing.T) {
	if os.Getenv("F25_CONTROL_DIR_CHILD") == "1" {
		path, err := SSHControlPath()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Print(path)
		os.Exit(0)
	}
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("TMPDIR", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("F25_CONTROL_DIR_CHILD", "1")
	commands := make([]*exec.Cmd, 4)
	outputs := make([]strings.Builder, len(commands))
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestSSHControlPathSharedAcrossProcesses$")
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if commands[i].ProcessState == nil {
				_ = commands[i].Process.Kill()
				_ = commands[i].Wait()
			}
		})
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	path, err := SSHControlPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Dir(path)) })
	for i := range outputs {
		if outputs[i].String() != path {
			t.Fatalf("Local processes selected different sockets: %q, %q", outputs[i].String(), path)
		}
	}
}

func TestSSHControlPathAvoidsPredictablePublicDirectory(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	attackerDir := filepath.Join(t.TempDir(), fmt.Sprintf("coslash-%d", os.Getuid()))
	if err := os.Mkdir(attackerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(attackerDir, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", attackerDir)
	t.Setenv("XDG_RUNTIME_DIR", attackerDir)
	path, err := SSHControlPath()
	if err != nil {
		t.Fatalf("an unsafe predictable directory disabled SSH: %v", err)
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == fmt.Sprintf("coslash-%d", os.Getuid()) {
		t.Fatalf("SSH still uses a predictable public directory: %s", dir)
	}
	if err := ensureSSHSocketDir(dir, os.Getuid()); err != nil {
		t.Fatal(err)
	}
}

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
