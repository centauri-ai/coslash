//go:build !darwin && !windows

package launch

import (
	"context"
)

func openTerminal(context.Context, string, string, string) error {
	return ErrTerminalUnsupported
}

func openTerminalForAgent(ctx context.Context, terminal, _ string, workingDirectory, command string) error {
	return openTerminal(ctx, terminal, workingDirectory, command)
}

func Available(context.Context, string) bool {
	return false
}
