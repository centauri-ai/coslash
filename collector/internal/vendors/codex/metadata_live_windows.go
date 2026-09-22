//go:build windows

package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/winprocess"
)

const maxFilesPerRestartManagerQuery = 128

var (
	processesUsingRollouts = winprocess.ProcessesUsingFiles
	codexProcess           = isCurrentUserCodexProcess
)

func loadLiveSessionsContext(ctx context.Context) (map[string]struct{}, error) {
	files, err := FilesContext(ctx)
	if err != nil {
		return nil, err
	}
	return loadLiveSessionsFromFiles(ctx, files)
}

func loadLiveSessionsForFilesContext(ctx context.Context, files []string) (map[string]struct{}, error) {
	return loadLiveSessionsFromFiles(ctx, files)
}

func loadLiveSessionsFromFiles(ctx context.Context, files []string) (map[string]struct{}, error) {
	live := map[string]struct{}{}
	for start := 0; start < len(files); start += maxFilesPerRestartManagerQuery {
		end := min(start+maxFilesPerRestartManagerQuery, len(files))
		if err := findOpenRollouts(ctx, files[start:end], live); err != nil {
			return nil, err
		}
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

func isCurrentUserCodexProcess(pid uint32) bool {
	executable, err := winprocess.CurrentUserExecutable(pid)
	return err == nil && strings.EqualFold(filepath.Base(executable), "codex.exe")
}
