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
	return ProjectsRoot(home), nil
}

func ProjectsRoot(home string) string {
	return filepath.Join(home, ".claude", "projects")
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
	return FilesSource(vendors.LocalReadSource, root)
}

func FilesSource(source vendors.ReadSource, root string) ([]string, error) {
	files, err := vendors.JSONLFilesUnderSource(source, root)
	if err != nil {
		return nil, err
	}
	return filterWorkflowTranscripts(files), nil
}

func Scan() (*vendors.SourceScan, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return ScanSource(vendors.LocalReadSource, root)
}

func ScanSource(source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	scan, err := vendors.ScanSource(source, root)
	if err != nil {
		return nil, err
	}
	scan.Files = filterWorkflowTranscripts(scan.Files)
	return scan, nil
}

func filterWorkflowTranscripts(all []string) []string {
	files := make([]string, 0, len(all))
	workflowSegment := string(filepath.Separator) + filepath.Join("subagents", "workflows") + string(filepath.Separator)
	for _, file := range all {
		if strings.Contains(file, workflowSegment) && !strings.HasPrefix(filepath.Base(file), "agent-") {
			continue
		}
		files = append(files, file)
	}
	return files
}

// FilesSince keeps recent/live roots and every subagent file in those sessions.
func FilesSince(files []string, live map[string]string, since int64) []string {
	return FilesSinceSource(vendors.LocalReadSource, files, live, since)
}

func FilesSinceSource(
	source vendors.ReadSource,
	files []string,
	live map[string]string,
	since int64,
) []string {
	selectedRoots := map[string]struct{}{}
	for _, file := range files {
		if ParentIDFromPath(file) != "" {
			continue
		}
		id := IDFromPath(file)
		info, err := source.Stat(file)
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
