package dagama

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// MaxBoardBytes bounds one board document. A board is configuration, not
// content; anything larger is a sign of an injected payload rather than a
// pipeline someone configured.
const MaxBoardBytes int64 = 256 << 10

// BoardStore persists boards with atomic, revision-checked writes.
//
// Boards live under a project-owned directory so they can be committed with the
// project. The store never resolves a path from a board field: the project and
// board identifiers are validated path components and nothing else reaches the
// filesystem.
type BoardStore struct {
	scope   *runfs.Scope
	now     func() time.Time
	writers sync.Map
}

func (s *BoardStore) lockBoard(location string) func() {
	value, _ := s.writers.LoadOrStore(location, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// NewBoardStore binds a store to a scope rooted at the boards root.
func NewBoardStore(scope *runfs.Scope, now func() time.Time) (*BoardStore, error) {
	if scope == nil {
		return nil, newError(CodeStorageFailed, "a board store requires a scope")
	}
	if now == nil {
		now = time.Now
	}
	return &BoardStore{scope: scope, now: now}, nil
}

func boardPath(projectID, boardID string) (string, error) {
	if !ValidProjectID(projectID) {
		return "", &Error{Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId"}
	}
	if !ValidBoardID(boardID) {
		return "", &Error{Code: CodeInvalidBoardID, Message: "the board identifier is not valid", Field: "id"}
	}
	return path.Join(projectID, "boards", boardID+".json"), nil
}

// Load reads and validates one board.
//
// Policy is asserted on read as well as on write, because a board file can
// change on disk between the two and the read is the call that precedes a run.
func (s *BoardStore) Load(ctx context.Context, projectID, boardID string) (*Board, error) {
	location, err := boardPath(projectID, boardID)
	if err != nil {
		return nil, err
	}
	contents, err := s.scope.ReadFile(ctx, location)
	if err != nil {
		return nil, translateStorageError(err, "the board could not be read")
	}
	if int64(len(contents)) > MaxBoardBytes {
		return nil, newError(CodeCorruptDocument, "the board document is over the size limit")
	}

	var board Board
	if err := json.Unmarshal(contents, &board); err != nil {
		return nil, newError(CodeCorruptDocument, "the board document is not valid JSON").
			withDetail(err.Error()).withCause(err)
	}
	Normalize(&board)
	if err := AssertPolicy(&board); err != nil {
		return nil, err
	}
	if board.ID != boardID || board.ProjectID != projectID {
		// A board whose contents disagree with its location would let one board
		// be reachable under another's identity.
		return nil, newError(CodeCorruptDocument, "the board identity does not match its location")
	}
	return &board, nil
}

// Save writes a board with an optimistic revision check.
//
// expectedRevision is the revision the caller believes it edited. Zero means
// "create": the write succeeds only when no board exists yet. A mismatch returns
// a REVISION_CONFLICT carrying the actual revision so a client can rebase
// without a second round trip, and the stored document is left untouched.
func (s *BoardStore) Save(ctx context.Context, board *Board, expectedRevision uint64) (*Board, error) {
	if board == nil {
		return nil, policyError("board", "the board is missing")
	}
	saved := *board
	Normalize(&saved)
	if err := AssertPolicy(&saved); err != nil {
		return nil, err
	}
	location, err := boardPath(saved.ProjectID, saved.ID)
	if err != nil {
		return nil, err
	}
	unlock := s.lockBoard(location)
	defer unlock()

	existing, loadErr := s.loadRaw(ctx, location)
	switch {
	case loadErr != nil:
		return nil, loadErr
	case existing == nil:
		if expectedRevision != 0 {
			return nil, (&Error{
				Code: CodeRevisionConflict, Message: "the board no longer exists", Field: "revision",
			}).withActualRevision(0)
		}
		saved.CreatedAt = s.now().UTC()
	default:
		if existing.Revision != expectedRevision {
			return nil, (&Error{
				Code:    CodeRevisionConflict,
				Message: "the board changed since it was loaded",
				Field:   "revision",
			}).withActualRevision(existing.Revision)
		}
		saved.CreatedAt = existing.CreatedAt
	}

	saved.SchemaVersion = BoardSchemaVersion
	saved.Revision = expectedRevision + 1
	saved.UpdatedAt = s.now().UTC()

	encoded, err := json.MarshalIndent(&saved, "", "  ")
	if err != nil {
		return nil, newError(CodeStorageFailed, "the board could not be encoded").
			withDetail(err.Error()).withCause(err)
	}
	encoded = append(encoded, '\n')
	if int64(len(encoded)) > MaxBoardBytes {
		return nil, newError(CodeCorruptDocument, "the board document is over the size limit")
	}
	if err := s.scope.AtomicWrite(ctx, location, encoded); err != nil {
		return nil, translateStorageError(err, "the board could not be written")
	}
	return &saved, nil
}

// loadRaw reads a board without asserting policy, so a stored document that has
// since become illegal can still be counted for the revision check rather than
// blocking every future write.
func (s *BoardStore) loadRaw(ctx context.Context, location string) (*Board, error) {
	contents, err := s.scope.ReadFile(ctx, location)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, translateStorageError(err, "the board could not be read")
	}
	var board Board
	if err := json.Unmarshal(contents, &board); err != nil {
		return nil, newError(CodeCorruptDocument, "the stored board is not valid JSON").
			withDetail(err.Error()).withCause(err)
	}
	return &board, nil
}

// List returns every readable board identifier for a project, sorted.
//
// A board that fails to parse or violates policy is reported through the second
// return value rather than failing the listing: one bad file must not make a
// project's other boards unreachable.
func (s *BoardStore) List(ctx context.Context, projectID string) ([]string, []string, error) {
	if !ValidProjectID(projectID) {
		return nil, nil, &Error{
			Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId",
		}
	}
	// Resolve refuses traversal and every symlinked component below the root and
	// returns the canonical location, so the directory read below cannot escape
	// the scope. Each entry is still loaded back through the scope.
	directory, err := s.scope.Resolve(path.Join(projectID, "boards"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, []string{}, nil
		}
		return nil, nil, translateStorageError(err, "the project boards could not be listed")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, []string{}, nil
		}
		return nil, nil, translateStorageError(err, "the project boards could not be listed")
	}

	readable := make([]string, 0, len(entries))
	unreadable := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		boardID := strings.TrimSuffix(entry.Name(), ".json")
		if !ValidBoardID(boardID) {
			unreadable = append(unreadable, entry.Name())
			continue
		}
		if _, err := s.Load(ctx, projectID, boardID); err != nil {
			unreadable = append(unreadable, boardID)
			continue
		}
		readable = append(readable, boardID)
	}
	sort.Strings(readable)
	sort.Strings(unreadable)
	return readable, unreadable, nil
}

