package claude

import (
	"context"
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
	return strings.TrimSuffix(filepath.Base(filepath.ToSlash(path)), ".jsonl")
}

func ParentIDFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i] == "subagents" {
			return parts[i-1]
		}
	}
	return ""
}

func Files(ctx context.Context, source vendors.ReadSource, root string) ([]string, error) {
	files, err := vendors.JSONLFilesUnderSource(ctx, source, root)
	if err != nil {
		return nil, err
	}
	return filterWorkflowTranscripts(files), nil
}

func Scan(ctx context.Context, source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	scan, err := vendors.ScanSource(ctx, source, root)
	if err != nil {
		return nil, err
	}
	scan.Files = filterWorkflowTranscripts(scan.Files)
	return scan, nil
}

func filterWorkflowTranscripts(all []string) []string {
	files := make([]string, 0, len(all))
	for _, file := range all {
		if AcceptsTranscript(file) {
			files = append(files, file)
		}
	}
	return files
}

// AcceptsTranscript reports whether discovery would parse the file: a .jsonl
// under the projects root that is not a workflow journal or run record.
func AcceptsTranscript(file string) bool {
	normalized := filepath.ToSlash(file)
	if !strings.HasSuffix(normalized, ".jsonl") {
		return false
	}
	return !strings.Contains(normalized, "/subagents/workflows/") || strings.HasPrefix(filepath.Base(normalized), "agent-")
}

// familyFilesSince selects like FilesSince and adds every file of each selected
// local family.
func familyFilesSince(ctx context.Context, files []string, live map[string]string, since int64) ([]string, error) {
	selected, err := FilesSince(ctx, vendors.LocalReadSource, files, live, since)
	if err != nil {
		return nil, err
	}
	include := make(map[string]struct{}, len(selected))
	for _, file := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		include[file] = struct{}{}
		if ParentIDFromPath(file) != "" {
			continue
		}
		family, err := familyFiles(ctx, vendors.LocalReadSource, files, IDFromPath(file))
		if err != nil {
			return nil, err
		}
		for _, familyFile := range family {
			include[familyFile] = struct{}{}
		}
	}
	selected = selected[:0]
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := include[file]; ok {
			selected = append(selected, file)
		}
	}
	return selected, nil
}

func FilesSince(
	ctx context.Context,
	source vendors.ReadSource,
	files []string,
	live map[string]string,
	since int64,
) ([]string, error) {
	selectedRoots := map[string]struct{}{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rootID := ParentIDFromPath(file)
		if rootID == "" {
			rootID = IDFromPath(file)
		}
		if _, ok := selectedRoots[rootID]; ok {
			selected = append(selected, file)
		}
	}
	return selected, nil
}
