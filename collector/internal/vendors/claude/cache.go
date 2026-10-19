package claude

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// ParserVersion identifies this exporter's parse output. Bumping it
// invalidates only Claude Code entries in the local parse cache.
const ParserVersion = "claude-1"

type cachedUsage struct {
	Model string       `json:"model"`
	Usage *claudeUsage `json:"usage,omitempty"`
}

type cachedExtra struct {
	ForkUsage  map[string]cachedUsage `json:"forkUsage,omitempty"`
	RowUUIDs   []string               `json:"rowUuids,omitempty"`
	RowCount   int                    `json:"rowCount,omitempty"`
	Background bool                   `json:"background,omitempty"`
}

// parseSourceCachedContext parses through the local parse cache. The
// fingerprint is taken before the file is read, so a file that changes during
// the parse is re-parsed on the next pass rather than served stale.
func parseSourceCachedContext(ctx context.Context, source vendors.ReadSource, path string) (*parsedSession, error) {
	cache := vendors.LocalParseCache(source)
	if cache == nil {
		return parseSourceContext(ctx, source, path)
	}
	fingerprint, err := transcriptFingerprint(source, path)
	if err != nil {
		return parseSourceContext(ctx, source, path)
	}
	key := vendors.CacheKey{Agent: vendors.AgentClaude, Identity: path, Version: ParserVersion, Fingerprint: fingerprint}
	if payload, ok := cache.Lookup(key); ok {
		if parsed, err := decodeCached(payload); err == nil {
			return parsed, nil
		}
	}
	parsed, err := parseSourceContext(ctx, source, path)
	if err != nil {
		return nil, err
	}
	if payload, err := encodeCached(parsed); err == nil {
		_ = cache.Store(key, payload)
	}
	return parsed, nil
}

// transcriptFingerprint covers the transcript and, for a subagent, the
// metadata sidecar the parser reads with it.
func transcriptFingerprint(source vendors.ReadSource, path string) (string, error) {
	paths := []string{path}
	if ParentIDFromPath(path) != "" {
		meta := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
		if _, err := source.Stat(meta); err == nil {
			paths = append(paths, meta)
		}
	}
	return vendors.FilesFingerprint(source, paths)
}

func encodeCached(parsed *parsedSession) (json.RawMessage, error) {
	extra := cachedExtra{RowCount: parsed.rowCount, Background: parsed.background}
	if len(parsed.forkUsage) > 0 {
		extra.ForkUsage = make(map[string]cachedUsage, len(parsed.forkUsage))
		for id, usage := range parsed.forkUsage {
			extra.ForkUsage[id] = cachedUsage{Model: usage.model, Usage: usage.usage}
		}
	}
	for id := range parsed.rowUUIDs {
		extra.RowUUIDs = append(extra.RowUUIDs, id)
	}
	return vendors.EncodeParsedSession(parsed.transcript, extra)
}

func decodeCached(payload json.RawMessage) (*parsedSession, error) {
	var extra cachedExtra
	transcript, err := vendors.DecodeParsedSession(payload, &extra)
	if err != nil || transcript == nil {
		return nil, err
	}
	parsed := &parsedSession{
		transcript: transcript, rowCount: extra.RowCount, background: extra.Background,
		forkUsage: map[string]messageUsage{}, rowUUIDs: make(map[string]struct{}, len(extra.RowUUIDs)),
	}
	for id, usage := range extra.ForkUsage {
		parsed.forkUsage[id] = messageUsage{model: usage.Model, usage: usage.Usage}
	}
	for _, id := range extra.RowUUIDs {
		parsed.rowUUIDs[id] = struct{}{}
	}
	return parsed, nil
}
