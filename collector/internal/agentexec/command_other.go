//go:build !windows

package agentexec

import (
	"context"
	"os/exec"
)

func CommandContext(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}

func Run(cmd *exec.Cmd) error { return cmd.Run() }

func Output(cmd *exec.Cmd) ([]byte, error) { return cmd.Output() }
