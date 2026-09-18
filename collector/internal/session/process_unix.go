//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

func processSignalAlive(err error) bool {
	return err == nil || errors.Is(err, syscall.EPERM)
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	return err == nil && processSignalAlive(process.Signal(syscall.Signal(0)))
}
