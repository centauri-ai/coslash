//go:build !windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockRuntimeFileExclusive(file *os.File, nonBlocking bool) error {
	flags := unix.LOCK_EX
	if nonBlocking {
		flags |= unix.LOCK_NB
	}
	return unix.Flock(int(file.Fd()), flags)
}

func runtimeFileExclusivelyLocked(file *os.File) bool {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		return errors.Is(err, unix.EWOULDBLOCK)
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return false
}
