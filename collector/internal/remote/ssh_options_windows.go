package remote

import (
	"context"
	"strconv"
)

func sshOptions(connectTimeoutSeconds int) []string {
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	return []string{"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds)}
}

func ControlExitArgs(alias string) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	return nil, nil
}

func ensureControlMaster(context.Context, string, OpenOptions) error { return nil }

func ensureSSHControlDir() error { return nil }

func ExitControlMaster(alias string, _ OpenOptions) error {
	_, err := ControlExitArgs(alias)
	return err
}
