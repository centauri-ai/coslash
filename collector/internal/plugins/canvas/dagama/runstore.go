package dagama

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Durable layout for one run. events.jsonl IS the run; run.json is a
// materialized view, rebuildable from the log at any time.
const (
	runEventsName = "events.jsonl"
	runViewName   = "run.json"
)

// RunStore persists runs as an append-only event log plus a materialized view.
//
// The collector is not the authority on run state — the disk is. Every append
// is fsynced before the side effect it authorizes, so a crash between intent
// and effect leaves the intent recorded and reconciliation can see it.
type RunStore struct {
	scope *runfs.Scope
	now   func() time.Time
	// writers serializes the read-validate-append cycle per run. The event log
	// already serializes the append itself, but validating a transition against
	// state that another goroutine is about to change would let two mutually
	// exclusive events both pass their check. One collector owns a runs root, so
	// an in-process lock closes the realistic race; a second collector pointed at
	// the same root would still need the controller to be the single writer.
	writers sync.Map
}

// NewRunStore binds a store to a scope rooted at the private runs root.
func NewRunStore(scope *runfs.Scope, now func() time.Time) (*RunStore, error) {
	if scope == nil {
		return nil, newError(CodeStorageFailed, "a run store requires a scope")
	}
	if now == nil {
		now = time.Now
	}
	return &RunStore{scope: scope, now: now}, nil
}

