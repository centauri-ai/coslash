package atlas

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// DocumentSchemaVersion is the storage envelope carrying a board graph. It is
// versioned separately from the graph, because a board can be exported and
// re-imported without its document metadata.
const DocumentSchemaVersion uint64 = 1

// MaxBoardBytes bounds one board document. A board is configuration, not
// content; anything larger is a sign of an injected payload rather than a graph
// someone drew.
const MaxBoardBytes int64 = 2 << 20

// BoardsDirectory is the project-relative location of Atlas boards. Boards live
// with the project so they can be committed alongside it.
const BoardsDirectory = ".coslash/atlas/boards"

// BoardDocument is one stored board: the revision envelope plus the graph.
//
// The member names match the frozen contracts.BoardDocument envelope so the
// legacy client shapes for board CRUD keep working. ProjectID is additive and
// records which project the board belongs to, so a file copied from elsewhere
// is refused rather than silently adopted.
type BoardDocument struct {
	SchemaVersion uint64    `json:"schemaVersion"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ProjectID     string    `json:"projectId,omitempty"`
	Revision      uint64    `json:"revision"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Board         *Board    `json:"board"`

	extra map[string]json.RawMessage
}

// BoardSummary is a document without its graph, for listing.
type BoardSummary struct {
	SchemaVersion uint64    `json:"schemaVersion"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ProjectID     string    `json:"projectId,omitempty"`
	Revision      uint64    `json:"revision"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Summary drops the graph from a document.
func (d *BoardDocument) Summary() BoardSummary {
	return BoardSummary{
		SchemaVersion: d.SchemaVersion,
		ID:            d.ID,
		Name:          d.Name,
		ProjectID:     d.ProjectID,
		Revision:      d.Revision,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
}

// UnmarshalJSON decodes the envelope and runs the graph through the migration
// boundary, so a stored v1 board becomes a current graph at the moment it is
// read and never reaches the rest of the package in its legacy shape.
func (d *BoardDocument) UnmarshalJSON(data []byte) error {
	type envelope struct {
		SchemaVersion uint64          `json:"schemaVersion"`
		ID            string          `json:"id"`
		Name          string          `json:"name"`
		ProjectID     string          `json:"projectId"`
		Revision      uint64          `json:"revision"`
		CreatedAt     time.Time       `json:"createdAt"`
		UpdatedAt     time.Time       `json:"updatedAt"`
		Board         json.RawMessage `json:"board"`
	}
	var shadow envelope
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*d = BoardDocument{
		SchemaVersion: shadow.SchemaVersion,
		ID:            shadow.ID,
		Name:          shadow.Name,
		ProjectID:     shadow.ProjectID,
		Revision:      shadow.Revision,
		CreatedAt:     shadow.CreatedAt,
		UpdatedAt:     shadow.UpdatedAt,
		extra:         captureExtra(data, BoardDocument{}),
	}
	if len(shadow.Board) == 0 || string(shadow.Board) == "null" {
		return newError(CodeCorruptDocument, "the board document carries no board")
	}
	board, err := DecodeBoard(shadow.Board)
	if err != nil {
		return err
	}
	d.Board = board
	return nil
}

func (d BoardDocument) MarshalJSON() ([]byte, error) {
	type alias BoardDocument
	encoded, err := json.Marshal(alias(d))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, d.extra)
}

// BoardStore persists one project's boards with atomic, revision-checked writes.
//
// The store never resolves a path from a document field: the board identifier
// is a validated single path component and nothing else reaches the filesystem.
// Its scope is rooted at the project directory, so every read and write is
// contained there and refuses traversal and symlinked components.
type BoardStore struct {
	scope     *runfs.Scope
	projectID string
	now       func() time.Time
}

// NewBoardStore binds a store to a scope rooted at the project directory.
func NewBoardStore(scope *runfs.Scope, projectID string, now func() time.Time) (*BoardStore, error) {
	if scope == nil {
		return nil, newError(CodeStorageFailed, "a board store requires a scope")
	}
	if !ValidProjectID(projectID) {
		return nil, &Error{Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId"}
	}
	if now == nil {
		now = time.Now
	}
	return &BoardStore{scope: scope, projectID: projectID, now: now}, nil
}

// ProjectID reports the project this store is bound to.
func (s *BoardStore) ProjectID() string { return s.projectID }

func boardLocation(boardID string) (string, error) {
	if !ValidBoardID(boardID) {
		return "", &Error{Code: CodeInvalidBoardID, Message: "the board identifier is not valid", Field: "id"}
	}
	return path.Join(BoardsDirectory, boardID+".json"), nil
}

// Load reads, migrates, normalizes, and validates one board.
//
// Policy is asserted on read as well as on write, because a board file can
// change on disk between the two and the read is the call that precedes a run.
func (s *BoardStore) Load(ctx context.Context, boardID string) (*BoardDocument, error) {
	document, err := s.loadDocument(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if err := AssertPolicy(document.Board); err != nil {
		return nil, err
	}
	return document, nil
}

// loadDocument reads a board without asserting policy, so a stored document
// that has since become illegal can still be counted for a revision check
// rather than blocking every future write to that identifier.
func (s *BoardStore) loadDocument(ctx context.Context, boardID string) (*BoardDocument, error) {
	location, err := boardLocation(boardID)
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

	var document BoardDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		var typed *Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		return nil, newError(CodeCorruptDocument, "the board document is not valid JSON").
			withDetail(err.Error()).withCause(err)
	}
	if document.SchemaVersion != DocumentSchemaVersion {
		return nil, &Error{
			Code:    CodeSchemaVersion,
			Message: "the board document uses an unsupported storage schema",
			Field:   "schemaVersion",
		}
	}
	// A document whose contents disagree with its location would let one board
	// be reachable under another's identity, and one copied in from another
	// project would inherit this project's runs.
	if document.ID != boardID {
		return nil, newError(CodeCorruptDocument, "the board identity does not match its location")
	}
	if document.ProjectID != "" && document.ProjectID != s.projectID {
		return nil, newError(CodeCorruptDocument, "the board belongs to a different project")
	}
	// Older board envelopes predate projectId. Their location inside this
	// project is authoritative; adopt that ownership in memory so they can be
	// run immediately and persist it on the next save.
	if document.ProjectID == "" {
		document.ProjectID = s.projectID
	}
	if document.Revision < 1 {
		return nil, newError(CodeCorruptDocument, "the board document has no revision")
	}
	return &document, nil
}

// Save writes a board with an optimistic revision check.
//
// expectedRevision is the revision the caller believes it edited. Zero means
// create: the write succeeds only when no board exists yet. A mismatch returns
// REVISION_CONFLICT carrying the actual revision so a client can rebase without
// a second round trip, and the stored document is left untouched.
func (s *BoardStore) Save(ctx context.Context, document *BoardDocument, expectedRevision uint64) (*BoardDocument, error) {
	if document == nil || document.Board == nil {
		return nil, policyError("board", "the board is missing")
	}
	name := strings.TrimSpace(document.Name)
	if name == "" {
		return nil, policyError("name", "a board name is required")
	}
	if len(name) > 200 {
		return nil, policyError("name", "a board name may hold at most 200 characters")
	}
	location, err := boardLocation(document.ID)
	if err != nil {
		return nil, err
	}
	unlock := atlasWriteLocks.lock("board:" + s.scope.Root() + "/" + s.projectID + "/" + document.ID)
	defer unlock()

	saved := *document
	saved.Name = name
	saved.ProjectID = s.projectID
	board := *document.Board
	Normalize(&board)
	if err := AssertPolicy(&board); err != nil {
		return nil, err
	}
	saved.Board = &board
	saved.extra = cloneExtra(document.extra)

	existing, err := s.readRevision(ctx, location, document.ID)
	if err != nil {
		return nil, err
	}
	switch {
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
		if existing.ProjectID != "" && existing.ProjectID != s.projectID {
			return nil, newError(CodeCorruptDocument, "the board belongs to a different project")
		}
		saved.CreatedAt = existing.CreatedAt
	}

	saved.SchemaVersion = DocumentSchemaVersion
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

// revisionOnly is the minimum needed for an optimistic check. It deliberately
// avoids the graph, so a stored board this build cannot decode still reports a
// revision instead of wedging every future write.
type revisionOnly struct {
	SchemaVersion uint64    `json:"schemaVersion"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"projectId"`
	Revision      uint64    `json:"revision"`
	CreatedAt     time.Time `json:"createdAt"`
}

func (s *BoardStore) readRevision(ctx context.Context, location, boardID string) (*revisionOnly, error) {
	contents, err := s.scope.ReadFile(ctx, location)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, translateStorageError(err, "the board could not be read")
	}
	var existing revisionOnly
	if err := json.Unmarshal(contents, &existing); err != nil {
		return nil, newError(CodeCorruptDocument, "the stored board is not valid JSON").
			withDetail(err.Error()).withCause(err)
	}
	if existing.ID != boardID {
		return nil, newError(CodeCorruptDocument, "the board identity does not match its location")
	}
	if existing.SchemaVersion != DocumentSchemaVersion || existing.Revision == 0 || existing.CreatedAt.IsZero() {
		return nil, newError(CodeCorruptDocument, "the stored board revision envelope is invalid")
	}
	if existing.ProjectID != "" && existing.ProjectID != s.projectID {
		return nil, newError(CodeCorruptDocument, "the board belongs to a different project")
	}
	return &existing, nil
}

// Delete removes a board after an optimistic revision check.
func (s *BoardStore) Delete(ctx context.Context, boardID string, expectedRevision uint64) error {
	location, err := boardLocation(boardID)
	if err != nil {
		return err
	}
	unlock := atlasWriteLocks.lock("board:" + s.scope.Root() + "/" + s.projectID + "/" + boardID)
	defer unlock()
	existing, err := s.readRevision(ctx, location, boardID)
	if err != nil {
		return err
	}
	if existing == nil {
		return newError(CodeNotFound, "the board was not found")
	}
	if existing.Revision != expectedRevision {
		return (&Error{
			Code: CodeRevisionConflict, Message: "the board changed since it was loaded", Field: "revision",
		}).withActualRevision(existing.Revision)
	}
	// Resolve refuses traversal and every symlinked component below the root and
	// returns the canonical location, so the removal below cannot escape the
	// project. The scope intentionally exposes no deletion API of its own.
	resolved, err := s.scope.Resolve(location)
	if err != nil {
		return translateStorageError(err, "the board could not be removed")
	}
	if err := os.Remove(resolved); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return newError(CodeNotFound, "the board was not found")
		}
		return translateStorageError(err, "the board could not be removed")
	}
	syncDirectoryOf(resolved)
	return nil
}