func translateStorageError(err error, message string) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return newError(CodeNotFound, "the document was not found").withCause(err)
	case errors.Is(err, runfs.ErrSymlink), errors.Is(err, runfs.ErrNotRegular):
		return newError(CodeUnsafePath, "the storage path is unsafe").withCause(err)
	case errors.Is(err, runfs.ErrTooLarge):
		return newError(CodeCorruptDocument, "the document is over the size limit").withCause(err)
	case errors.Is(err, runfs.ErrInvalidPath):
		return newError(CodeUnsafePath, "the storage path is not valid").withCause(err)
	default:
		return newError(CodeStorageFailed, message).withDetail(err.Error()).withCause(err)
	}
}

// Delete removes a board with the same optimistic revision check Save applies.
//
// The revision is required, not optional: a stale tab that still shows an older
// board must not be able to delete work it never saw. The stored document is
// read through the scope and the location is re-resolved before removal, so the
// path that is unlinked is the one the scope already accepted.
func (s *BoardStore) Delete(ctx context.Context, projectID, boardID string, expectedRevision uint64) error {
	location, err := boardPath(projectID, boardID)
	if err != nil {
		return err
	}
	existing, err := s.loadRaw(ctx, location)
	if err != nil {
		return err
	}
	if existing == nil {
		return newError(CodeNotFound, "the board was not found")
	}
	if existing.Revision != expectedRevision {
		return (&Error{
			Code:    CodeRevisionConflict,
			Message: "the board changed since it was loaded",
			Field:   "revision",
		}).withActualRevision(existing.Revision)
	}
	resolved, err := s.scope.Resolve(location)
	if err != nil {
		return translateStorageError(err, "the board could not be removed")
	}
	if err := os.Remove(resolved); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return translateStorageError(err, "the board could not be removed")
	}
	return nil
}
