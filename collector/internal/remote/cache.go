package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const cacheVersion = 1

type AgentCoverage struct {
	Agent          string `json:"agent"`
	CandidateFiles int    `json:"candidateFiles"`
	SelectedFiles  int    `json:"selectedFiles"`
	Truncated      bool   `json:"truncated"`
	Error          string `json:"error,omitempty"`
	ErrorReason    string `json:"errorReason,omitempty"`
}

// CachedSession is intentionally narrower than session.Session so transcript
// content and remote paths cannot accidentally reach disk.
type CachedSession struct {
	Agent            string                         `json:"agent"`
	ID               string                         `json:"id"`
	Name             *string                        `json:"name,omitempty"`
	Status           *string                        `json:"status,omitempty"`
	Branch           *string                        `json:"branch,omitempty"`
	DurationMs       *int                           `json:"durationMs,omitempty"`
	Tokens           map[string]session.ModelTokens `json:"tokens,omitempty"`
	Cost             float64                        `json:"cost,omitempty"`
	UnpricedModels   []string                       `json:"unpricedModels,omitempty"`
	StartedAt        int64                          `json:"startedAt"`
	LastActivityTime int64                          `json:"lastActivityAt"`
	Entrypoint       *string                        `json:"entrypoint,omitempty"`
	Model            *string                        `json:"model,omitempty"`
	ContextTokens    *int                           `json:"contextTokens,omitempty"`
	ContextWindow    *int                           `json:"contextWindow,omitempty"`
	Turns            int                            `json:"turns,omitempty"`
	ToolUses         int                            `json:"toolUses,omitempty"`
	Errors           int                            `json:"errors,omitempty"`
	Compactions      int                            `json:"compactions,omitempty"`
	PullRequests     int                            `json:"pullRequests,omitempty"`
	EditedFileCount  int                            `json:"editedFileCount,omitempty"`
}

type CachedSnapshot struct {
	Version         int                                  `json:"version"`
	Sessions        []CachedSession                      `json:"sessions"`
	Coverage        []AgentCoverage                      `json:"coverage"`
	Fingerprints    map[string][]vendors.FileFingerprint `json:"fingerprints,omitempty"`
	CoverageSinceMs int64                                `json:"coverageSinceMs"`
	FetchedAtMs     int64                                `json:"fetchedAtMs"`
	RoundTripMs     int64                                `json:"roundTripMs"`
	Truncated       bool                                 `json:"truncated"`
}

func snapshotForCache(
	sessions []*session.Session,
	coverage []AgentCoverage,
	fingerprints map[string][]vendors.FileFingerprint,
	coverageSinceMs, fetchedAtMs, roundTripMs int64,
) CachedSnapshot {
	cached := CachedSnapshot{
		Version: cacheVersion, Coverage: append([]AgentCoverage(nil), coverage...),
		Fingerprints:    fingerprints,
		CoverageSinceMs: coverageSinceMs, FetchedAtMs: fetchedAtMs, RoundTripMs: roundTripMs,
	}
	for _, item := range coverage {
		cached.Truncated = cached.Truncated || item.Truncated
	}
	for _, item := range sessions {
		cached.Sessions = append(cached.Sessions, CachedSession{
			Agent: item.Agent, ID: item.ID, Name: item.Name, Status: item.Status,
			Branch: item.Branch, DurationMs: item.DurationMs, Tokens: item.Tokens,
			Cost: item.Cost, UnpricedModels: append([]string(nil), item.UnpricedModels...),
			StartedAt: item.StartedAt, LastActivityTime: item.LastActivityTime,
			Entrypoint: item.Entrypoint, Model: item.Model, ContextTokens: item.ContextTokens,
			ContextWindow: item.ContextWindow, Turns: item.Turns, ToolUses: item.ToolUses,
			Errors: item.Errors, Compactions: item.Compactions, PullRequests: item.PullRequests,
			EditedFileCount: item.EditedFileCount,
		})
	}
	return cached
}

func (cached CachedSnapshot) sessions() []*session.Session {
	result := make([]*session.Session, 0, len(cached.Sessions))
	for _, item := range cached.Sessions {
		tokens := item.Tokens
		if tokens == nil {
			tokens = map[string]session.ModelTokens{}
		}
		result = append(result, &session.Session{
			Agent: item.Agent, ID: item.ID, Name: item.Name, Status: item.Status,
			Branch: item.Branch, DurationMs: item.DurationMs, Tokens: tokens,
			Cost: item.Cost, UnpricedModels: append([]string{}, item.UnpricedModels...),
			Subagents: []session.Subagent{},
			StartedAt: item.StartedAt, LastActivityTime: item.LastActivityTime,
			Entrypoint: item.Entrypoint, EditedFileCount: item.EditedFileCount,
			SessionDetails: session.SessionDetails{
				Model: item.Model, ContextTokens: item.ContextTokens, ContextWindow: item.ContextWindow,
				Turns: item.Turns, ToolUses: item.ToolUses, Errors: item.Errors,
				Compactions: item.Compactions, PullRequests: item.PullRequests,
				Commands: []string{}, Commits: []string{}, Todos: []session.Todo{},
				Digest: []session.DigestEntry{}, FileEdits: []session.FileEdit{},
			},
		})
	}
	return result
}

type Cache struct {
	Root string
}

func NewCache(root string) *Cache {
	if root == "" {
		root = settings.Home()
	}
	return &Cache{Root: root}
}

func (c *Cache) sourceDir(sourceID string) (string, error) {
	if !settings.ValidRemoteID(sourceID) {
		return "", fmt.Errorf("invalid remote source id %q", sourceID)
	}
	return filepath.Join(c.Root, "remotes", sourceID), nil
}

func (c *Cache) snapshotPath(sourceID string) (string, error) {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snapshot.json"), nil
}

func (c *Cache) Load(sourceID string) (CachedSnapshot, bool, error) {
	path, err := c.snapshotPath(sourceID)
	if err != nil {
		return CachedSnapshot{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CachedSnapshot{}, false, nil
		}
		return CachedSnapshot{}, false, err
	}
	var cached CachedSnapshot
	if json.Unmarshal(data, &cached) != nil || cached.Version != cacheVersion {
		return CachedSnapshot{}, false, nil
	}
	for _, item := range cached.Sessions {
		if item.ID == "" || (item.Agent != "claude" && item.Agent != "codex") {
			return CachedSnapshot{}, false, nil
		}
	}
	return cached, true, nil
}

func (c *Cache) Store(sourceID string, cached CachedSnapshot) error {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return err
	}
	remotes := filepath.Join(c.Root, "remotes")
	if err := os.MkdirAll(remotes, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(remotes, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	path, err := c.snapshotPath(sourceID)
	if err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (c *Cache) RemoveSource(sourceID string) error {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func age(fetchedAtMs int64, now time.Time) time.Duration {
	if fetchedAtMs <= 0 {
		return FreshnessInterval + time.Second
	}
	return now.Sub(time.UnixMilli(fetchedAtMs))
}
