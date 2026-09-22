//go:build !windows

package launch

import "github.com/centauri-ai/coslash/collector/internal/settings"

func sshAuthenticationCommand(executable, attemptID string) string {
	return localCommandJoin("env", "COSLASH_HOME="+settings.Home(), executable, "ssh-auth", attemptID)
}