// List returns every readable board summary, sorted newest first, alongside the
// identifiers that could not be read.
//
// A board that fails to parse or violates policy is reported through the second
// return value rather than failing the listing: one bad file must not make a
// project's other boards unreachable.
func (s *BoardStore) List(ctx context.Context) ([]BoardSummary, []string, error) {
	directory, err := s.scope.Resolve(BoardsDirectory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []BoardSummary{}, []string{}, nil
		}
		return nil, nil, translateStorageError(err, "the project boards could not be listed")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []BoardSummary{}, []string{}, nil
		}
		return nil, nil, translateStorageError(err, "the project boards could not be listed")
	}

	readable := make([]BoardSummary, 0, len(entries))
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
		document, err := s.Load(ctx, boardID)
		if err != nil {
			unreadable = append(unreadable, boardID)
			continue
		}
		readable = append(readable, document.Summary())
	}
	sort.Slice(readable, func(a, b int) bool {
		if readable[a].UpdatedAt.Equal(readable[b].UpdatedAt) {
			return readable[a].ID < readable[b].ID
		}
		return readable[a].UpdatedAt.After(readable[b].UpdatedAt)
	})
	sort.Strings(unreadable)
	return readable, unreadable, nil
}

// syncDirectoryOf makes a removal durable. A failure here is not reported: the
// entry is already gone from the caller's view, and a crash before the metadata
// reaches disk resurrects a board rather than losing one.
func syncDirectoryOf(file string) {
	directory, err := os.Open(path.Dir(file))
	if err != nil {
		return
	}
	defer directory.Close()
	_ = directory.Sync()
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
	case errors.Is(err, runfs.ErrLogFull):
		return newError(CodeLogFull, "the run log is full").withCause(err)
	case errors.Is(err, runfs.ErrCorruptLog):
		return newError(CodeCorruptDocument, "the run log is corrupt").withCause(err).withDetail(err.Error())
	default:
		return newError(CodeStorageFailed, message).withDetail(err.Error()).withCause(err)
	}
}
