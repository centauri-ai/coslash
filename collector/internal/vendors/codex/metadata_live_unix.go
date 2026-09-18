//go:build !windows

package codex

import (
	"errors"
	"os/exec"
	"strings"
)

func loadLiveSessions() (map[string]struct{}, error) {
	openCodexSessions, err := exec.Command("lsof", "-a", "-c", "codex", "-Fn").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) || errors.As(err, &exitErr) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	live := map[string]struct{}{}
	for line := range strings.SplitSeq(string(openCodexSessions), "\n") {
		if !strings.HasPrefix(line, "n") || !strings.HasSuffix(line, ".jsonl") {
			continue
		}
		if id := SessionIDFromRollout(line[1:]); id != "" {
			live[id] = struct{}{}
		}
	}
	return live, nil
}
