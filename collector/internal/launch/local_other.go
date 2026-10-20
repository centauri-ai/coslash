//go:build !darwin && !windows

package launch

import (
	"context"
	"fmt"
	"runtime"
)

func openTerminal(context.Context, string, string, string) error {
	return fmt.Errorf("launch: opening a terminal is not supported on %s", runtime.GOOS)
}

func openTerminalForAgent(ctx context.Context, terminal, _ string, workingDirectory, command string) error {
	return openTerminal(ctx, terminal, workingDirectory, command)
}

func Available(terminal string) bool { return AvailableContext(context.Background(), terminal) }

func AvailableContext(context.Context, string) bool {
	return false
}
