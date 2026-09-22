//go:build !windows

package codex

import (
	"context"
	"errors"
	"os/exec"
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
