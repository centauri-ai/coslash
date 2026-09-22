//go:build !windows

package diagnostics

import (
	"context"
	"os/exec"
)

func versionCommand(ctx context.Context, bin string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, "--version")
}
