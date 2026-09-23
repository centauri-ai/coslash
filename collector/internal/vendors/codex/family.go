package codex

import (
	"context"
	"io"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(destination)
}

// FileHeader is the session identity a rollout's first row carries. Err marks a
// file whose header could not be read; its family cannot be resolved from it.
type FileHeader struct {
	SessionID string
	ParentID  string
	Err       error
}

// HeadersSource reads only the first row of each rollout. Codex families are
// recoverable only from headers, so grouping reads them before deciding which
// families changed; transcript bodies stay closed.
func HeadersSource(source vendors.ReadSource, files []string) map[string]FileHeader {
	headers, _ := HeadersSourceContext(context.Background(), source, files)
	return headers
}

func HeadersSourceContext(ctx context.Context, source vendors.ReadSource, files []string) (map[string]FileHeader, error) {
	headers := make(map[string]FileHeader, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, parentID, err := readHeaderSourceContext(ctx, source, file)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		headers[file] = FileHeader{SessionID: id, ParentID: parentID, Err: err}
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

// ParseFamilyFilesSource parses one selected file set through the same pipeline
// remote collection uses, returning every main-file failure to the caller so one
// bad rollout can be isolated to its own family.
func ParseFamilyFilesSource(
	source vendors.ReadSource,
	home string,
	files []string,
	knownActiveFiles []string,
) ([]*vendors.ParsedSession, error) {
	return ParseFamilyFilesSourceContext(context.Background(), source, home, files, knownActiveFiles)
}

func ParseFamilyFilesSourceContext(
	ctx context.Context,
	source vendors.ReadSource,
	home string,
	files []string,
	knownActiveFiles []string,
) ([]*vendors.ParsedSession, error) {
	parsed, _, err := vendors.ParseSourceFilesStrictContext(ctx, source, files,
		func(ctx context.Context, source vendors.ReadSource, path string) (*parsedSession, error) {
			return parseSourceContext(ctx, source, path, func(string, string) bool { return true })
		})
	if err != nil {
		return nil, err
	}
	finalized, err := finalizeParsedFilesContext(
		ctx,
		source,
		filepath.Join(home, ".codex", "archived_sessions"),
		knownActiveFiles,
		parsed,
	)
	if err != nil {
		return nil, err
	}
	clearPromptDerivedNames(finalized)
	return finalized, nil
}
