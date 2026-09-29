package vendors

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

// ParseCache stores one parsed summary per local source, keyed by the source
// identity plus the size/mtime fingerprint and parser version that produced
// it, so an unchanged source is never parsed twice. Implementations are safe
// for concurrent use and treat every stored payload as replaceable: a decode
// failure on read is a miss, never an error.
type ParseCache interface {
	Lookup(key CacheKey) (json.RawMessage, bool)
	Store(key CacheKey, payload json.RawMessage) error
}

// CacheKey names one cached summary. Identity is stable across runs (a local
// path or a family ID within the agent); Fingerprint changes whenever the
// source bytes may have changed; Version is the producing parser's version.
type CacheKey struct {
	Agent       string
	Identity    string
	Version     string
	Fingerprint string
}

var activeParseCache atomic.Pointer[parseCacheHolder]

type parseCacheHolder struct{ cache ParseCache }

// SetParseCache installs the process-wide cache used by local parsing. A nil
// cache restores uncached parsing.
func SetParseCache(cache ParseCache) {
	if cache == nil {
		activeParseCache.Store(nil)
		return
	}
	activeParseCache.Store(&parseCacheHolder{cache: cache})
}

// ActiveParseCache returns the installed cache, or nil.
func ActiveParseCache() ParseCache {
	holder := activeParseCache.Load()
	if holder == nil {
		return nil
	}
	return holder.cache
}

// LocalParseCache returns the installed cache only for the host filesystem;
// remote sources keep their own family cache.
func LocalParseCache(source ReadSource) ParseCache {
	if source != LocalReadSource {
		return nil
	}
	return ActiveParseCache()
}

// StatFingerprint identifies a file's bytes by size and modification time.
func StatFingerprint(info fs.FileInfo) string {
	return strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// FilesFingerprint identifies a set of local files independent of order.
func FilesFingerprint(source ReadSource, paths []string) (string, error) {
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := source.Stat(path)
		if err != nil {
			return "", err
		}
		parts = append(parts, filepath.ToSlash(path)+"="+StatFingerprint(info))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";"), nil
}

// cachedSession carries the session fields the API JSON hides, so a cached
// summary round-trips exactly what the parser produced.
type cachedSession struct {
	Session          json.RawMessage             `json:"session"`
	ParentSessionID  string                      `json:"parentSessionId,omitempty"`
	StartedAt        int64                       `json:"startedAt,omitempty"`
	ActivityFallback bool                        `json:"activityFallback,omitempty"`
	CommitLog        []session.CommitObservation `json:"commitLog,omitempty"`
	CommitSHAs       []string                    `json:"commitShas,omitempty"`
	CompactionSeed   string                      `json:"compactionSeed,omitempty"`
	DigestSpawnKeys  []string                    `json:"digestSpawnKeys,omitempty"`
	FileEditChanges  [][]session.FileChange      `json:"fileEditChanges,omitempty"`
}

type cachedParsed struct {
	Session         cachedSession             `json:"session"`
	LogPath         string                    `json:"logPath,omitempty"`
	LogModifiedAtMs int64                     `json:"logModifiedAtMs,omitempty"`
	ParentID        string                    `json:"parentId,omitempty"`
	SpawnKey        string                    `json:"spawnKey,omitempty"`
	Stopped         bool                      `json:"stopped,omitempty"`
	Result          string                    `json:"result,omitempty"`
	Spawns          map[string]SpawnState     `json:"spawns"`
	Commands        []session.SubagentCommand `json:"commands"`
	Name            string                    `json:"name,omitempty"`
	InTurn          bool                      `json:"inTurn,omitempty"`
	StatusHint      *string                   `json:"statusHint,omitempty"`
	RecordedCost    *float64                  `json:"recordedCost,omitempty"`
	Extra           json.RawMessage           `json:"extra,omitempty"`
}

var errCachedSessionMissing = errors.New("cached summary has no session")

