package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

const (
	// SchemaVersion is the workspace envelope version written by this build.
	SchemaVersion uint64 = 1

	defaultMaxStateBytes int64 = 1 << 20
	defaultMaxDocuments  int   = 4096

	// scopeHeadroom leaves room for the envelope fields around the state
	// payload so a state at exactly MaxStateBytes still fits a scoped write.
	scopeHeadroom int64 = 64 << 10
)

// Options bounds the store. Zero values select conservative defaults.
type Options struct {
	// MaxStateBytes bounds one workspace state payload.
	MaxStateBytes int64
	// MaxDocuments bounds how many distinct workspaces may be created. Existing
	// documents remain writable once the bound is reached.
	MaxDocuments int
	// Now supplies timestamps; tests inject a deterministic clock.
	Now func() time.Time
}

// Store is a revisioned, atomic document store for Canvas workspace state.
//
// State is opaque JSON. Every write is an optimistic compare-and-swap against
// the caller's expected revision, so a stale browser tab cannot silently
// overwrite a newer save.
type Store struct {
	scope         *runfs.Scope
	locks         *keyLocks
	maxStateBytes int64
	maxDocuments  int
	now           func() time.Time
}

// Entry catalogs one stored workspace for enumeration and legacy import.
type Entry struct {
	Session   contracts.SessionIdentity `json:"session"`
	Revision  uint64                    `json:"revision"`
	UpdatedAt time.Time                 `json:"updatedAt"`
	Bytes     int64                     `json:"bytes"`
}

type indexDocument struct {
	SchemaVersion uint64           `json:"schemaVersion"`
	Entries       map[string]Entry `json:"entries"`
}

// Root returns the canonical directory backing this store. It is used for
// user-visible diagnostics only.
func (s *Store) Root() string { return s.scope.Root() }

// Open prepares a store beneath root, creating the directory when absent.
// Callers pass filepath.Join(settings.Home(), "canvas").
func Open(ctx context.Context, root string, options Options) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: empty root", runfs.ErrInvalidPath)
	}
	maxStateBytes := options.MaxStateBytes
	if maxStateBytes == 0 {
		maxStateBytes = defaultMaxStateBytes
	}
	maxDocuments := options.MaxDocuments
	if maxDocuments == 0 {
		maxDocuments = defaultMaxDocuments
	}
	if maxStateBytes < 1 || maxDocuments < 1 {
		return nil, fmt.Errorf("persistence: invalid store options")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	if err := ensureRoot(root); err != nil {
		return nil, err
	}
	scope, err := runfs.OpenScope(root, runfs.ScopeOptions{
		MaxReadBytes:  maxStateBytes + scopeHeadroom,
		MaxWriteBytes: maxStateBytes + scopeHeadroom,
	})
	if err != nil {
		return nil, err
	}
	store := &Store{
		scope:         scope,
		locks:         newKeyLocks(),
		maxStateBytes: maxStateBytes,
		maxDocuments:  maxDocuments,
		now:           now,
	}
	if err := scope.MkdirAll(ctx, documentDirectory); err != nil {
		scope.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the store's filesystem handle.
func (s *Store) Close() error { return s.scope.Close() }

// Load returns the stored workspace for a session.
//
// A session that has never been saved returns revision 0 with a null state and
// no error, so a first-time client does not have to special-case absence. A
// document that exists but cannot be decoded returns ErrCorrupt; the client
// recovers by saving with ExpectedRevision 0.
func (s *Store) Load(ctx context.Context, session contracts.SessionIdentity) (contracts.WorkspaceDocument, error) {
	if err := ValidateSession(session); err != nil {
		return contracts.WorkspaceDocument{}, err
	}
	name := documentName(session)
	release, err := s.locks.acquire(ctx, name)
	if err != nil {
		return contracts.WorkspaceDocument{}, err
	}
	defer release()
	return s.read(ctx, session, name)
}

func (s *Store) read(ctx context.Context, session contracts.SessionIdentity, name string) (contracts.WorkspaceDocument, error) {
	raw, err := s.scope.ReadFile(ctx, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return contracts.WorkspaceDocument{
			SchemaVersion: SchemaVersion,
			Revision:      0,
			Session:       session,
			State:         json.RawMessage("null"),
		}, nil
	case errors.Is(err, runfs.ErrTooLarge):
		return contracts.WorkspaceDocument{}, &CorruptionError{Reason: "document exceeds the configured size limit"}
	case err != nil:
		return contracts.WorkspaceDocument{}, err
	}

	var document contracts.WorkspaceDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return contracts.WorkspaceDocument{}, &CorruptionError{Reason: "document is not a valid workspace envelope"}
	}
	if decoder.More() {
		return contracts.WorkspaceDocument{}, &CorruptionError{Reason: "document has trailing content"}
	}
	if document.SchemaVersion != SchemaVersion {
		return contracts.WorkspaceDocument{}, fmt.Errorf("%w: document schema %d", ErrSchemaUnsupported, document.SchemaVersion)
	}
	if document.Revision == 0 {
		return contracts.WorkspaceDocument{}, &CorruptionError{Reason: "stored document has revision 0"}
	}
	if document.Session != session {
		return contracts.WorkspaceDocument{}, &CorruptionError{Reason: "document identity does not match its location"}
	}
	if !json.Valid(document.State) {
		return contracts.WorkspaceDocument{}, &CorruptionError{Reason: "document state is not valid JSON"}
	}
	return document, nil
}

// Save applies an optimistic write and returns the stored document.
//
// ExpectedRevision must equal the current revision. Zero means "create, or
// replace a document that could not be decoded", which is the only recovery
// path out of corruption.
func (s *Store) Save(ctx context.Context, session contracts.SessionIdentity, write contracts.WorkspaceWrite) (contracts.WorkspaceDocument, error) {
	if err := ValidateSession(session); err != nil {
		return contracts.WorkspaceDocument{}, err
	}
	if write.SchemaVersion != SchemaVersion {
		return contracts.WorkspaceDocument{}, fmt.Errorf("%w: requested schema %d", ErrSchemaUnsupported, write.SchemaVersion)
	}
	state, err := canonicalState(write.State, s.maxStateBytes)
	if err != nil {
		return contracts.WorkspaceDocument{}, err
	}

	name := documentName(session)
	release, err := s.locks.acquire(ctx, name)
	if err != nil {
		return contracts.WorkspaceDocument{}, err
	}
	defer release()

	current, readErr := s.read(ctx, session, name)
	switch {
	case readErr == nil:
		if write.ExpectedRevision != current.Revision {
			return contracts.WorkspaceDocument{}, &ConflictError{Expected: write.ExpectedRevision, Actual: current.Revision}
		}
	case errors.Is(readErr, ErrCorrupt), errors.Is(readErr, ErrSchemaUnsupported):
		// A document we cannot decode has no usable revision to compare
		// against. Only an explicit revision-0 write may replace it.
		if write.ExpectedRevision != 0 {
			return contracts.WorkspaceDocument{}, readErr
		}
		current = contracts.WorkspaceDocument{Revision: 0}
	default:
		return contracts.WorkspaceDocument{}, readErr
	}

	document := contracts.WorkspaceDocument{
		SchemaVersion: SchemaVersion,
		Revision:      current.Revision + 1,
		Session:       session,
		State:         state,
		UpdatedAt:     s.now().UTC().Truncate(time.Millisecond),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return contracts.WorkspaceDocument{}, err
	}

	// Catalog the intent before the effect. An index entry without its document
	// is a harmless stale catalog row; a document missing from the index would
	// silently escape the count bound.
	entry := Entry{
		Session:   session,
		Revision:  document.Revision,
		UpdatedAt: document.UpdatedAt,
		Bytes:     int64(len(state)),
	}
	if err := s.updateIndex(ctx, name, entry, current.Revision == 0); err != nil {
		return contracts.WorkspaceDocument{}, err
	}
	if err := s.scope.AtomicWrite(ctx, name, encoded); err != nil {
		if errors.Is(err, runfs.ErrTooLarge) {
			return contracts.WorkspaceDocument{}, ErrStateTooLarge
		}
		return contracts.WorkspaceDocument{}, err
	}
	return document, nil
}

// List returns every catalogued workspace ordered by identity. It is the
// enumeration entry point for the legacy import task.
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	release, err := s.locks.acquire(ctx, indexName)
	if err != nil {
		return nil, err
	}
	defer release()
	index, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(index.Entries))
	for _, entry := range index.Entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].Session.Agent != entries[b].Session.Agent {
			return entries[a].Session.Agent < entries[b].Session.Agent
		}
		return entries[a].Session.ID < entries[b].Session.ID
	})
	return entries, nil
}

