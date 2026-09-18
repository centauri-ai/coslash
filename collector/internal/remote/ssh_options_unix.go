//go:build !windows

package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	return []string{"-O", "exit", "-o", "ControlPath=" + controlSocketPath(), alias}, nil
}

func controlCheckArgs(alias string) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	return []string{"-O", "check", "-o", "ControlPath=" + controlSocketPath(), alias}, nil
}

func controlMasterStartArgs(alias string, connectTimeoutSeconds int) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	// -f -N leaves a background master so later SFTP clients can die without the tunnel.
	return []string{
		"-f", "-N", "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		"-o", "ControlMaster=yes", "-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist, alias,
	}, nil
}

func ensureControlMaster(ctx context.Context, alias string, options OpenOptions) error {
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
