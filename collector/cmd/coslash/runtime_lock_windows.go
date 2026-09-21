package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockRuntimeFileExclusive(file *os.File, nonBlocking bool) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonBlocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	return windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
}

func runtimeFileExclusivelyLocked(file *os.File) bool {
	overlapped := &windows.Overlapped{}
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err != nil {
		return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
	}
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
	return false
}
