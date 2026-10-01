package grok

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/session"
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
func scan(ctx context.Context) (string, *vendors.SourceScan, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
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
		if err := ctx.Err(); err != nil {
			return root, nil, err
		}
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
			if err := ctx.Err(); err != nil {
				return root, nil, err
			}
			summary := filepath.Join(groupDir, entry.Name(), "summary.json")
			if info, err := os.Stat(summary); entry.IsDir() && err == nil && info.Mode().IsRegular() {
				result.Files = append(result.Files, summary)
			}
		}
	}
	return root, result, nil
}

func sessionDirs(ctx context.Context) ([]string, error) {
	_, result, err := scan(ctx)
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
	for _, name := range []string{"summary.json", "updates.jsonl", "signals.json", "usage.json"} {
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
	parsed, err := vendors.ParseFilesContext(ctx, dirs, func(ctx context.Context, dir string) (*vendors.ParsedSession, error) {
		return parseSessionContext(ctx, dir)
	})
	if err != nil {
		return nil, nil, err
	}
	attachSubagents(parsed)
	return parsed, loadMetadata(), nil
}

// loadMetadata marks sessions whose active_sessions.json pid is alive as live.
func loadMetadata() *vendors.SessionMetadata {
	metadata := vendors.EmptySessionMetadata()
	root, err := Root()
	if err != nil {
		return metadata
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(root), "active_sessions.json"))
	if errors.Is(err, fs.ErrNotExist) {
		metadata.LivenessChecked = true
		return metadata
	}
	var active []struct {
		SessionID string `json:"session_id"`
		PID       int    `json:"pid"`
	}
	if err != nil || json.Unmarshal(data, &active) != nil {
		return metadata
	}
	metadata.LivenessChecked = true
	for _, entry := range active {
		if entry.SessionID != "" && session.IsProcessAlive(entry.PID) {
			metadata.Session(entry.SessionID).Live = "interactive"
		}
	}
	return metadata
}

func sessionDirsByID() (map[string]string, error) {
	dirs, err := sessionDirs(context.Background())
	if err != nil {
		return nil, err
	}
	byID := make(map[string]string, len(dirs))
	for _, dir := range dirs {
		byID[filepath.Base(dir)] = dir
	}
	return byID, nil
}

func GetSessionFacts(id string) (*vendors.ParsedSession, error) {
	if id == "" {
		return nil, nil
	}
	dirs, err := sessionDirsByID()
	if err != nil || dirs[id] == "" {
		return nil, err
	}
	return parseSession(dirs[id])
}

// GetSessionFamily returns the session and the children its subagents/ directory names.
func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	dirs, err := sessionDirsByID()
	if err != nil || id == "" || dirs[id] == "" {
		return nil, vendors.EmptySessionMetadata(), err
	}
	root, err := parseSession(dirs[id])
	if err != nil || root == nil {
		return nil, vendors.EmptySessionMetadata(), err
	}
	family := []*vendors.ParsedSession{root}
	for _, meta := range readSubagentMetas(dirs[id]) {
		if dir := dirs[meta.ChildSessionID]; dir != "" {
			child, err := parseSession(dir)
			if err != nil {
				return nil, vendors.EmptySessionMetadata(), err
			}
			if child != nil {
				family = append(family, child)
			}
		}
	}
	attachSubagents(family)
	return family, loadMetadata(), nil
}

func Health() vendors.SourceHealth {
	root, result, err := scan(context.Background())
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
