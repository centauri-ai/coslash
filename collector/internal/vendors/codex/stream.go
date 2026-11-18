package codex

import (
	"context"
	"encoding/json"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// headerVersion identifies the cached first-row header of a rollout.
const headerVersion = "codex-header-1"

type cachedHeader struct {
	SessionID string `json:"sessionId"`
	ParentID  string `json:"parentId,omitempty"`
}

// Families groups local rollouts by their topmost parent-less
// session, reading each file's first row through the parse cache. A file
// whose header cannot be read is its own family keyed by its filename ID,
// or by its path when it has none.
func Families(ctx context.Context, files []string) (map[string][]string, error) {
	cache := vendors.ActiveParseCache()
	headers := make(map[string]cachedHeader, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var key vendors.CacheKey
		if cache != nil {
			if info, err := vendors.LocalReadSource.Stat(file); err == nil {
				key = vendors.CacheKey{Agent: vendors.AgentCodex, Identity: file, Version: headerVersion, Fingerprint: vendors.StatFingerprint(info)}
				if payload, ok := cache.Lookup(key); ok {
					var header cachedHeader
					if json.Unmarshal(payload, &header) == nil && header.SessionID != "" {
						headers[file] = header
						continue
					}
				}
			}
		}
		id, parentID, err := readHeader(ctx, vendors.LocalReadSource, file)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		header := cachedHeader{SessionID: id, ParentID: parentID}
		headers[file] = header
		if cache != nil && key.Fingerprint != "" {
			if payload, err := json.Marshal(header); err == nil {
				_ = cache.Store(key, payload)
			}
		}
	}
	parents := make(map[string]string, len(headers))
	for _, header := range headers {
		parents[header.SessionID] = header.ParentID
	}
	rootOf := func(id string) string {
		seen := map[string]bool{}
		for {
			parentID, ok := parents[id]
			if !ok || parentID == "" || seen[id] {
				return id
			}
			seen[id] = true
			id = parentID
		}
	}
	families := map[string][]string{}
	for _, file := range files {
		id := ""
		if header, ok := headers[file]; ok {
			id = rootOf(header.SessionID)
		} else if id = SessionIDFromRollout(file); id == "" {
			id = file
		}
		families[id] = append(families[id], file)
	}
	return families, nil
}
