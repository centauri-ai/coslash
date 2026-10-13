//go:build !windows

package settings

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func SSHControlPath() (string, error) {
	dir := fmt.Sprintf("/tmp/coslash-%d", os.Getuid())
	if err := ensureSSHSocketDir(dir, os.Getuid()); err != nil {
		return "", err
	}
	// Bound the path even with a long home or TMPDIR; keep Local installs isolated.
	homePath, err := filepath.Abs(Home())
	if err != nil {
		return "", err
	}
	home := sha256.Sum256([]byte(homePath))
	return filepath.Join(dir, fmt.Sprintf("cm-%x-%%C", home[:8])), nil
}

func ensureSSHSocketDir(dir string, uid int) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create SSH socket directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect SSH socket directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || stat.Uid != uint32(uid) {
		return fmt.Errorf("SSH socket directory must be a private directory owned by uid %d", uid)
	}
	return nil
}
