//go:build !windows

package synthesis

import (
	"os"
	"syscall"
)

func lockAccountingFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
