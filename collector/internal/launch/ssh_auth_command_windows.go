package launch

import "github.com/centauri-ai/coslash/collector/internal/settings"

func sshAuthenticationCommand(executable, attemptID string) string {
	return "$env:COSLASH_HOME = " + powerShellQuote(settings.Home()) + "; " + localCommandJoin(executable, "ssh-auth", attemptID)
}
