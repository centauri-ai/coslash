package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	"github.com/centauri-ai/coslash/collector/internal/vendors/cursor"
)

const activePollInterval = 20 * time.Second
const activePollWindow = 30 * time.Minute

func runActivePoller(ctx context.Context, home string, control *syncLoopControl) {
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return
		}
	}
	previous, _ := activeSnapshot(ctx, home, time.Now())
	ticker := time.NewTicker(activePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			current, err := activeSnapshot(ctx, home, now)
			if err == nil {
				changed := activeSnapshotChanged(previous, current, now)
				if !changed || signalActiveChange(control, true) {
					previous = current
				}
			}
		}
	}
}

func signalActiveChange(control *syncLoopControl, changed bool) bool {
	return changed && control.wakeIfIdle()
}

// activeSnapshot uses only directory reads and stat calls. It keeps metadata
// for sessions modified in the last 30 minutes and the agent root mtimes.
func activeSnapshot(ctx context.Context, home string, now time.Time) (map[string]string, error) {
	roots := []string{
		codex.SessionsRoot(home),
		codex.ArchivedDir(home),
		claude.ProjectsRoot(home),
		cursor.ProjectsRoot(home),
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	openCodeDB := inventory.OpenCodeDatabasePath(home)
	roots = append(roots, filepath.Join(dataHome, "opencode"), openCodeDB, openCodeDB+"-wal")
	snapshot := make(map[string]string)
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			snapshot["root:"+root] = "missing"
			continue
		}
		snapshot["root:"+root] = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	}
	inventorySnapshot, err := inventory.Scan(ctx, inventory.Options{Home: home, OpenCodeDB: openCodeDB, Now: now})
	if err != nil {
		return nil, err
	}
	cutoff := now.Add(-activePollWindow).UnixMilli()
	for _, file := range inventorySnapshot.Files {
		if file.ModTimeMs >= cutoff {
			snapshot["session:"+file.Agent+"\x00"+file.Path] = fmt.Sprintf("%d:%d", file.ModTimeMs, file.Size)
		}
	}
	return snapshot, nil
}

func activeSnapshotChanged(previous, current map[string]string, now time.Time) bool {
	cutoff := now.Add(-activePollWindow).UnixMilli()
	for key, value := range current {
		if old, ok := previous[key]; !ok || old != value {
			return true
		}
	}
	for key := range previous {
		if _, ok := current[key]; ok {
			continue
		}
		if strings.HasPrefix(key, "session:") {
			parts := strings.SplitN(previous[key], ":", 2)
			if len(parts) == 2 {
				modifiedAt, err := strconv.ParseInt(parts[0], 10, 64)
				if err == nil && modifiedAt >= cutoff {
					return true
				}
			}
		}
		// Root directory changes, including creation or removal, are active
		// changes even if no session file has appeared yet.
		if strings.HasPrefix(key, "root:") {
			return true
		}
	}
	return false
}
