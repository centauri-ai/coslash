//go:build !windows

package codex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func loadLiveSessionsContext(ctx context.Context) (map[string]struct{}, error) {
	openCodexSessions, err := exec.CommandContext(ctx, "lsof", "-a", "-c", "codex", "-Fn").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) || errors.As(err, &exitErr) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return LiveSessionIDs(string(openCodexSessions)), nil
}

func loadLiveSessionsForFilesContext(ctx context.Context, _ []string) (map[string]struct{}, error) {
	return loadLiveSessionsContext(ctx)
}

func deletePlatformLiveSessions(ctx context.Context, files []string) (map[string]struct{}, error) {
	return deleteUnixLiveSessions(ctx, files)
}

func tryDeleteFileLock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK {
		return ErrSessionActive
	}
	return err
}
