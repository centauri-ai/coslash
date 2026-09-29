//go:build !windows

package agentexec

import (
	"context"
	"os"
	"os/exec"
)

func CommandContext(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = WithoutSessionMarkers(os.Environ())
	return cmd
}

func Run(cmd *exec.Cmd) error { return cmd.Run() }

func Output(cmd *exec.Cmd) ([]byte, error) { return cmd.Output() }
