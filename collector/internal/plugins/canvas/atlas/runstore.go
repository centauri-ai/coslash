package atlas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Durable layout for one run. events.jsonl IS the run; run.json is a
// materialized view, rebuildable from the log at any time.
const (
	runEventsName     = "events.jsonl"
	runViewName       = "run.json"
	boardSnapshotName = "board.snapshot.json"
	inputSnapshotName = "inputs.snapshot.json"
)

// RunStore persists runs as an append-only event log plus a materialized view.
//
// The collector is not the authority on run state — the disk is. Every append
// is fsynced before the side effect it authorizes, so a crash between an intent
// and its effect leaves the intent recorded and reconciliation can see it.
type RunStore struct {
	scope *runfs.Scope
	now   func() time.Time
	// writers serializes the read-validate-append cycle per run. The event log
	// already serializes the append itself, but validating a transition against
	// state another goroutine is about to change would let two mutually
	// exclusive events both pass their check. One collector owns a runs root, so
	// an in-process lock closes the realistic race.
}

// NewRunStore binds a store to a scope rooted at the private Atlas projects
// root, conventionally ~/.coslash/atlas/projects.
func NewRunStore(scope *runfs.Scope, now func() time.Time) (*RunStore, error) {
	if scope == nil {
		return nil, newError(CodeStorageFailed, "a run store requires a scope")
	}
	if now == nil {
		now = time.Now
	}
	return &RunStore{scope: scope, now: now}, nil
}

// NewRunID builds a run identifier from a timestamp and a random suffix. The
// caller supplies both, so the function stays deterministic under test.
//
// The identifier is lowercase hex and a lowercase separator, so it is already
// case-folded: macOS is case-insensitive, and a run directory differing from
// another only by case would collide on disk while looking distinct in the log.
func NewRunID(at time.Time, suffix string) (string, error) {
	candidate := "run-" + at.UTC().Format("20060102t150405") + "-" + suffix
	if !ValidRunID(candidate) {
		return "", &Error{Code: CodeInvalidRunID, Message: "the run identifier is not valid", Field: "runId"}
	}
	return candidate, nil
}

// runDirectory and rootPath are siblings under the project, so no relative path
// leads from an agent's working directory up into the control state.
func runDirectory(projectID, runID string) (string, error) {
	if !ValidProjectID(projectID) {
		return "", &Error{Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId"}
	}
	if !ValidRunID(runID) {
		return "", &Error{Code: CodeInvalidRunID, Message: "the run identifier is not valid", Field: "runId"}
	}
	return path.Join(projectID, RunsDirectory, runID), nil
}

// RunRootPath returns the disposable run root for a run. It is a sibling of the
// control state, never a child of it.
func (s *RunStore) RunRootPath(projectID, runID string) (string, error) {
	if !ValidProjectID(projectID) {
		return "", &Error{Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId"}
	}
	if !ValidRunID(runID) {
		return "", &Error{Code: CodeInvalidRunID, Message: "the run identifier is not valid", Field: "runId"}
	}
	resolved, err := s.scope.Resolve(path.Join(projectID, RootsDirectory, runID))
	if err != nil {
		return "", translateStorageError(err, "the run root could not be resolved")
	}
	return resolved, nil
}

func (s *RunStore) lockRun(projectID, runID string) func() {
	return atlasWriteLocks.lock("run:" + s.scope.Root() + "/" + projectID + "/" + runID)
}

func (s *RunStore) eventLog(projectID, runID string) (*runfs.EventLog, error) {
	directory, err := runDirectory(projectID, runID)
	if err != nil {
		return nil, err
	}
	log, err := runfs.NewEventLog(s.scope, path.Join(directory, runEventsName), runfs.EventLogOptions{
		MaxEventBytes: MaxEventBytes,
		MaxEvents:     MaxEventsPerRun,
		Now:           s.now,
	})
	if err != nil {
		return nil, translateStorageError(err, "the run log could not be opened")
	}
	return log, nil
}

// Allocate creates a run directory and writes its immutable snapshots.
//
// No event is appended here. The caller emits run_created once the snapshots
// are on disk, so a crash between the two leaves an empty directory rather than
// a run whose log references a snapshot that was never written.
func (s *RunStore) Allocate(ctx context.Context, projectID, runID string, board *BoardDocument, inputs any) error {
	directory, err := runDirectory(projectID, runID)
	if err != nil {
		return err
	}
	if board == nil {
		return newError(CodeInvalidState, "a run needs a board snapshot")
	}
	if board.ProjectID != projectID || !ValidBoardID(board.ID) || board.Revision == 0 || board.Board == nil {
		return newError(CodeInvalidState, "the board snapshot does not belong to this project")
	}
	if err := AssertPolicy(board.Board); err != nil {
		return err
	}
	unlock := s.lockRun(projectID, runID)
	defer unlock()
	// An existing log means this identifier already belongs to another run.
	// Reusing the directory would append one run's history onto another's.
	if _, err := s.scope.ReadFile(ctx, path.Join(directory, runEventsName)); err == nil {
		return newError(CodeInvalidState, "the run identifier is already in use")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return translateStorageError(err, "the run directory could not be inspected")
	}

	encodedBoard, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		return newError(CodeStorageFailed, "the board snapshot could not be encoded").
			withDetail(err.Error()).withCause(err)
	}
	encodedInputs, err := json.MarshalIndent(inputs, "", "  ")
	if err != nil {
		return newError(CodeStorageFailed, "the input snapshot could not be encoded").
			withDetail(err.Error()).withCause(err)
	}
	encodedBoard = append(encodedBoard, '\n')
	encodedInputs = append(encodedInputs, '\n')
	boardExists, err := s.snapshotMatches(ctx, path.Join(directory, boardSnapshotName), encodedBoard, "board")
	if err != nil {
		return err
	}
	inputsExist, err := s.snapshotMatches(ctx, path.Join(directory, inputSnapshotName), encodedInputs, "input")
	if err != nil {
		return err
	}
	if !boardExists {
		if err := s.scope.AtomicWrite(ctx, path.Join(directory, boardSnapshotName), encodedBoard); err != nil {
			return translateStorageError(err, "the board snapshot could not be written")
		}
	}
	if !inputsExist {
		if err := s.scope.AtomicWrite(ctx, path.Join(directory, inputSnapshotName), encodedInputs); err != nil {
			return translateStorageError(err, "the input snapshot could not be written")
		}
	}
	return nil
}

// snapshotMatches makes allocation idempotent without allowing a caller to
// replace another allocation's immutable inputs before run_created is emitted.
func (s *RunStore) snapshotMatches(ctx context.Context, location string, expected []byte, kind string) (bool, error) {
	contents, err := s.scope.ReadFile(ctx, location)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, translateStorageError(err, "the "+kind+" snapshot could not be inspected")
	}
	if !bytes.Equal(contents, expected) {
		return false, newError(CodeInvalidState, "the run identifier already has a different "+kind+" snapshot")
	}
	return true, nil
}

