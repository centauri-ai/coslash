//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func startBackgroundProcess() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "--background")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008}
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func ignoreBackgroundHangup() {}
