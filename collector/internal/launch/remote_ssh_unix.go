//go:build !windows

package launch

import "github.com/centauri-ai/coslash/collector/internal/settings"

func remoteSSHArgs(destination settings.SSHDestination, command string) []string {
	args := []string{"ssh", "-tt", "-o", "ControlMaster=auto", "-o", "ControlPath=" + settings.SSHControlPath()}
	args = append(args, destination.Args()...)
	return append(args, command)
}
