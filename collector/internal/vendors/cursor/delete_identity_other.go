//go:build !windows

package cursor

import (
	"fmt"
	"os"
	"syscall"
)

func cursorDeleteFileIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrDeleteUnverified
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
