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
func Files() ([]string, error) {
	return FilesContext(context.Background())
}

func FilesContext(ctx context.Context) ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return FilesSourceContext(ctx, vendors.LocalReadSource, root)
}

func FilesSource(source vendors.ReadSource, root string) ([]string, error) {
	return FilesSourceContext(context.Background(), source, root)
}

func FilesSourceContext(ctx context.Context, source vendors.ReadSource, root string) ([]string, error) {
	files, err := vendors.JSONLFilesUnderSourceContext(ctx, source, root)
	if err != nil {
		return nil, err
	}
	return filterWorkflowTranscripts(files), nil
}

func Scan() (*vendors.SourceScan, error) {
	return ScanContext(context.Background())
}

func ScanContext(ctx context.Context) (*vendors.SourceScan, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return ScanSourceContext(ctx, vendors.LocalReadSource, root)
}

func ScanSource(source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	return ScanSourceContext(context.Background(), source, root)
}

func ScanSourceContext(ctx context.Context, source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	scan, err := vendors.ScanSourceContext(ctx, source, root)
	if err != nil {
		return nil, err
	}
	scan.Files = filterWorkflowTranscripts(scan.Files)
	return scan, nil
}

func filterWorkflowTranscripts(all []string) []string {
	files := make([]string, 0, len(all))
	workflowSegment := "/subagents/workflows/"
	for _, file := range all {
		normalized := filepath.ToSlash(file)
		if strings.Contains(normalized, workflowSegment) && !strings.HasPrefix(filepath.Base(normalized), "agent-") {
			continue
		}
		files = append(files, file)
	}
	return files
}

// FilesSince keeps recent/live roots, their subagents, and any older family
// files that a selected background session re-homed.
func FilesSince(files []string, live map[string]string, since int64) []string {
	selected, _ := FilesSinceContext(context.Background(), files, live, since)
	return selected
}

func FilesSinceContext(ctx context.Context, files []string, live map[string]string, since int64) ([]string, error) {
	selected, err := FilesSinceSourceContext(ctx, vendors.LocalReadSource, files, live, since)
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
		family, err := familyFilesContext(ctx, vendors.LocalReadSource, files, IDFromPath(file))
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

func FilesSinceSource(
	source vendors.ReadSource,
	files []string,
	live map[string]string,
	since int64,
) []string {
	files, _ = FilesSinceSourceContext(context.Background(), source, files, live, since)
	return files
}

func FilesSinceSourceContext(
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
