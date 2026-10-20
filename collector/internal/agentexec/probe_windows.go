package agentexec

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureProbeProcess(command *exec.Cmd) func() {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return func() {} // Run closes the Job Object, including remaining descendants.
}
