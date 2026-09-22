//go:build windows

package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/winprocess"
)

const maxWindowsLiveRolloutCandidates = 256

var (
	processesUsingRollouts = winprocess.ProcessesUsingFiles
	codexProcess           = isCurrentUserCodexProcess
)

func loadLiveSessionsContext(ctx context.Context) (map[string]struct{}, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	return loadLiveSessionsFromFiles(ctx, files)
}

func loadLiveSessionsFromFiles(ctx context.Context, files []string) (map[string]struct{}, error) {
	live := map[string]struct{}{}
	candidates := newestRolloutCandidates(files, maxWindowsLiveRolloutCandidates)
	if err := findOpenRollouts(ctx, candidates, live); err != nil {
		return nil, err
	}
	return live, nil
}

func findOpenRollouts(ctx context.Context, files []string, live map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	pids, err := processesUsingRollouts(files)
	if err != nil {
		return fmt.Errorf("rollout group: %w", err)
	}
	hasCodex := false
	for _, pid := range pids {
		if codexProcess(pid) {
			hasCodex = true
			break
		}
	}
	if !hasCodex {
		return nil
	}
	if len(files) == 1 {
		if id := SessionIDFromRollout(files[0]); id != "" {
			live[id] = struct{}{}
		}
		return nil
	}
	middle := len(files) / 2
	if err := findOpenRollouts(ctx, files[:middle], live); err != nil {
		return err
	}
	return findOpenRollouts(ctx, files[middle:], live)
}

func newestRolloutCandidates(files []string, limit int) []string {
	if len(files) <= limit {
		return slices.Clone(files)
	}
	type candidate struct {
		path       string
		modifiedAt int64
	}
	candidates := make([]candidate, len(files))
	for index, path := range files {
		candidates[index].path = path
		if info, err := os.Stat(path); err == nil {
			candidates[index].modifiedAt = info.ModTime().UnixNano()
		}
	}
	slices.SortFunc(candidates, func(left, right candidate) int {
		if left.modifiedAt > right.modifiedAt {
			return -1
		}
		if left.modifiedAt < right.modifiedAt {
			return 1
		}
		return strings.Compare(left.path, right.path)
	})
	result := make([]string, limit)
	for index := range result {
		result[index] = candidates[index].path
	}
	return result
}

func isCurrentUserCodexProcess(pid uint32) bool {
	executable, err := winprocess.CurrentUserExecutable(pid)
	return err == nil && strings.EqualFold(filepath.Base(executable), "codex.exe")
}