// BoardSnapshot returns the board a run was started against.
//
// A run reads its snapshot, never the live board: the board can be edited while
// the run is in flight, and a run that changed shape halfway through would make
// its own history unexplainable.
func (s *RunStore) BoardSnapshot(ctx context.Context, projectID, runID string) (*BoardDocument, error) {
	directory, err := runDirectory(projectID, runID)
	if err != nil {
		return nil, err
	}
	contents, err := s.scope.ReadFile(ctx, path.Join(directory, boardSnapshotName))
	if err != nil {
		return nil, translateStorageError(err, "the board snapshot could not be read")
	}
	var document BoardDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		var typed *Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		return nil, newError(CodeCorruptDocument, "the board snapshot is not valid JSON").
			withDetail(err.Error()).withCause(err)
	}
	if document.SchemaVersion != DocumentSchemaVersion || document.ProjectID != projectID ||
		!ValidBoardID(document.ID) || document.Revision == 0 || document.Board == nil {
		return nil, newError(CodeCorruptDocument, "the board snapshot does not belong to this run's project")
	}
	if err := AssertPolicy(document.Board); err != nil {
		return nil, err
	}
	return &document, nil
}

// Append records one event and returns the state it produces.
//
// The transition is validated against the current state first, so an undefined
// move is refused before it reaches the log and the previous state is never
// damaged by a rejected append. The log is written before the view, so a crash
// between them loses only a regenerable file — the other order would let
// run.json claim a fact the log cannot prove.
func (s *RunStore) Append(ctx context.Context, projectID, runID string, payload Payload) (*RunState, error) {
	if payload == nil {
		return nil, newError(CodeInvalidState, "an event payload is required")
	}
	if created, ok := payload.(*RunCreated); ok && created.ProjectID != projectID {
		return nil, newError(CodeInvalidState, "the run event belongs to a different project")
	}
	log, err := s.eventLog(projectID, runID)
	if err != nil {
		return nil, err
	}
	unlock := s.lockRun(projectID, runID)
	defer unlock()

	current, events, err := s.replay(ctx, log, runID)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(current, payload); err != nil {
		return nil, err
	}
	appended, err := log.Append(ctx, payload.EventType(), payload)
	if err != nil {
		return nil, translateStorageError(err, "the event could not be recorded")
	}
	events = append(events, appended)
	state, err := Reduce(runID, events)
	if err != nil {
		return nil, err
	}
	state.ProjectID = projectIDOr(state.ProjectID, projectID)
	if err := s.materialize(ctx, projectID, runID, state); err != nil {
		return nil, err
	}
	return state, nil
}

