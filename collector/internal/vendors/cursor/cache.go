package cursor

import (
	"context"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// ParserVersion identifies this exporter's parse output. Bumping it
// invalidates only Cursor entries in the local parse cache.
const ParserVersion = "cursor-1"

// parseTranscriptFragmentsCachedContext parses one session's fragments
// through the local parse cache, keyed by the session ID and the fingerprint
// of every fragment. The fingerprint is taken before any fragment is read.
func parseTranscriptFragmentsCachedContext(ctx context.Context, source vendors.ReadSource, paths []string) (*vendors.ParsedSession, error) {
	cache := vendors.LocalParseCache(source)
	if cache == nil || len(paths) == 0 {
		return parseTranscriptFragmentsSourceContext(ctx, source, paths)
	}
	fingerprint, err := vendors.FilesFingerprint(source, paths)
	if err != nil {
		return parseTranscriptFragmentsSourceContext(ctx, source, paths)
	}
	key := vendors.CacheKey{Agent: vendors.AgentCursor, Identity: IDFromPath(paths[0]), Version: ParserVersion, Fingerprint: fingerprint}
	if payload, ok := cache.Lookup(key); ok {
		if parsed, err := vendors.DecodeParsedSession(payload, nil); err == nil && parsed != nil {
			return parsed, nil
		}
	}
	parsed, err := parseTranscriptFragmentsSourceContext(ctx, source, paths)
	if err != nil {
		return nil, err
	}
	if payload, err := vendors.EncodeParsedSession(parsed, nil); err == nil {
		_ = cache.Store(key, payload)
	}
	return parsed, nil
}
