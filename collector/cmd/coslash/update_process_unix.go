//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func startDetachedUpdateProcess(executable string, args ...string) error {
	command := exec.Command(executable, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	command.Stdin, command.Stdout, command.Stderr = null, null, null
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func replaceUpdateTarget(target, staged, backup string) error {
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(target, backup); err != nil {
		return err
	}
	if err := syncUpdateDirectory(filepath.Dir(target)); err != nil {
		_ = os.Remove(backup)
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Remove(backup)
		return err
	}
	return syncUpdateDirectory(filepath.Dir(target))
}

func restoreUpdateTarget(target, backup string) error {
	if err := os.Rename(backup, target); err != nil {
		return err
	}
	return syncUpdateDirectory(filepath.Dir(target))
}

func syncUpdateDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func scheduleUpdateHelperCleanup(helper string) error {
	return os.Remove(helper)
}

func reexecBackground(executable string) error {
	return syscall.Exec(executable, []string{executable, "--background"}, os.Environ())
}
