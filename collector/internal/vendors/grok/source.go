package grok

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Root is $GROK_HOME/sessions, else ~/.grok/sessions.
func Root() (string, error) {
	if home := os.Getenv("GROK_HOME"); home != "" {
		return filepath.Join(home, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".grok", "sessions"), nil
}

// scan lists <root>/<encoded-cwd>/<session-id>/summary.json paths.
func scan() (string, *vendors.SourceScan, error) {
	root, err := Root()
	if err != nil {
		return "", nil, err
	}
	result := &vendors.SourceScan{Files: []string{}, Skipped: []vendors.SkippedPath{}}
	groups, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		result.RootMissing = true
		return root, result, nil
	}
	if err != nil {
		return root, nil, err
	}
	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		groupDir := filepath.Join(root, group.Name())
		entries, err := os.ReadDir(groupDir)
		if err != nil {
			result.RecordSkipped(groupDir, err)
			continue
		}
		for _, entry := range entries {
			summary := filepath.Join(groupDir, entry.Name(), "summary.json")
			if info, err := os.Stat(summary); entry.IsDir() && err == nil && info.Mode().IsRegular() {
				result.Files = append(result.Files, summary)
			}
		}
	}
	return root, result, nil
}

func sessionDirs(ctx context.Context) ([]string, error) {
	_, result, err := scan()
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(result.Files))
	for _, path := range result.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dirs = append(dirs, filepath.Dir(path))
	}
	return dirs, nil
}

func modifiedAt(dir string) int64 {
	modified := int64(0)
	for _, name := range []string{"summary.json", "updates.jsonl"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
			modified = max(modified, info.ModTime().UnixMilli())
		}
	}
	return modified
}

func CollectContext(ctx context.Context, since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	dirs, err := sessionDirs(ctx)
	if err != nil {
		return nil, nil, err
	}
	if since > 0 {
		recent := dirs[:0]
		for _, dir := range dirs {
			if modifiedAt(dir) >= since {
				recent = append(recent, dir)
			}
		}
		dirs, _, err = vendors.LimitNewestFileFamiliesContext(ctx, recent, vendors.MaxCandidateFilesPerAgent,
			func(dir string) string { return dir }, modifiedAt)
		if err != nil {
			return nil, nil, err
		}
	}
	parsed, err := vendors.ParseFilesContext(ctx, dirs, func(_ context.Context, dir string) (*vendors.ParsedSession, error) {
		return parseTopLevel(dir)
	})
	if err != nil {
		return nil, nil, err
	}
	return parsed, vendors.EmptySessionMetadata(), nil
}

// parseTopLevel skips child sessions, which belong under their parent.
func parseTopLevel(dir string) (*vendors.ParsedSession, error) {
	summary, err := readSummary(dir)
	if err != nil || isSubagentKind(summary.SessionKind) {
		return nil, err
	}
	return parseSession(dir)
}

func GetSessionFacts(id string) (*vendors.ParsedSession, error) {
	if id == "" {
		return nil, nil
	}
	dirs, err := sessionDirs(context.Background())
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if filepath.Base(dir) == id {
			return parseSession(dir)
		}
	}
	return nil, nil
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	parsed, err := GetSessionFacts(id)
	if err != nil || parsed == nil {
		return nil, vendors.EmptySessionMetadata(), err
	}
	return []*vendors.ParsedSession{parsed}, vendors.EmptySessionMetadata(), nil
}

func Health() vendors.SourceHealth {
	root, result, err := scan()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentGrok, Root: root, Err: err}
	}
	return vendors.FileSourceHealth(vendors.AgentGrok, root, result, func(path string) (bool, error) {
		summary, err := readSummary(filepath.Dir(path))
		if err != nil {
			return false, err
		}
		return summary.ChatFormatVersion == 1 && !isSubagentKind(summary.SessionKind), nil
	})
}
