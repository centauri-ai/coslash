package cursor

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func loadLiveSessions() map[string]string {
	live := map[string]string{}
	if output, err := exec.Command("lsof", "-a", "-c", "Cursor", "-Fn").Output(); err == nil {
		for id := range liveIDEFromLSOF(string(output)) {
			live[id] = entrypointIDE
		}
	}
	if output, err := exec.Command("ps", "-ww", "-axo", "pid=,command=").Output(); err == nil {
		if pids := cursorAgentPIDs(string(output)); len(pids) > 0 {
			if output, err := exec.Command("lsof", "-a", "-p", strings.Join(pids, ","), "-Fn").Output(); err == nil {
				for id := range liveCLIFromLSOF(string(output)) {
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
	live := map[string]bool{}
	for line := range strings.SplitSeq(output, "\n") {
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
			live[parts[idIndex]] = true
		}
	}
	return live
}

func cursorAgentPIDs(output string) []string {
	var pids []string
	for line := range strings.SplitSeq(output, "\n") {
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
	return pids
}
