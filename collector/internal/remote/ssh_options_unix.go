//go:build !windows

package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const defaultControlPersist = "10m"

func sshOptions(connectTimeoutSeconds int) []string {
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	return []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		"-o", "ControlMaster=auto", "-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
	}
}

func controlSocketPath() string { return settings.SSHControlPath() }

func ensureSSHControlDir() error {
	dir := filepath.Join(settings.Home(), "ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create SSH control directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect SSH control directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("SSH control directory is not a real directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure SSH control directory: %w", err)
	}
	return nil
}

func ControlExitArgs(alias string) ([]string, error) {
	destination, err := parseDestination(alias)
	if err != nil {
		return nil, err
	}
	args := []string{"-O", "exit", "-o", "ControlPath=" + controlSocketPath()}
	return append(args, destination.Args()...), nil
}

func controlCheckArgs(alias string) ([]string, error) {
	destination, err := parseDestination(alias)
	if err != nil {
		return nil, err
	}
	args := []string{"-O", "check", "-o", "ControlPath=" + controlSocketPath()}
	return append(args, destination.Args()...), nil
}

func controlMasterStartArgs(alias string, connectTimeoutSeconds int) ([]string, error) {
	destination, err := parseDestination(alias)
	if err != nil {
		return nil, err
	}
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	// -f -N leaves a background master so later SFTP clients can die without the tunnel.
	args := []string{
		"-f", "-N", "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		"-o", "ControlMaster=yes", "-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
	}
	return append(args, destination.Args()...), nil
}

func ensureControlMaster(ctx context.Context, alias string, options OpenOptions) error {
	if _, err := parseDestination(alias); err != nil {
		return err
	}
	return withDestinationCoordinator(ctx, alias, func() error {
		if AuthAttemptActive(alias) {
			return ErrAuthAttemptActive
		}
		if err := ensureSSHControlDir(); err != nil {
			return err
		}
		checkArgs, err := controlCheckArgs(alias)
		if err != nil {
			return err
		}
		limits := options.Limits.withDefaults()
		checkCtx, cancelCheck := context.WithTimeout(ctx, limits.ConnectTimeout)
		err = runSSHCommand(checkCtx, options, checkArgs)
		cancelCheck()
		if err == nil {
			return nil
		} else if errors.Is(err, ErrStderrLimit) || ctx.Err() != nil {
			return err
		}
		startArgs, err := controlMasterStartArgs(alias, int(limits.ConnectTimeout.Seconds()))
		if err != nil {
			return err
		}
		startCtx, cancel := context.WithTimeout(ctx, limits.ConnectTimeout)
		defer cancel()
		if err := runSSHCommand(startCtx, options, startArgs); err != nil {
			return fmt.Errorf("start SSH control master: %w", err)
		}
		return nil
	})
}

// ExitControlMaster asks OpenSSH to drop coSlash's multiplexed master for alias.
func ExitControlMaster(alias string, options OpenOptions) error {
	args, err := ControlExitArgs(alias)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runSSHCommand(ctx, options, args); err != nil {
		return fmt.Errorf("exit SSH control master: %w", err)
	}
	return nil
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

func authAttemptConnectionReady(ctx context.Context, destination string) (bool, error) {
	args, err := controlCheckArgs(destination)
	if err != nil {
		return false, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return runSSHCommand(checkCtx, OpenOptions{}, args) == nil, nil
}

var resolveAuthControlSocketPath = func(ctx context.Context, destination string) (string, error) {
	parsed, err := parseDestination(destination)
	if err != nil {
		return "", err
	}
	args := []string{"-G", "-o", "ControlPath=" + controlSocketPath()}
	output, err := exec.CommandContext(ctx, "ssh", append(args, parsed.Args()...)...).Output()
	if err != nil {
		return "", fmt.Errorf("expand SSH control path: %w", err)
	}
	var expanded string
	for _, line := range strings.Split(string(output), "\n") {
		if value, found := strings.CutPrefix(line, "controlpath "); found {
			expanded = strings.TrimSpace(value)
			break
		}
	}
	if expanded == "" {
		return "", errors.New("SSH did not report its control path")
	}
	expanded, err = filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	controlDir, err := filepath.Abs(filepath.Join(settings.Home(), "ssh"))
	if err != nil {
		return "", err
	}
	if filepath.Dir(expanded) != controlDir || !strings.HasPrefix(filepath.Base(expanded), "cm-") {
		return "", errors.New("SSH reported an unexpected control path")
	}
	return expanded, nil
}

func prepareAuthControlSocket(ctx context.Context, destination string) (bool, error) {
	path, err := resolveAuthControlSocketPath(ctx, destination)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return false, errors.New("SSH control path is not a socket")
	}
	checkErr := checkAuthControlMaster(ctx, destination)
	if checkErr == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(checkErr, &exitErr) {
		return false, fmt.Errorf("check SSH control master: %w", checkErr)
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("remove stale SSH control socket: %w", err)
	}
	return false, nil
}

func interactiveMasterArgs(destination string) ([]string, error) {
	parsed, err := parseDestination(destination)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-f", "-N",
		"-o", "BatchMode=no",
		"-o", "ConnectTimeout=" + strconv.Itoa(int(DefaultConnectTimeout.Seconds())),
		"-o", "ControlMaster=yes",
		"-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
	}
	return append(args, parsed.Args()...), nil
}
