//go:build !windows && !linux && !darwin

package session

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
