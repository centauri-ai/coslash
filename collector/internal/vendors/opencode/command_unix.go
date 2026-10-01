//go:build !windows

package opencode

import (
	"os"
	"os/exec"
	"syscall"
)

func runOwnedCommand(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Cancel()
	return cmd.Wait()
}
