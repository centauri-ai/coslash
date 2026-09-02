package codex

import (
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

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
	headers := make(map[string]FileHeader, len(files))
	for _, file := range files {
		id, parentID, err := readHeaderSource(source, file)
		headers[file] = FileHeader{SessionID: id, ParentID: parentID, Err: err}
	}
	return headers
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
) ([]*vendors.ParsedSession, error) {
	parsed, _, err := parseFilesSourceStrict(
		source,
		filepath.Join(home, ".codex", "archived_sessions"),
		files,
		// Remote collection never inspects the remote working directory, so every
		// command keeps the approval-required shape both transports agree on.
		func(string, string) bool { return true },
	)
	if err != nil {
		return nil, err
	}
	// Codex currently derives ParsedSession.Name from the first user prompt.
	// Prompts are outside remote schema v1, so do not let that fallback cross the
	// helper boundary. Approved session-index metadata remains available.
	for _, item := range parsed {
		if item != nil {
			item.Name = ""
		}
	}
	return parsed, nil
}
