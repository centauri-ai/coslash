package claude

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// root: 		~/.claude/projects/<cwd-slug>/<session-uuid>.jsonl
// subagent: 	~/.claude/projects/<cwd-slug>/<uuid>/subagents/agent-<id>.jsonl
// workflow: 	~/.claude/projects/<cwd-slug>/<uuid>/subagents/workflows/<run-id>/agent-<id>.jsonl
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func IDFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

func ParentIDFromPath(path string) string {
	parts := strings.Split(path, string(filepath.Separator))
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i] == "subagents" {
			return parts[i-1]
		}
	}
	return ""
}
func Files() ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	all, err := vendors.JSONLFilesUnder(root)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(all))
	for _, file := range all {
		if strings.Contains(file, "/subagents/workflows/") &&
			!strings.HasPrefix(filepath.Base(file), "agent-") {
			continue
		}
		files = append(files, file)
	}
	return files, nil
}

// FilesSince keeps recent/live roots and every subagent file in those sessions.
func FilesSince(files []string, live map[string]string, since int64) []string {
	selectedRoots := map[string]struct{}{}
	for _, file := range files {
		if ParentIDFromPath(file) != "" {
			continue
		}
		id := IDFromPath(file)
		info, err := os.Stat(file)
		_, isLive := live[id]
		if err != nil || isLive || info.ModTime().UnixMilli() >= since {
			selectedRoots[id] = struct{}{}
		}
	}
	selected := make([]string, 0, len(files))
	for _, file := range files {
		rootID := ParentIDFromPath(file)
		if rootID == "" {
			rootID = IDFromPath(file)
		}
		if _, ok := selectedRoots[rootID]; ok {
			selected = append(selected, file)
		}
	}
	return selected
}
