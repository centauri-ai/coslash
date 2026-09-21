//go:build !windows

package cursor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// loadLiveSessions returns transcript IDs backed by Cursor processes. The IDE
// and CLI use different stores, so probe them independently and preserve the
// lane only when the same ID is not simultaneously reported by both.
func loadLiveSessions() map[string]string {
	return loadLiveSessionsContext(context.Background())
}

func loadLiveSessionsContext(ctx context.Context) map[string]string {
	live := map[string]string{}
	if output, err := exec.CommandContext(ctx, "lsof", "-a", "-c", "Cursor", "-Fn").Output(); err == nil {
		ids, _ := liveIDsFromLSOFContext(ctx, string(output), "/Library/Application Support/Cursor/AgentStores/cursor_agent_stores/", ".sync", "index.sqlite")
		for id := range ids {
			live[id] = entrypointIDE
		}
	}
	if output, err := exec.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,command=").Output(); err == nil {
		pids, _ := cursorAgentPIDsContext(ctx, string(output))
		if len(pids) > 0 {
			if output, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strings.Join(pids, ","), "-Fn").Output(); err == nil {
				ids, _ := liveIDsFromLSOFContext(ctx, string(output), "/.cursor/chats/", "", "store.db")
				for id := range ids {
					if lane, exists := live[id]; !exists || lane == entrypointCLI {
						live[id] = entrypointCLI
					} else {
						live[id] = ""
					}
				}
			}
		}
	}
	return live
}

func liveIDEFromLSOF(output string) map[string]bool {
	return liveIDsFromLSOF(output, "/Library/Application Support/Cursor/AgentStores/cursor_agent_stores/", ".sync", "index.sqlite")
}

func liveCLIFromLSOF(output string) map[string]bool {
	return liveIDsFromLSOF(output, "/.cursor/chats/", "", "store.db")
}

func liveIDsFromLSOF(output, root, child, database string) map[string]bool {
	live, _ := liveIDsFromLSOFContext(context.Background(), output, root, child, database)
	return live
}

func liveIDsFromLSOFContext(ctx context.Context, output, root, child, database string) (map[string]bool, error) {
	live := map[string]bool{}
	for line := range strings.SplitSeq(output, "\n") {
		if err := ctx.Err(); err != nil {
			return live, err
		}
		if !strings.HasPrefix(line, "n") {
			continue
		}
		path := filepath.ToSlash(line[1:])
		_, suffix, ok := strings.Cut(path, root)
		if !ok {
			continue
		}
		parts := strings.Split(suffix, "/")
		idIndex := 0
		if child == "" {
			idIndex = 1
		}
		if len(parts) <= idIndex+1 || !transcriptIDPattern.MatchString(parts[idIndex]) {
			continue
		}
		if child != "" && parts[idIndex+1] != child {
			continue
		}
		name := parts[len(parts)-1]
		if name == database || name == database+"-wal" || name == database+"-shm" {
			live[canonicalCursorID(parts[idIndex])] = true
		}
	}
	return live, ctx.Err()
}

func cursorAgentPIDs(output string) []string {
	pids, _ := cursorAgentPIDsContext(context.Background(), output)
	return pids
}

func cursorAgentPIDsContext(ctx context.Context, output string) ([]string, error) {
	var pids []string
	for line := range strings.SplitSeq(output, "\n") {
		if err := ctx.Err(); err != nil {
			return pids, err
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		for _, argument := range fields[1:] {
			if strings.Contains(filepath.ToSlash(argument), "/cursor-agent/versions/") && strings.HasSuffix(argument, "/index.js") {
				pids = append(pids, fields[0])
				break
			}
		}
	}
	return pids, ctx.Err()
}
