//go:build !windows

package cursor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// cursorAgentStoresRoot matches the IDE agent store under both the macOS
// Application Support and the Linux XDG config layouts.
const cursorAgentStoresRoot = "/Cursor/AgentStores/cursor_agent_stores/"

func loadLiveSessions(ctx context.Context) map[string]string {
	live := map[string]string{}
	if output, err := exec.CommandContext(ctx, "lsof", "-a", "-c", cursorIDEProcess, "-Fn").Output(); err == nil {
		ids, _ := liveIDsFromLSOF(ctx, string(output), cursorAgentStoresRoot, ".sync", "index.sqlite")
		for id := range ids {
			live[id] = entrypointIDE
		}
	}
	if output, err := exec.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,command=").Output(); err == nil {
		pids, _ := cursorAgentPIDs(ctx, string(output))
		if len(pids) > 0 {
			if output, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strings.Join(pids, ","), "-Fn").Output(); err == nil {
				ids, _ := liveIDsFromLSOF(ctx, string(output), "/.cursor/chats/", "", "store.db")
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

func liveIDsFromLSOF(ctx context.Context, output, root, child, database string) (map[string]bool, error) {
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

func cursorAgentPIDs(ctx context.Context, output string) ([]string, error) {
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
