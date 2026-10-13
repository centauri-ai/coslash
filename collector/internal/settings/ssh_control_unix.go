//go:build !windows

package settings

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func SSHControlPath() (string, error) {
	home, err := filepath.Abs(Home())
	if err != nil {
		return "", err
	}
	var preferred string
	switch runtime.GOOS {
	case "darwin":
		preferred = os.Getenv("TMPDIR")
	case "linux":
		preferred = os.Getenv("XDG_RUNTIME_DIR")
	}
	return sshControlPath(home, preferred)
}

func sshControlPath(home, preferred string) (string, error) {
	hash := sha256.Sum256([]byte(home))
	name := fmt.Sprintf("cm-%x-%%C", hash[:8])
	stateDir := filepath.Join(home, "ssh")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	if err := ensureSSHSocketDir(stateDir, os.Getuid()); err != nil {
		return "", err
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, ".control-directory.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()
	// Serialize selection across Local processes so their masters share one directory.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	record := filepath.Join(stateDir, "control-directory")
	saved, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	dir := string(saved)
	if usableSSHSocketDir(dir, name) {
		return filepath.Join(dir, name), nil
	}
	dir = preferred
	created := false
	if !usableSSHSocketDir(dir, name) {
		dir, err = os.MkdirTemp("/tmp", "cs-")
		if err != nil {
			return "", err
		}
		created = true
		if !usableSSHSocketDir(dir, name) {
			_ = os.Remove(dir)
			return "", fmt.Errorf("SSH socket directory cannot fit the Unix socket path limit")
		}
	}
	if err := os.WriteFile(record, []byte(dir), 0o600); err != nil {
		if created {
			_ = os.Remove(dir)
		}
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func usableSSHSocketDir(dir, name string) bool {
	// OpenSSH expands %C to 40 hex digits, appends 17 bytes, and needs a NUL.
	return filepath.IsAbs(dir) && !strings.Contains(dir, "%") && !strings.Contains(dir, "${") &&
		len(filepath.Join(dir, name))+38+17+1 <= 104 && ensureSSHSocketDir(dir, os.Getuid()) == nil
}

func ensureSSHSocketDir(dir string, uid int) error {
	info, err := os.Lstat(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("inspect SSH socket directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || stat.Uid != uint32(uid) {
		return fmt.Errorf("SSH socket directory must be a private directory owned by uid %d", uid)
	}
	return nil
}
