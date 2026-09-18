//go:build !windows

package launch

import "github.com/centauri-ai/coslash/collector/internal/settings"

func remoteSSHArgs(alias, command string) []string {
	return []string{"ssh", "-tt", "-o", "ControlMaster=auto", "-o", "ControlPath=" + settings.SSHControlPath(), alias, command}
}
