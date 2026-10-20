//go:build windows

package backgroundexec

import (
	"context"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	// Detached Local has no console for child processes to inherit.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}