// Read returns the current state. The event log is always checked before the
// materialized view so a crash after appending an event but before rewriting
// run.json cannot leave a stale view visible forever.
func (s *RunStore) Read(ctx context.Context, projectID, runID string) (*RunState, error) {
	directory, err := runDirectory(projectID, runID)
	if err != nil {
		return nil, err
	}
	log, err := s.eventLog(projectID, runID)
	if err != nil {
		return nil, err
	}
	unlock := s.lockRun(projectID, runID)
	defer unlock()

	replayed, _, err := s.replay(ctx, log, runID)
	if err != nil {
		return nil, err
	}
	if replayed.CreatedAt == nil {
		return nil, newError(CodeNotFound, "the run was not found")
	}
	replayed.ProjectID = projectIDOr(replayed.ProjectID, projectID)

	contents, err := s.scope.ReadFile(ctx, path.Join(directory, runViewName))
	if err == nil {
		var view RunState
		if json.Unmarshal(contents, &view) == nil &&
			view.SchemaVersion == RunSchemaVersion && view.RunID == runID &&
			view.ProjectID == replayed.ProjectID && view.LastSeq == replayed.LastSeq &&
			sameRunState(&view, replayed) {
			return replayed, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, translateStorageError(err, "the run view could not be read")
	}
	if err := s.materialize(ctx, projectID, runID, replayed); err != nil {
		return nil, err
	}
	return replayed, nil
}

func sameRunState(left, right *RunState) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

// Rebuild replays from disk, ignoring the view, and rewrites it.
func (s *RunStore) Rebuild(ctx context.Context, projectID, runID string) (*RunState, error) {
	log, err := s.eventLog(projectID, runID)
	if err != nil {
		return nil, err
	}
	unlock := s.lockRun(projectID, runID)
	defer unlock()

	state, _, err := s.replay(ctx, log, runID)
	if err != nil {
		return nil, err
	}
	if state.CreatedAt == nil {
		return nil, newError(CodeNotFound, "the run was not found")
	}
	state.ProjectID = projectIDOr(state.ProjectID, projectID)
	if err := s.materialize(ctx, projectID, runID, state); err != nil {
		return nil, err
	}
	return state, nil
}

// Events returns the durable event log for one run.
func (s *RunStore) Events(ctx context.Context, projectID, runID string) ([]runfs.Event, error) {
	log, err := s.eventLog(projectID, runID)
	if err != nil {
		return nil, err
	}
	result, err := log.Read(ctx)
	if err != nil {
		return nil, translateStorageError(err, "the run log could not be read")
	}
	return result.Events, nil
}

// replay recovers the log — repairing only an unterminated tail — and reduces
// it. A torn tail is a crash between a write and its fsync, never a durable
// event, and it must be removed before the next append concatenates onto it.
func (s *RunStore) replay(ctx context.Context, log *runfs.EventLog, runID string) (*RunState, []runfs.Event, error) {
	result, err := log.Recover(ctx)
	if err != nil {
		return nil, nil, translateStorageError(err, "the run log could not be read")
	}
	state, err := Reduce(runID, result.Events)
	if err != nil {
		return nil, nil, err
	}
	return state, result.Events, nil
}

func (s *RunStore) materialize(ctx context.Context, projectID, runID string, state *RunState) error {
	directory, err := runDirectory(projectID, runID)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return newError(CodeStorageFailed, "the run view could not be encoded").
			withDetail(err.Error()).withCause(err)
	}
	if err := s.scope.AtomicWrite(ctx, path.Join(directory, runViewName), append(encoded, '\n')); err != nil {
		return translateStorageError(err, "the run view could not be written")
	}
	return nil
}

// List returns every readable run summary for a project, newest first, and the
// identifiers that could not be read.
//
// Run identifiers are timestamp-prefixed, so the ordering is also creation
// order for a run whose log is unreadable.
func (s *RunStore) List(ctx context.Context, projectID string) ([]RunSummary, []string, error) {
	if !ValidProjectID(projectID) {
		return nil, nil, &Error{
			Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId",
		}
	}
	// Resolve refuses traversal and every symlinked component below the root and
	// returns the canonical location, so the directory read cannot escape the
	// scope. Every entry is still read back through the scope.
	directory, err := s.scope.Resolve(path.Join(projectID, RunsDirectory))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []RunSummary{}, []string{}, nil
		}
		return nil, nil, translateStorageError(err, "the project runs could not be listed")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []RunSummary{}, []string{}, nil
		}
		return nil, nil, translateStorageError(err, "the project runs could not be listed")
	}

	readable := make([]RunSummary, 0, len(entries))
	unreadable := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !ValidRunID(entry.Name()) {
			continue
		}
		state, err := s.Read(ctx, projectID, entry.Name())
		if err != nil {
			unreadable = append(unreadable, entry.Name())
			continue
		}
		readable = append(readable, state.Summary())
	}
	sort.Slice(readable, func(a, b int) bool { return readable[a].RunID > readable[b].RunID })
	sort.Strings(unreadable)
	return readable, unreadable, nil
}

func projectIDOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