func (s *Store) updateIndex(ctx context.Context, name string, entry Entry, creating bool) error {
	release, err := s.locks.acquire(ctx, indexName)
	if err != nil {
		return err
	}
	defer release()

	index, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	if _, known := index.Entries[name]; creating && !known && len(index.Entries) >= s.maxDocuments {
		return ErrQuotaExceeded
	}
	index.Entries[name] = entry
	encoded, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if err := s.scope.AtomicWrite(ctx, indexName, encoded); err != nil {
		if errors.Is(err, runfs.ErrTooLarge) {
			return ErrQuotaExceeded
		}
		return err
	}
	return nil
}

// readIndex returns the catalog, treating an unreadable catalog as empty.
//
// The index is derived data: documents remain the source of truth, so a corrupt
// catalog must not make saved workspaces unreadable. It is rebuilt from the next
// write onward.
func (s *Store) readIndex(ctx context.Context) (indexDocument, error) {
	empty := indexDocument{SchemaVersion: SchemaVersion, Entries: map[string]Entry{}}
	raw, err := s.scope.ReadFile(ctx, indexName)
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, runfs.ErrTooLarge):
		return empty, nil
	case err != nil:
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return indexDocument{}, ctxErr
		}
		return indexDocument{}, err
	}
	var index indexDocument
	if err := json.Unmarshal(raw, &index); err != nil || index.SchemaVersion != SchemaVersion {
		return empty, nil
	}
	if index.Entries == nil {
		index.Entries = map[string]Entry{}
	}
	return index, nil
}

// canonicalState validates and compacts a state payload.
//
// The bound applies to canonical bytes, so pretty-printed and minified state
// are accepted identically. A separate, looser bound on the raw input keeps an
// absurd buffer from being compacted at all.
func canonicalState(state json.RawMessage, limit int64) (json.RawMessage, error) {
	if len(state) == 0 {
		return nil, fmt.Errorf("%w: state is empty", ErrInvalidState)
	}
	if int64(len(state)) > limit+scopeHeadroom {
		return nil, ErrStateTooLarge
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, state); err != nil {
		return nil, fmt.Errorf("%w: state is not valid JSON", ErrInvalidState)
	}
	if int64(compacted.Len()) > limit {
		return nil, ErrStateTooLarge
	}
	return json.RawMessage(compacted.Bytes()), nil
}

func ensureRoot(root string) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return mkdirAllPrivate(absolute)
}