// EncodeParsedSession serializes a parsed session with an optional
// vendor-private extra payload. A nil session encodes as an explicit absence
// so a vendor that parses a file to nothing can cache that outcome.
func EncodeParsedSession(parsed *ParsedSession, extra any) (json.RawMessage, error) {
	envelope := cachedParsed{}
	if extra != nil {
		encoded, err := json.Marshal(extra)
		if err != nil {
			return nil, err
		}
		envelope.Extra = encoded
	}
	if parsed != nil && parsed.Session != nil {
		body, err := json.Marshal(parsed.Session)
		if err != nil {
			return nil, err
		}
		s := parsed.Session
		envelope.Session = cachedSession{
			Session: body, ParentSessionID: s.ParentSessionID, StartedAt: s.StartedAt,
			ActivityFallback: s.ActivityFallback, CommitLog: s.CommitLog, CommitSHAs: s.CommitSHAs,
			CompactionSeed: s.CompactionSeed,
		}
		for _, entry := range s.Digest {
			envelope.Session.DigestSpawnKeys = append(envelope.Session.DigestSpawnKeys, entry.SpawnKey)
		}
		for _, edit := range s.FileEdits {
			envelope.Session.FileEditChanges = append(envelope.Session.FileEditChanges, edit.Changes())
		}
		envelope.LogPath, envelope.LogModifiedAtMs = parsed.LogPath, parsed.LogModifiedAtMs
		envelope.ParentID, envelope.SpawnKey, envelope.Stopped = parsed.ParentID, parsed.SpawnKey, parsed.Stopped
		envelope.Result, envelope.Spawns, envelope.Commands = parsed.Result, parsed.Spawns, parsed.Commands
		envelope.Name, envelope.InTurn, envelope.StatusHint = parsed.Name, parsed.InTurn, parsed.StatusHint
		envelope.RecordedCost = parsed.RecordedCost
	}
	return json.Marshal(envelope)
}

// DecodeParsedSession restores a parsed session and decodes the vendor-private
// extra payload into extra when both are present. A cached absence decodes to
// a nil session and a nil error.
func DecodeParsedSession(payload json.RawMessage, extra any) (*ParsedSession, error) {
	var envelope cachedParsed
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	if extra != nil && len(envelope.Extra) > 0 {
		if err := json.Unmarshal(envelope.Extra, extra); err != nil {
			return nil, err
		}
	}
	if len(envelope.Session.Session) == 0 {
		return nil, errCachedSessionMissing
	}
	if string(envelope.Session.Session) == "null" {
		return nil, nil
	}
	s := &session.Session{}
	if err := json.Unmarshal(envelope.Session.Session, s); err != nil {
		return nil, err
	}
	if s.Agent == "" && s.ID == "" {
		return nil, errCachedSessionMissing
	}
	cached := envelope.Session
	s.ParentSessionID, s.StartedAt, s.ActivityFallback = cached.ParentSessionID, cached.StartedAt, cached.ActivityFallback
	s.CommitLog, s.CommitSHAs, s.CompactionSeed = cached.CommitLog, cached.CommitSHAs, cached.CompactionSeed
	if len(cached.DigestSpawnKeys) == len(s.Digest) {
		for index := range s.Digest {
			s.Digest[index].SpawnKey = cached.DigestSpawnKeys[index]
		}
	}
	if len(cached.FileEditChanges) == len(s.FileEdits) {
		for index, edit := range s.FileEdits {
			s.FileEdits[index] = session.FileEditWithIdentifiedChanges(
				edit.Path, edit.Additions, edit.Deletions, edit.Edits, edit.IsNew, edit.ChangeIDs, cached.FileEditChanges[index],
			)
		}
	}
	return &ParsedSession{
		Session: s, LogPath: envelope.LogPath, LogModifiedAtMs: envelope.LogModifiedAtMs,
		ParentID: envelope.ParentID, SpawnKey: envelope.SpawnKey, Stopped: envelope.Stopped,
		Result: envelope.Result, Spawns: envelope.Spawns, Commands: envelope.Commands,
		Name: envelope.Name, InTurn: envelope.InTurn, StatusHint: envelope.StatusHint,
		RecordedCost: envelope.RecordedCost,
	}, nil
}
