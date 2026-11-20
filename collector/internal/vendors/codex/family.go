package codex

import (
	"context"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// FileHeader is the session identity a rollout's first row carries. Err marks a
// file whose header could not be read; its family cannot be resolved from it.
// Hidden marks a rollout whose own header declares the guardian (auto-review)
// subagent, which the parser deliberately yields no session for.
type FileHeader struct {
	SessionID string
	ParentID  string
	Hidden    bool
	Err       error
}

// Headers reads only the first row of each rollout. Codex families are
// recoverable only from headers, so grouping reads them before deciding which
// families changed; transcript bodies stay closed.
func Headers(ctx context.Context, source vendors.ReadSource, files []string) (map[string]FileHeader, error) {
	headers := make(map[string]FileHeader, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := readFileHeader(ctx, source, file)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		header.Err = err
		headers[file] = header
	}
	return headers, nil
}

// FamilyRoots walks parent headers to the root session that names each file's
// family. A file whose header failed falls back to its filename session ID, and
// a cycle stops at the session it was first seen from.
func FamilyRoots(headers map[string]FileHeader) map[string]string {
	parents := make(map[string]string, len(headers))
	for _, header := range headers {
		if header.Err == nil && header.SessionID != "" {
			parents[header.SessionID] = header.ParentID
		}
	}
	rootID := func(id string) string {
		seen := map[string]struct{}{}
		for {
			parentID, ok := parents[id]
			if !ok || parentID == "" {
				return id
			}
			if _, repeated := seen[id]; repeated {
				return id
			}
			seen[id] = struct{}{}
			id = parentID
		}
	}
	roots := make(map[string]string, len(headers))
	for file, header := range headers {
		id := header.SessionID
		if header.Err != nil || id == "" {
			id = SessionIDFromRollout(file)
		}
		if id == "" {
			continue
		}
		roots[file] = rootID(id)
	}
	return roots
}

// ParseFamilyFiles parses one selected file set through the same pipeline
// remote collection uses, returning every main-file failure to the caller so one
// bad rollout can be isolated to its own family.
func ParseFamilyFiles(
	ctx context.Context,
	source vendors.ReadSource,
	home string,
	files []string,
	knownActiveFiles []string,
) ([]*vendors.ParsedSession, error) {
	parsed, _, err := vendors.ParseSourceFilesStrict(ctx, source, files,
		func(ctx context.Context, source vendors.ReadSource, path string) (*parsedSession, error) {
			return parseSource(ctx, source, path, func(string, string) bool { return true })
		})
	if err != nil {
		return nil, err
	}
	finalized, err := finalizeParsedFiles(
		ctx,
		source,
		vendors.SourcePathJoin(source, home, ".codex", "archived_sessions"),
		knownActiveFiles,
		parsed,
	)
	if err != nil {
		return nil, err
	}
	clearPromptDerivedNames(finalized)
	return finalized, nil
}
