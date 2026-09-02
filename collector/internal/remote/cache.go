package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const cacheVersion = 1

var (
	// ErrHelperOwnershipCorrupt is fail-closed: ownership protects remote code
	// from being silently orphaned and malformed evidence cannot mean "none".
	ErrHelperOwnershipCorrupt = errors.New("helper ownership record is corrupt")
	// ErrHelperOwnershipLegacy marks the former {"version":"..."} format.
	// Manager migrates it only after binding it to the configured SSH alias.
	ErrHelperOwnershipLegacy = errors.New("helper ownership record needs alias migration")
)

type AgentCoverage struct {
	Agent          string `json:"agent"`
	CandidateFiles int    `json:"candidateFiles"`
	SelectedFiles  int    `json:"selectedFiles"`
	SkippedEntries int    `json:"skippedEntries,omitempty"`
	Truncated      bool   `json:"truncated"`
	Error          string `json:"error,omitempty"`
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

type helperOwnership struct {
	Version string `json:"version"`
	Alias   string `json:"alias"`
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

// Cache v2 persists normalized per-family facts (remotefacts.Family) instead
// of composed session cards, so an incremental refresh can skip re-parsing an
// unchanged family and still reconstruct a displayable snapshot from what
// changed. It is written to a separate file from the v1 snapshot: a v1 card
// stays visible (marked stale) until the first v2 generation commits, and a
// v1 fingerprint is never reinterpreted as v2 baseline state — the first v2
// refresh always starts from an empty generation.
const cacheV2Version = 2
const maxCacheV2Bytes = 64 << 20

// CachedFamilyV2 is one durable family entry. Vendor and FamilyID are stored
// alongside Facts (rather than only as a map key) so the file round-trips
// through JSON without a custom marshaler.
type CachedFamilyV2 struct {
	Vendor          string             `json:"vendor"`
	FamilyID        string             `json:"familyId"`
	Facts           remotefacts.Family `json:"facts"`
	Fingerprint     string             `json:"fingerprint"`
	StaleReason     string             `json:"staleReason,omitempty"`
	LastSuccessAtMs int64              `json:"lastSuccessAtMs"`
}

// CachedCodexHeader lets an unchanged Codex file's session/parent header be
// reused without reopening it, keyed by the file's identity fingerprint.
type CachedCodexHeader struct {
	Key           string `json:"key"`
	Size          int64  `json:"size"`
	ModifiedAtMs  int64  `json:"modifiedAtMs"`
	ParserVersion string `json:"parserVersion"`
	SessionID     string `json:"sessionId"`
	ParentID      string `json:"parentId,omitempty"`
}

type CachedSnapshotV2 struct {
	Version         int                 `json:"version"`
	BaselineID      string              `json:"baselineId"`
	CoverageSinceMs int64               `json:"coverageSinceMs"`
	Families        []CachedFamilyV2    `json:"families"`
	Coverage        []AgentCoverage     `json:"coverage,omitempty"`
	FetchedAtMs     int64               `json:"fetchedAtMs"`
	RoundTripMs     int64               `json:"roundTripMs"`
	CodexHeaders    []CachedCodexHeader `json:"codexHeaders,omitempty"`
}

func (c *Cache) snapshotV2Path(sourceID string) (string, error) {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snapshot-v2.json"), nil
}

func (c *Cache) helperOwnershipPath(sourceID string) (string, error) {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "helper.json"), nil
}

// LoadHelperVersion records local ownership, not executable authority. A
// loaded version must still pass fresh signed-metadata, remote-file, and
// capability verification before it becomes a helperTarget.
func (c *Cache) LoadHelperOwnership(sourceID string) (helperOwnership, bool, error) {
	path, err := c.helperOwnershipPath(sourceID)
	if err != nil {
		return helperOwnership{}, false, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return helperOwnership{}, false, nil
	}
	if err != nil {
		return helperOwnership{}, false, err
	}
	if len(content) == 0 || len(content) > 256 {
		return helperOwnership{}, false, ErrHelperOwnershipCorrupt
	}
	var document struct {
		Version *string `json:"version"`
		Alias   *string `json:"alias"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(&struct{}{}) != io.EOF || document.Version == nil || !helperVersionPattern.MatchString(*document.Version) {
		return helperOwnership{}, false, ErrHelperOwnershipCorrupt
	}
	if document.Alias == nil {
		return helperOwnership{Version: *document.Version}, true, ErrHelperOwnershipLegacy
	}
	if !settings.ValidSSHAlias(*document.Alias) {
		return helperOwnership{}, false, ErrHelperOwnershipCorrupt
	}
	return helperOwnership{Version: *document.Version, Alias: *document.Alias}, true, nil
}

func (c *Cache) StoreHelperVersion(sourceID, version, alias string) error {
	if !helperVersionPattern.MatchString(version) || !settings.ValidSSHAlias(alias) {
		return ErrHelperArtifact
	}
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	content, err := json.Marshal(helperOwnership{Version: version, Alias: alias})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".helper-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	path, err := c.helperOwnershipPath(sourceID)
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (c *Cache) RemoveHelperVersion(sourceID string) error {
	path, err := c.helperOwnershipPath(sourceID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// LoadV2 returns ok=false for a missing, corrupt, or out-of-bounds file
// rather than an error, so a damaged cache degrades to a cold start instead of
// blocking startup.
func (c *Cache) LoadV2(sourceID string) (CachedSnapshotV2, bool, error) {
	path, err := c.snapshotV2Path(sourceID)
	if err != nil {
		return CachedSnapshotV2{}, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CachedSnapshotV2{}, false, nil
		}
		return CachedSnapshotV2{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return CachedSnapshotV2{}, false, err
	}
	if info.Size() < 0 || info.Size() > maxCacheV2Bytes {
		return CachedSnapshotV2{}, false, nil
	}
	var cached CachedSnapshotV2
	decoder := json.NewDecoder(io.LimitReader(file, maxCacheV2Bytes+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cached) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validCachedSnapshotV2(cached) {
		return CachedSnapshotV2{}, false, nil
	}
	return cached, true, nil
}

func validCachedSnapshotV2(cached CachedSnapshotV2) bool {
	if cached.Version != cacheV2Version || len(cached.BaselineID) > remotefacts.MaxIDBytes ||
		cached.CoverageSinceMs < 0 || cached.CoverageSinceMs > remotefacts.MaxTimestampMs ||
		cached.FetchedAtMs < 0 || cached.FetchedAtMs > remotefacts.MaxTimestampMs || cached.RoundTripMs < 0 ||
		len(cached.Families) > remoteprotocol.MaxRecords || len(cached.CodexHeaders) > DefaultMaxEntries ||
		len(cached.Coverage) > 2 {
		return false
	}
	seenCoverage := map[string]bool{}
	for _, coverage := range cached.Coverage {
		if (coverage.Agent != vendors.AgentClaude && coverage.Agent != vendors.AgentCodex) || seenCoverage[coverage.Agent] ||
			coverage.CandidateFiles < 0 || coverage.CandidateFiles > remotefacts.MaxCount ||
			coverage.SelectedFiles < 0 || coverage.SelectedFiles > coverage.CandidateFiles ||
			len(coverage.Error) > MaxErrorCopyBytes {
			return false
		}
		seenCoverage[coverage.Agent] = true
	}
	seen := map[remoteprotocol.FamilyKey]bool{}
	for _, family := range cached.Families {
		if family.Vendor != vendors.AgentClaude && family.Vendor != vendors.AgentCodex {
			return false
		}
		if family.FamilyID == "" || family.FamilyID != family.Facts.FamilyID || family.Vendor != family.Facts.Vendor {
			return false
		}
		if family.Fingerprint == "" || len(family.Fingerprint) > remotefacts.MaxIDBytes {
			return false
		}
		if family.StaleReason != "" && !remotefacts.ValidStaleReason(family.StaleReason) {
			return false
		}
		if family.LastSuccessAtMs <= 0 || family.LastSuccessAtMs > remotefacts.MaxTimestampMs {
			return false
		}
		if remotefacts.Validate(family.Facts) != nil {
			return false
		}
		key := remoteprotocol.FamilyKey{Vendor: family.Vendor, FamilyID: family.FamilyID}
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	seenHeaders := map[string]bool{}
	for _, header := range cached.CodexHeaders {
		if header.Key == "" || len(header.Key) > remotefacts.MaxIDBytes || header.Size < 0 ||
			header.ModifiedAtMs < 0 || header.ModifiedAtMs > remotefacts.MaxTimestampMs {
			return false
		}
		if header.SessionID == "" || len(header.SessionID) > remotefacts.MaxIDBytes ||
			header.ParserVersion == "" || len(header.ParserVersion) > remotefacts.MaxIDBytes ||
			len(header.ParentID) > remotefacts.MaxIDBytes {
			return false
		}
		if seenHeaders[header.Key] {
			return false
		}
		seenHeaders[header.Key] = true
	}
	return true
}

// StoreV2 writes the snapshot atomically: a temp file is written, synced, and
// closed before an atomic rename replaces the prior snapshot, and the
// containing directory is synced afterward where the platform supports it.
func (c *Cache) StoreV2(sourceID string, cached CachedSnapshotV2) error {
	cached.Version = cacheV2Version
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
	if len(data) > maxCacheV2Bytes {
		return fmt.Errorf("cache v2 exceeds %d-byte limit", maxCacheV2Bytes)
	}
	temp, err := os.CreateTemp(dir, ".snapshot-v2-*.tmp")
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
	path, err := c.snapshotV2Path(sourceID)
	if err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	syncDirBestEffort(dir)
	return nil
}

// syncDirBestEffort fsyncs a directory so a rename inside it is durable. Not
// every platform supports fsync on a directory descriptor; failure here is
// deliberately ignored since the rename itself already completed.
func syncDirBestEffort(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	defer handle.Close()
	_ = handle.Sync()
}
