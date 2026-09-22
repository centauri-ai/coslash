package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func sshOptions(connectTimeoutSeconds int) []string {
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	return []string{"-T", "-o", "ControlMaster=no", "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds)}
}

func ControlExitArgs(alias string) ([]string, error) {
	if _, err := parseDestination(alias); err != nil {
		return nil, err
	}
	return nil, nil
}

func controlCheckArgs(alias string) ([]string, error) {
	args, err := sshArgs(alias, 3)
	if err != nil {
		return nil, err
	}
	return append(args, "true"), nil
}

func ensureControlMaster(ctx context.Context, alias string, _ OpenOptions) error {
	if _, err := parseDestination(alias); err != nil {
		return err
	}
	return withDestinationCoordinator(ctx, alias, func() error {
		if AuthAttemptActive(alias) {
			return ErrAuthAttemptActive
		}
		return nil
	})
}

func ensureSSHControlDir() error {
	if err := os.MkdirAll(filepath.Join(settings.Home(), "ssh"), 0o700); err != nil {
		return fmt.Errorf("create SSH state directory: %w", err)
	}
	return nil
}

func ExitControlMaster(alias string, _ OpenOptions) error {
	_, err := ControlExitArgs(alias)
	return err
}

var checkAuthControlMaster = func(ctx context.Context, destination string) error {
	args, err := controlCheckArgs(destination)
	if err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return runSSHCommand(checkCtx, OpenOptions{}, args)
}

func authAttemptConnectionReady(_ context.Context, destination string) (bool, error) {
	_, err := parseDestination(destination)
	return false, err
}

func prepareAuthControlSocket(context.Context, string) (bool, error) { return false, nil }

func interactiveMasterArgs(destination string) ([]string, error) {
	parsed, err := parseDestination(destination)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-T",
		"-o", "ControlMaster=no",
		"-o", "BatchMode=no",
		"-o", "AddKeysToAgent=yes",
		"-o", "ConnectTimeout=" + fmt.Sprint(int(DefaultConnectTimeout.Seconds())),
	}
	args = append(args, parsed.Args()...)
	return append(args, "true"), nil
}
