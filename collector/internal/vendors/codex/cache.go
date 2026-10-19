package codex

import (
	"context"
	"encoding/json"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// ParserVersion identifies this exporter's parse output. Bumping it
// invalidates only Codex entries in the local parse cache.
const ParserVersion = "codex-1"

type cachedSample struct {
	Model string          `json:"model"`
	Usage codexTokenUsage `json:"usage"`
}

type cachedExtra struct {
	ForkedFromID string         `json:"forkedFromId,omitempty"`
	Samples      []cachedSample `json:"samples,omitempty"`
}

// parseSourceCachedContext parses through the local parse cache; the
// fingerprint is taken before the file is read so a change during the parse
// forces a re-parse next time. A rollout the parser hides (a guardian
// subagent) is cached as an absence.
func parseSourceCachedContext(
	ctx context.Context,
	source vendors.ReadSource,
	path string,
	needsApproval func(string, string) bool,
) (*parsedSession, error) {
	cache := vendors.LocalParseCache(source)
	if cache == nil {
		return parseSourceContext(ctx, source, path, needsApproval)
	}
	info, err := source.Stat(path)
	if err != nil {
		return parseSourceContext(ctx, source, path, needsApproval)
	}
	key := vendors.CacheKey{Agent: vendors.AgentCodex, Identity: path, Version: ParserVersion, Fingerprint: vendors.StatFingerprint(info)}
	if payload, ok := cache.Lookup(key); ok {
		if parsed, err := decodeCached(payload); err == nil {
			return parsed, nil
		}
	}
	parsed, err := parseSourceContext(ctx, source, path, needsApproval)
	if err != nil {
		return nil, err
	}
	if payload, err := encodeCached(parsed); err == nil {
		_ = cache.Store(key, payload)
	}
	return parsed, nil
}

func encodeCached(parsed *parsedSession) (json.RawMessage, error) {
	if parsed == nil {
		return vendors.EncodeParsedSession(nil, nil)
	}
	extra := cachedExtra{ForkedFromID: parsed.fork.forkedFromID}
	for _, sample := range parsed.fork.samples {
		extra.Samples = append(extra.Samples, cachedSample{Model: sample.model, Usage: sample.usage})
	}
	return vendors.EncodeParsedSession(parsed.transcript, extra)
}

func decodeCached(payload json.RawMessage) (*parsedSession, error) {
	var extra cachedExtra
	transcript, err := vendors.DecodeParsedSession(payload, &extra)
	if err != nil || transcript == nil {
		return nil, err
	}
	parsed := &parsedSession{transcript: transcript, fork: codexFork{forkedFromID: extra.ForkedFromID}}
	for _, sample := range extra.Samples {
		parsed.fork.samples = append(parsed.fork.samples, codexTokenSample{model: sample.Model, usage: sample.Usage})
	}
	return parsed, nil
}
