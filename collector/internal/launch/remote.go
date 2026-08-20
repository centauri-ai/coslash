package launch

import (
	"fmt"
	"os"
	"runtime"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const remoteCollectorExec = `exec "$HOME/.local/bin/coslash"`

// HandoffPutRemoteCommand returns the fixed remote handoff-put command.
func HandoffPutRemoteCommand(agent, sessionID string) (string, error) {
	if err := ValidateRemoteAgent(agent); err != nil {
		return "", err
	}
	if err := ValidateUUIDSessionID(sessionID); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`%s handoff put --agent %s --session %s`,
		remoteCollectorExec,
		agent,
		sessionID,
	), nil
}

// LaunchResumeRemoteCommand returns the fixed remote resume command.
func LaunchResumeRemoteCommand(agent, sessionID string) (string, error) {
	if err := ValidateRemoteAgent(agent); err != nil {
		return "", err
	}
	if err := ValidateUUIDSessionID(sessionID); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`%s launch --agent %s --session %s --mode resume`,
		remoteCollectorExec,
		agent,
		sessionID,
	), nil
}

// LaunchNewRemoteCommand returns the fixed remote new-session command.
func LaunchNewRemoteCommand(agent, sessionID, handoffID string) (string, error) {
	if err := ValidateRemoteAgent(agent); err != nil {
		return "", err
	}
	if err := ValidateUUIDSessionID(sessionID); err != nil {
		return "", err
	}
	if err := ValidateHandoffID(handoffID); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`%s launch --agent %s --session %s --mode new --handoff %s`,
		remoteCollectorExec,
		agent,
		sessionID,
		handoffID,
	), nil
}

// InteractiveSSHCommand joins a validated alias with a prebuilt remote command.
func InteractiveSSHCommand(alias, remoteCommand string) (string, error) {
	if err := ValidateSSHAlias(alias); err != nil {
		return "", err
	}
	if remoteCommand == "" {
		return "", fmt.Errorf("%w: empty remote command", ErrInvalidInput)
	}
	return "ssh -t -- " + alias + " " + shellQuote(remoteCommand), nil
}

// RemoteTerminal opens the configured Mac terminal with an interactive SSH launch command.
func RemoteTerminal(terminal, alias, remoteCommand string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("%w: opening a terminal is not supported on %s", ErrUnsupportedOnHost, runtime.GOOS)
	}
	adapter, err := terminalFor(terminal)
	if err != nil {
		return err
	}
	if err := adapter.available(); err != nil {
		return fmt.Errorf("%w: %s is not installed or available; choose another terminal in Settings", ErrTerminalOpen, adapter.label)
	}
	command, err := InteractiveSSHCommand(alias, remoteCommand)
	if err != nil {
		return err
	}
	cwd, err := os.UserHomeDir()
	if err != nil || cwd == "" {
		cwd = settings.Home()
	}
	if err := adapter.open(cwd, command); err != nil {
		return fmt.Errorf("%w: open %s: %v", ErrTerminalOpen, adapter.label, err)
	}
	return nil
}
