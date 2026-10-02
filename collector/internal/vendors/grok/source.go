package grok

import (
	"cmp"
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
		// A family is in the window when any member is recent, so a parent and its subagents stay together.
		families := make(map[string]string, len(dirs))
		recentFamilies := map[string]bool{}
		for _, dir := range dirs {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			families[dir] = familyID(dir)
			if modifiedAt(dir) >= since {
				recentFamilies[families[dir]] = true
			}
		}
		recent := dirs[:0]
		for _, dir := range dirs {
			if recentFamilies[families[dir]] {
				recent = append(recent, dir)
			}
		}
		dirs, _, err = vendors.LimitNewestFileFamiliesContext(ctx, recent, vendors.MaxCandidateFilesPerAgent,
			func(dir string) string { return families[dir] }, modifiedAt)
		if err != nil {
			return nil, nil, err
		}
	}
	parsed, err := vendors.ParseFilesContext(ctx, dirs, func(_ context.Context, dir string) (*vendors.ParsedSession, error) {
		return parseSession(dir)
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

// familyID is the root session id: parent_session_id for a subagent, else the session's own id.
func familyID(dir string) string {
	summary, err := readSummary(dir)
	switch {
	case err != nil:
		return dir
	case isSubagentKind(summary.SessionKind) && summary.ParentSessionID != "":
		return summary.ParentSessionID
	case summary.Info.ID != "":
		return summary.Info.ID
	}
	return dir
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
	family, _, err := GetSessionFamily(id)
	if err != nil || len(family) == 0 {
		return nil, err
	}
	var root *vendors.ParsedSession
	for _, item := range family {
		if item.Session.ID == id {
			root = item
			break
		}
	}
	if root == nil || root.ParentID != "" {
		return root, nil
	}
	root.Session.Subagents = subagentsFromFamily(root, family)
	return root, nil
}

func subagentsFromFamily(root *vendors.ParsedSession, family []*vendors.ParsedSession) []session.Subagent {
	subagents := make([]session.Subagent, 0)
	for _, item := range family {
		if item.ParentID != root.Session.ID {
			continue
		}
		status := session.SubagentRunning
		if item.Stopped {
			status = session.SubagentAborted
		} else if root.Spawns[item.Session.ID].Completed {
			status = session.SubagentReturned
		}
		subagent := session.Subagent{
			ID:         item.Session.ID,
			ParentID:   root.Session.ID,
			Name:       cmp.Or(item.Name, stringPtr(item.Session.Name), item.Session.ID),
			Model:      item.Session.Model,
			Status:     status,
			Task:       root.Spawns[item.Session.ID].Task,
			Result:     item.Result,
			DurationMs: item.Session.DurationMs,
			ToolUses:   item.Session.ToolUses,
		}
		linkSubagentDigest(root.Session, subagent)
		subagents = append(subagents, subagent)
	}
	return subagents
}

func linkSubagentDigest(parent *session.Session, subagent session.Subagent) {
	for index := range parent.Digest {
		entry := &parent.Digest[index]
		if entry.Category == session.DigestSubagent && entry.SpawnKey == subagent.ID && entry.SubagentID == "" {
			entry.SubagentID = subagent.ID
			entry.Description = subagent.Name
			return
		}
	}
	parent.Digest = append(parent.Digest, session.DigestEntry{
		Turn: digestTurn(parent.Digest), Category: session.DigestSubagent, Description: subagent.Name, SubagentID: subagent.ID, SpawnKey: subagent.ID,
	})
}

func digestTurn(entries []session.DigestEntry) int {
	turn := 1
	for _, entry := range entries {
		if entry.Turn > turn {
			turn = entry.Turn
		}
	}
	return turn
}

func stringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
