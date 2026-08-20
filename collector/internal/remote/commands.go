package remote

import (
	"fmt"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const remoteCollectorExec = `exec "$HOME/.local/bin/coslash"`

// SnapshotCommand returns the fixed remote snapshot grammar for validated epochs.
func SnapshotCommand(sinceMs, requestNowMs int64) (string, error) {
	if sinceMs < 0 || requestNowMs < 0 {
		return "", fmt.Errorf("snapshot epochs must be non-negative")
	}
	if sinceMs > requestNowMs {
		return "", fmt.Errorf("since must not exceed request-now")
	}
	return fmt.Sprintf(
		`%s snapshot --since %d --request-now %d --agents claude,codex`,
		remoteCollectorExec,
		sinceMs,
		requestNowMs,
	), nil
}

// ProbeCommand returns the fixed remote probe grammar.
func ProbeCommand() string {
	return remoteCollectorExec + ` snapshot --probe`
}

// SSHArgv builds local OpenSSH argv with BatchMode and ConnectTimeout fixed.
func SSHArgv(alias, remoteCommand string) ([]string, error) {
	if !settings.ValidSSHAlias(alias) {
		return nil, fmt.Errorf("invalid SSH alias %q", alias)
	}
	if remoteCommand == "" {
		return nil, fmt.Errorf("remote command is required")
	}
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"--",
		alias,
		remoteCommand,
	}, nil
}
