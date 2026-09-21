package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"golang.org/x/sys/windows"
)

func withDestinationCoordinator(ctx context.Context, _ string, callback func() error) error {
	if err := ensureSSHControlDir(); err != nil {
		return err
	}
	lockPath := filepath.Join(settings.Home(), "ssh", ".auth-coordinator.lock")
	lock, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	overlapped := &windows.Overlapped{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		err := windows.LockFileEx(
			windows.Handle(lock.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			overlapped,
		)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return fmt.Errorf("lock SSH destination: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer func() {
		_ = windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, overlapped)
	}()
	return callback()
}
