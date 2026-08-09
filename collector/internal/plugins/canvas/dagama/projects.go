package dagama

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// MaxProjectBytes bounds one project record. It is three short strings; a
// larger file is a sign of an injected payload rather than a project someone
// opened.
const MaxProjectBytes int64 = 8 << 10

// Project is a folder the operator has opened for DaGama workflows.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// ProjectStore maps opened folders to the stable identifiers every other route
// is scoped by.
//
// The identifier is derived from the canonical path rather than allocated, so
// it survives a collector restart: the legacy dev server kept opened projects
// in memory and every client call started failing with PROJECT_NOT_OPEN after a
// restart. It is still recorded on disk, because a board and a run are stored
// under the identifier and the path must be recoverable from it — a run needs
// the repository it was authorized against, and an identifier alone cannot
// produce one.
type ProjectStore struct {
	scope *runfs.Scope
}

// NewProjectStore binds a store to a scope rooted at the projects root.
func NewProjectStore(scope *runfs.Scope) (*ProjectStore, error) {
	if scope == nil {
		return nil, newError(CodeStorageFailed, "a project store requires a scope")
	}
	return &ProjectStore{scope: scope}, nil
}

func projectRecordPath(projectID string) (string, error) {
	if !ValidProjectID(projectID) {
		return "", &Error{Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId"}
	}
	return path.Join(projectID, "project.json"), nil
}

// ProjectIDFor derives the stable identifier for a canonical project path.
//
// The digest is what makes it stable and collision-safe; the readable prefix is
// only so an operator inspecting the storage root can tell which directory a
// folder belongs to. Two projects whose basenames collide still get different
// identifiers, and the same project always gets the same one.
func ProjectIDFor(canonicalPath string) string {
	digest := sha256.Sum256([]byte(canonicalPath))
	suffix := hex.EncodeToString(digest[:])[:12]

	base := strings.ToLower(filepath.Base(canonicalPath))
	cleaned := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			return character
		case character == '-', character == '_', character == '.':
			return character
		default:
			return '-'
		}
	}, base)
	cleaned = strings.Trim(cleaned, "-._")
	if len(cleaned) > 24 {
		cleaned = cleaned[:24]
	}
	// ValidProjectID requires an alphanumeric first character, so a basename
	// that reduced to nothing usable falls back to the digest alone.
	if cleaned == "" || !isAlphanumeric(rune(cleaned[0])) {
		return "p" + suffix
	}
	return cleaned + "-" + suffix
}

func isAlphanumeric(character rune) bool {
	return (character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9')
}

// Open records a project folder and returns its identity.
//
// The path is resolved to a realpath before anything is derived from it: on
// macOS /tmp is a symlink to /private/tmp, and an un-normalized path would
// produce a second identifier for a project that is already open.
func (s *ProjectStore) Open(ctx context.Context, projectPath string) (Project, error) {
	trimmed := strings.TrimSpace(projectPath)
	if err := assertProjectPath(trimmed); err != nil {
		return Project{}, err
	}
	resolved, err := filepath.EvalSymlinks(trimmed)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Project{}, newError(CodeNotFound, "the project folder does not exist")
		}
		return Project{}, newError(CodeUnsafePath, "the project folder could not be resolved").withCause(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Project{}, newError(CodeNotFound, "the project folder could not be read").withCause(err)
	}
	if !info.IsDir() {
		return Project{}, policyError("path", "the project path is not a directory")
	}
	if err := assertProjectPath(resolved); err != nil {
		return Project{}, err
	}

	project := Project{
		ID:   ProjectIDFor(resolved),
		Name: filepath.Base(resolved),
		Path: resolved,
	}
	location, err := projectRecordPath(project.ID)
	if err != nil {
		return Project{}, err
	}
	encoded, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return Project{}, newError(CodeStorageFailed, "the project could not be encoded").withCause(err)
	}
	if err := s.scope.AtomicWrite(ctx, location, append(encoded, '\n')); err != nil {
		return Project{}, translateStorageError(err, "the project could not be recorded")
	}
	return project, nil
}

// Get returns a previously opened project.
func (s *ProjectStore) Get(ctx context.Context, projectID string) (Project, error) {
	location, err := projectRecordPath(projectID)
	if err != nil {
		return Project{}, err
	}
	contents, err := s.scope.ReadFile(ctx, location)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A distinct code, because the client's recovery is specific: reopen
			// the folder by the path it still holds and retry once.
			return Project{}, newError(CodeProjectNotOpen, "the project is not open")
		}
		return Project{}, translateStorageError(err, "the project could not be read")
	}
	if int64(len(contents)) > MaxProjectBytes {
		return Project{}, newError(CodeCorruptDocument, "the project document is over the size limit")
	}
	var project Project
	if err := json.Unmarshal(contents, &project); err != nil {
		return Project{}, newError(CodeCorruptDocument, "the project document is not valid JSON").withCause(err)
	}
	if project.ID != projectID {
		// A record that disagrees with its location would let one project be
		// reachable under another's identity.
		return Project{}, newError(CodeCorruptDocument, "the project identity does not match its location")
	}
	if err := assertProjectPath(project.Path); err != nil {
		return Project{}, err
	}
	return project, nil
}
