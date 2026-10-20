//go:build unix

package agentexec

import (
	"os/exec"
	"syscall"
)

func configureProbeProcess(command *exec.Cmd) func() {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	return func() { _ = command.Cancel() }
}