// lockRun serializes writers for one run and returns the release function.
func (s *RunStore) lockRun(projectID, runID string) func() {
	value, _ := s.writers.LoadOrStore(projectID+"/"+runID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func runDirectory(projectID, runID string) (string, error) {
	if !ValidProjectID(projectID) {
		return "", &Error{Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId"}
	}
	if !ValidRunID(runID) {
		return "", &Error{Code: CodeInvalidRunID, Message: "the run identifier is not valid", Field: "runId"}
	}
	return path.Join(projectID, "runs", runID), nil
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

// NewRunID builds a run identifier from a timestamp and random suffix. The
// caller supplies both so the function stays deterministic under test.
func NewRunID(at time.Time, suffix string) (string, error) {
	candidate := "run-" + at.UTC().Format("20060102t150405") + "-" + suffix
	if !ValidRunID(candidate) {
		return "", &Error{Code: CodeInvalidRunID, Message: "the run identifier is not valid", Field: "runId"}
	}
	return candidate, nil
}

// Append records one event and returns the state it produces.
//
// The transition is validated against the current state first, so an undefined
// move is refused before it reaches the log. Nothing is written on refusal, so
// the previous state is never damaged by a rejected append.
func (s *RunStore) Append(
	ctx context.Context,
	projectID, runID string,
	payload Payload,
) (*RunState, error) {
	if payload == nil {
		return nil, newError(CodeInvalidState, "an event payload is required")
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
		return nil, translateStorageError(err, "the run event could not be appended")
	}

	next, err := Reduce(runID, append(events, appended))
	if err != nil {
		return nil, err
	}
	// The view is written after the log, so a crash between the two leaves a
	// stale view that Read repairs by replaying. The log stays authoritative.
	if err := s.writeView(ctx, projectID, runID, next); err != nil {
		return nil, err
	}
	return next, nil
}

// Read returns the materialized view, replaying the log when the view is
// missing, unreadable, or behind.
func (s *RunStore) Read(ctx context.Context, projectID, runID string) (*RunState, error) {
	directory, err := runDirectory(projectID, runID)
	if err != nil {
		return nil, err
	}
	log, err := s.eventLog(projectID, runID)
	if err != nil {
		return nil, err
	}
	replayed, _, err := s.replay(ctx, log, runID)
	if err != nil {
		return nil, err
	}
	if replayed.LastSeq == 0 {
		return nil, newError(CodeNotFound, "the run was not found")
	}

	view, viewErr := s.readView(ctx, directory)
	if viewErr == nil && reflect.DeepEqual(view, replayed) {
		return view, nil
	}
	// Sequence equality is not sufficient: a copied or tampered view can carry
	// the right LastSeq with the wrong identity, status, gate, or title. The log
	// is authoritative, so only a complete materialized-state match is trusted.
	if err := s.writeView(ctx, projectID, runID, replayed); err != nil {
		return nil, err
	}
	return replayed, nil
}

// Replay rebuilds state from the log, ignoring the materialized view entirely.
// This is the audit path: it proves the view is reproducible.
func (s *RunStore) Replay(ctx context.Context, projectID, runID string) (*RunState, error) {
	log, err := s.eventLog(projectID, runID)
	if err != nil {
		return nil, err
	}
	state, _, err := s.replay(ctx, log, runID)
	return state, err
}

func (s *RunStore) replay(
	ctx context.Context,
	log *runfs.EventLog,
	runID string,
) (*RunState, []runfs.Event, error) {
	result, err := log.Read(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return emptyRunState(runID), nil, nil
		}
		return nil, nil, translateStorageError(err, "the run log could not be read")
	}
	state, err := Reduce(runID, result.Events)
	if err != nil {
		return nil, nil, err
	}
	return state, result.Events, nil
}

func (s *RunStore) readView(ctx context.Context, directory string) (*RunState, error) {
	contents, err := s.scope.ReadFile(ctx, path.Join(directory, runViewName))
	if err != nil {
		return nil, translateStorageError(err, "the run view could not be read")
	}
	var state RunState
	if err := json.Unmarshal(contents, &state); err != nil {
		return nil, newError(CodeCorruptDocument, "the run view is not valid JSON").
			withDetail(err.Error()).withCause(err)
	}
	return &state, nil
}

func (s *RunStore) writeView(ctx context.Context, projectID, runID string, state *RunState) error {
	directory, err := runDirectory(projectID, runID)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return newError(CodeStorageFailed, "the run view could not be encoded").
			withDetail(err.Error()).withCause(err)
	}
	encoded = append(encoded, '\n')
	if err := s.scope.AtomicWrite(ctx, path.Join(directory, runViewName), encoded); err != nil {
		return translateStorageError(err, "the run view could not be written")
	}
	return nil
}

// List returns run summaries for a project, newest identifier first.
func (s *RunStore) List(ctx context.Context, projectID string) ([]RunSummary, error) {
	if !ValidProjectID(projectID) {
		return nil, &Error{
			Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId",
		}
	}
	directory, err := s.scope.Resolve(path.Join(projectID, "runs"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []RunSummary{}, nil
		}
		return nil, translateStorageError(err, "the project runs could not be listed")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []RunSummary{}, nil
		}
		return nil, translateStorageError(err, "the project runs could not be listed")
	}

	summaries := make([]RunSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !ValidRunID(entry.Name()) {
			continue
		}
		state, err := s.Read(ctx, projectID, entry.Name())
		if err != nil {
			// One damaged run must not make a project's other runs unreachable.
			continue
		}
		summaries = append(summaries, state.Summary())
	}
	// Run identifiers embed a sortable timestamp, so lexical order is time order.
	sort.Slice(summaries, func(left, right int) bool {
		return summaries[left].RunID > summaries[right].RunID
	})
	return summaries, nil
}

// ---------------------------------------------------------------------------
// Transitions
// ---------------------------------------------------------------------------

// ValidateTransition refuses an event the current state cannot legally accept.
//
// The reducer stays total and applies whatever the log holds, because replay
// must agree with history. This is where ordering is actually enforced: a
// rejected append never reaches the log, so the log only ever contains
// transitions that were legal when they were written.
func ValidateTransition(state *RunState, payload Payload) error {
	if state == nil {
		return newError(CodeInvalidState, "the run state is missing")
	}
	value := reflect.ValueOf(payload)
	if !value.IsValid() || (value.Kind() == reflect.Pointer && value.IsNil()) {
		return newError(CodeInvalidState, "an event payload is required")
	}
	created := state.LastSeq > 0

	if body, isCreate := payload.(*RunCreated); isCreate {
		if created {
			return newError(CodeInvalidState, "the run already exists")
		}
		if !ValidProjectID(body.ProjectID) || !ValidBoardID(body.BoardID) || body.BoardRevision == 0 {
			return newError(CodeInvalidState, "the run creation identity is invalid")
		}
		return nil
	}
	if !created {
		return newError(CodeInvalidState, "the run has not been created")
	}
	if isTerminal(state.Status) {
		return newError(CodeInvalidState, "the run has already finished")
	}

	switch body := payload.(type) {
	case *RunRootCreated:
		if state.RunRoot != "" {
			return newError(CodeInvalidState, "the run root already exists")
		}
		if body.RunRoot == "" || body.Branch == "" || body.BaseBranch == "" || body.BaseSha == "" {
			return newError(CodeInvalidState, "the run root record is incomplete")
		}
		return nil
	case *ComponentReady:
		return validateComponentInstance(body.ComponentInstance)
	case *ComponentStarted:
		if err := validateComponentInstance(body.ComponentInstance); err != nil {
			return err
		}
		return requireComponentStatus(state, body.ComponentID,
			ComponentReadyStatus, ComponentRunning, ComponentFailedStatus)
	case *ComponentSucceeded:
		if err := validateComponentInstance(body.ComponentInstance); err != nil {
			return err
		}
		return requireComponentStatus(state, body.ComponentID,
			ComponentRunning, ComponentValidating)
	case *ComponentFailed:
		if err := validateComponentInstance(body.ComponentInstance); err != nil {
			return err
		}
		return requireComponentStatus(state, body.ComponentID,
			ComponentRunning, ComponentValidating, ComponentReadyStatus)
	case *AttemptLaunchRequested:
		if err := validateAttemptRef(body.AttemptRef); err != nil {
			return err
		}
		attempt := state.Components[body.ComponentID].Attempt
		if attempt != nil && attempt.Status != AttemptExitedStatus && !replaces(body.AttemptRef, attempt) {
			// Two concurrent attempts on one component would each believe they
			// own the seat's outputs, so a launch while one is live is refused —
			// except when it is the successor that displaces it, which is what a
			// takeover is. TakeoverRequested is intent-only in the reducer, so
			// the attempt being displaced is still live at this point.
			return newError(CodeInvalidState, "the component already has a live attempt")
		}
		return nil
	case *AttemptLaunched:
		if err := validateAttemptRef(body.AttemptRef); err != nil {
			return err
		}
		return requireLiveAttempt(state, body.ComponentID, body.AttemptID)
	case *AttemptSessionBound:
		if err := validateAttemptRef(body.AttemptRef); err != nil {
			return err
		}
		return requireLiveAttempt(state, body.ComponentID, body.AttemptID)
	case *AttemptExited:
		if err := validateAttemptRef(body.AttemptRef); err != nil {
			return err
		}
		return requireLiveAttempt(state, body.ComponentID, body.AttemptID)
	case *GateOpened:
		if err := validateComponentInstance(body.ComponentInstance); err != nil {
			return err
		}
		if state.Gate != nil && state.Gate.Decision == "" {
			return newError(CodeInvalidState, "a gate is already open")
		}
		return nil
	case *GateDecided:
		if err := validateComponentInstance(body.ComponentInstance); err != nil {
			return err
		}
		gate := state.Gate
		if gate == nil || gate.ComponentID != body.ComponentID {
			return newError(CodeInvalidState, "no gate is open for this component")
		}
		if gate.Decision != "" {
			return newError(CodeInvalidState, "the gate has already been decided")
		}
		if body.Decision != GateApproved && body.Decision != GateRejected {
			return newError(CodeInvalidState, "the gate decision is not recognized")
		}
		return nil
	case *PublishCompleted:
		// The intent must be on disk before the fact, so a crash mid-publish is
		// always recoverable by reconciliation.
		if state.Components[ComponentPublish].Status != ComponentRunning {
			return newError(CodeInvalidState, "publish was not attempted")
		}
		return nil
	case *ChangeCaptured:
		if body.ChangeRevision == 0 || body.TreeOID == "" || body.PatchSha256 == "" || body.BaseSha == "" {
			return newError(CodeInvalidState, "the captured change is incomplete")
		}
		if state.Change != nil && body.ChangeRevision <= state.Change.ChangeRevision {
			// Revisions are monotonic; a repeat would let an approval attach to
			// the wrong tree.
			return newError(CodeInvalidState, "the change revision must increase")
		}
		return nil
	case *ArtifactPromoted:
		// A record pointing outside this run's blob store is either corrupt or a
		// cross-canvas reference, and a run must not attest either.
		return AssertArtifactReference(body.Artifact)
	case *PublishAttempted:
		if state.Change == nil || body.ChangeRevision != state.Change.ChangeRevision ||
			body.IdempotencyKey == "" || body.Branch == "" {
			return newError(CodeInvalidState, "the publication attempt is not bound to the current change")
		}
		return nil
	case *CancelRequested:
		if err := validateAttemptRef(body.AttemptRef); err != nil {
			return err
		}
		return requireLiveAttempt(state, body.ComponentID, body.AttemptID)
	case *TakeoverRequested:
		if err := validateAttemptRef(body.AttemptRef); err != nil {
			return err
		}
		// A takeover ALLOCATES an attempt rather than adopting one: the ref
		// names the new human-controlled turn, and PriorAttemptID names the
		// automated one being displaced. So the live-attempt check belongs on
		// the prior id — checking the new one would reject every takeover,
		// since the attempt it names does not exist yet.
		if body.PriorAttemptID == "" {
			return newError(CodeInvalidState, "the takeover does not name the attempt it displaces")
		}
		return requireLiveAttempt(state, body.ComponentID, body.PriorAttemptID)
	case *HandbackCompleted:
		if err := validateAttemptRef(body.AttemptRef); err != nil {
			return err
		}
		if err := requireLiveAttempt(state, body.ComponentID, body.AttemptID); err != nil {
			return err
		}
		if state.Components[body.ComponentID].Attempt.Ownership != OwnershipHumanControlled {
			return newError(CodeInvalidState, "the attempt is not controlled by a human")
		}
		return nil
	case *RunFinished:
		// isTerminal is the single source of truth for what a finished run may
		// be. An unrecognized status would otherwise be written verbatim and
		// then read back as non-terminal, leaving a finished run that every
		// control still offers to advance.
		if !isTerminal(body.Status) {
			return newError(CodeInvalidState, "the run finish status is not terminal")
		}
		return nil
	default:
		return newError(CodeInvalidState, "the event type is not recognized")
	}
}

func isTerminal(status RunStatus) bool {
	return status == RunSucceeded ||
		status == RunFailed ||
		status == RunCanceled ||
		status == RunInterruptedImport
}

func requireComponentStatus(state *RunState, id ComponentID, allowed ...ComponentStatus) error {
	if !ValidComponentID(id) {
		return newError(CodeInvalidState, "the component is not part of the pipeline")
	}
	component := state.Components[id]
	for _, candidate := range allowed {
		if component.Status == candidate {
			return nil
		}
	}
	return newError(CodeInvalidState, "the component cannot make this transition from its current status")
}

func validateComponentInstance(instance ComponentInstance) error {
	if !ValidComponentID(instance.ComponentID) {
		return newError(CodeInvalidState, "the component is not part of the pipeline")
	}
	if instance.Instance < 1 {
		return newError(CodeInvalidState, "the component instance is not valid")
	}
	return nil
}

func validateAttemptRef(attempt AttemptRef) error {
	if err := validateComponentInstance(attempt.ComponentInstance); err != nil {
		return err
	}
	if attempt.SeatID == "" || attempt.Attempt < 1 || attempt.AttemptID == "" {
		return newError(CodeInvalidState, "the attempt identity is not valid")
	}
	return nil
}

func requireLiveAttempt(state *RunState, id ComponentID, attemptID string) error {
	if !ValidComponentID(id) {
		return newError(CodeInvalidState, "the component is not part of the pipeline")
	}
	attempt := state.Components[id].Attempt
	if attempt == nil || attempt.AttemptID != attemptID {
		return newError(CodeInvalidState, "the attempt is not the component's live attempt")
	}
	if attempt.Status == AttemptExitedStatus {
		return newError(CodeInvalidState, "the attempt has already exited")
	}
	return nil
}

// replaces reports whether a launch is the successor of the attempt currently
// live on a component: same instance and seat, one attempt later. That is the
// shape a takeover produces, and the only case in which a component may hold a
// live attempt while another is launched.
func replaces(next AttemptRef, live *AttemptState) bool {
	return live != nil &&
		next.Instance == live.Instance &&
		next.SeatID == live.SeatID &&
		next.Attempt == live.Attempt+1
}
