//go:build !windows

package opencode

import (
	"fmt"
	"os"
	"syscall"
)

func deletionFileIdentity(info os.FileInfo, _ string) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrSessionUnverified
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}

func syncDeletionDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
