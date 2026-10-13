//go:build !windows

package launch

import "github.com/centauri-ai/coslash/collector/internal/settings"

func remoteSSHArgs(destination settings.SSHDestination, command string) ([]string, error) {
	controlPath, err := settings.SSHControlPath()
	if err != nil {
		return nil, err
	}
	args := []string{"ssh", "-tt", "-o", "ControlMaster=auto", "-o", "ControlPath=" + controlPath}
	args = append(args, destination.Args()...)
	return append(args, command), nil
}

func remoteReviewSSHArgs(destination settings.SSHDestination, command string) ([]string, error) {
	controlPath, err := settings.SSHControlPath()
	if err != nil {
		return nil, err
	}
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ControlMaster=auto", "-o", "ControlPath=" + controlPath}
	args = append(args, destination.Args()...)
	return append(args, command), nil
}
