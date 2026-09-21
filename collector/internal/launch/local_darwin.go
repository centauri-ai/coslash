package launch

import (
	"context"
	"fmt"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func openTerminal(ctx context.Context, terminal, workingDirectory, command string) error {
	var label, application string
	var open func(context.Context, string, string) error
	switch terminal {
	case settings.TerminalApple:
		label, application, open = "Apple Terminal", "Terminal", openMacTerminal
	case settings.TerminalITerm:
		label, application, open = "iTerm2", "iTerm2", openMacITerm
	default:
		return fmt.Errorf("launch: unsupported terminal %q", terminal)
	}
	if err := macApplicationAvailable(ctx, application); err != nil {
		return fmt.Errorf("launch: %s is not installed or available; choose another terminal in Settings", label)
	}
	if err := open(workingDirectory, command); err != nil {
		return fmt.Errorf("launch: open %s: %w", label, err)
	}
	return nil
}

func openTerminalForAgent(ctx context.Context, terminal, _ string, workingDirectory, command string) error {
	return openTerminal(ctx, terminal, workingDirectory, command)
}

func Available(terminal string) bool {
	switch terminal {
	case settings.TerminalApple:
		return macApplicationAvailable(context.Background(), "Terminal") == nil
	case settings.TerminalITerm:
		return macApplicationAvailable(context.Background(), "iTerm2") == nil
	default:
		return false
	}
}
