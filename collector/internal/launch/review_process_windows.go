package launch

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureReviewProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
