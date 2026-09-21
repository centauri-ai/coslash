//go:build !windows

package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

// withDestinationCoordinator serializes an interactive attempt with every
// control-master check/start. The single lock avoids leaving a lock file for
// every attempted destination. flock is released if the process exits.
func withDestinationCoordinator(ctx context.Context, _ string, callback func() error) error {
	if err := ensureSSHControlDir(); err != nil {
		return err
	}
	lockPath := filepath.Join(settings.Home(), "ssh", ".auth-coordinator.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("lock SSH destination: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return callback()
}
