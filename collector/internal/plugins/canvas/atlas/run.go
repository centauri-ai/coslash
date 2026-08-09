package atlas

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
)

// The Atlas run model.
//
// A run is an append-only event log. events.jsonl IS the run; run.json is a
// materialized view that can be deleted and rebuilt at any time. Every event is
// fsynced before the side effect it authorizes, so a crash between an intent
// and its effect leaves the intent recorded and reconciliation can see it.
//
// Two shapes follow from that. Payloads are facts or intents, never derived
// state — nothing computed is ever written into the log. And every timestamp in
// the state comes from the event envelope that carried it, so replay is
// deterministic and never reads a clock.

// RunSchemaVersion is the version of the materialized run view.
const RunSchemaVersion uint64 = 1

// Bounds on one run's log. A run whose log grows past these is a symptom of a
// stuck controller rather than of a long run.
const (
	MaxEventsPerRun uint64 = 20_000
	MaxEventBytes   int64  = 64 << 10
)

// RunsDirectory and RootsDirectory are siblings on purpose: no relative path
// leads from an agent's working directory up into the control state.
const (
	RunsDirectory  = "runs"
	RootsDirectory = "roots"
)

// RunStatus is the run's lifecycle position.
type RunStatus string

const (
	RunPreparing         RunStatus = "preparing"
	RunRunning           RunStatus = "running"
	RunAwaitingApproval  RunStatus = "awaiting_approval"
	RunSucceeded         RunStatus = "succeeded"
	RunFailed            RunStatus = "failed"
	RunCanceled          RunStatus = "canceled"
	RunInterruptedImport RunStatus = "interrupted_migration"
)

// TerminalRunStatuses are the statuses no further work follows.
var TerminalRunStatuses = []RunStatus{RunSucceeded, RunCanceled, RunInterruptedImport}

// ComponentStatus is one pipeline stage's position.
type ComponentStatus string

const (
	ComponentBlocked          ComponentStatus = "blocked"
	ComponentReady            ComponentStatus = "ready"
	ComponentRunning          ComponentStatus = "running"
	ComponentValidating       ComponentStatus = "validating"
	ComponentAwaitingApproval ComponentStatus = "awaiting_approval"
	ComponentSucceeded        ComponentStatus = "succeeded"
	ComponentFailed           ComponentStatus = "failed"
)

// AttemptStatus is one agent launch's position.
type AttemptStatus string

const (
	AttemptStatusLaunchRequested AttemptStatus = "launch_requested"
	AttemptStatusRunning         AttemptStatus = "running"
	AttemptStatusExited          AttemptStatus = "exited"
)

// Ownership records whether the controller or a human is driving an attempt.
type Ownership string

const (
	OwnedByController Ownership = "automated"
	OwnedByHuman      Ownership = "human_controlled"
)

// GateDecision is a human's answer at a gate.
type GateDecision string

const (
	GateApproved GateDecision = "approved"
	GateRejected GateDecision = "rejected"
)

// Reasons carried on component and gate records. They are stable machine
// strings the run cards already surface, so they are preserved verbatim.
const (
	ReasonWaitingForTrigger  = "waiting_for_trigger"
	ReasonWaitingForRepair   = "waiting_for_repair"
	ReasonWaitingForFeedback = "waiting_for_feedback"
	ReasonBlockedByGate      = "blocked_by_gate"
	ReasonGateRejected       = "gate_rejected"
)

// ---------------------------------------------------------------------------
// Records
// ---------------------------------------------------------------------------

// SourceRecord describes the input a run was started from.
type SourceRecord struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// ChangedFileRecord is one path in a captured revision.
type ChangedFileRecord struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// ChangeRecord is an immutable capture of what Build produced.
type ChangeRecord struct {
	ChangeRevision uint64              `json:"changeRevision"`
	TreeOID        string              `json:"treeOid"`
	PatchSHA256    string              `json:"patchSha256"`
	PatchBytes     int64               `json:"patchBytes"`
	Insertions     int                 `json:"insertions"`
	Deletions      int                 `json:"deletions"`
	ChangedFiles   []ChangedFileRecord `json:"changedFiles"`
	BaseSha        string              `json:"baseSha"`
}

// ArtifactRecord is one promoted output and who produced it.
type ArtifactRecord struct {
	ArtifactID string           `json:"artifactId"`
	Kind       string           `json:"kind"`
	Name       string           `json:"name"`
	Path       string           `json:"path"`
	SHA256     string           `json:"sha256"`
	Bytes      int64            `json:"bytes"`
	CreatedAt  time.Time        `json:"createdAt"`
	Producer   ArtifactProducer `json:"producer"`
}

// ArtifactProducer attributes an artifact to a seat and attempt, which is what
// keeps a sibling worker's output distinguishable after a committee fan-out.
type ArtifactProducer struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
	SeatID      string      `json:"seatId,omitempty"`
	Attempt     uint64      `json:"attempt,omitempty"`
}

// GateRecord is the latest open or decided human gate.
type GateRecord struct {
	ComponentID    ComponentID  `json:"componentId"`
	Instance       uint64       `json:"instance"`
	Reason         string       `json:"reason"`
	Message        string       `json:"message"`
	Decision       GateDecision `json:"decision,omitempty"`
	ChangeRevision *uint64      `json:"changeRevision"`
	OpenedAt       time.Time    `json:"openedAt"`
	DecidedAt      *time.Time   `json:"decidedAt"`
}

// PublicationRecord is the one pull request an approved revision produced.
type PublicationRecord struct {
	ChangeRevision uint64    `json:"changeRevision"`
	CommitSha      string    `json:"commitSha"`
	Branch         string    `json:"branch"`
	Remote         string    `json:"remote"`
	PRURL          string    `json:"prUrl,omitempty"`
	PRNumber       int       `json:"prNumber,omitempty"`
	Action         string    `json:"action"`
	IdempotencyKey string    `json:"idempotencyKey"`
	PublishedAt    time.Time `json:"publishedAt"`
}

// FailureRecord explains a terminal failure.
type FailureRecord struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// AttemptState is one agent launch.
//
// Session is the composite {agent,id} identity. A bare id is never enough:
// Claude and Codex allocate ids independently and collide.
type AttemptState struct {
	AttemptID   string                     `json:"attemptId"`
	ComponentID ComponentID                `json:"componentId"`
	Instance    uint64                     `json:"instance"`
	SeatID      string                     `json:"seatId"`
	Attempt     uint64                     `json:"attempt"`
	TmuxName    string                     `json:"tmuxName"`
	Session     *contracts.SessionIdentity `json:"session"`
	Ownership   Ownership                  `json:"ownership"`
	Status      AttemptStatus              `json:"status"`
	ExitCode    *int                       `json:"exitCode"`
	StartedAt   *time.Time                 `json:"startedAt"`
	FinishedAt  *time.Time                 `json:"finishedAt"`
}

// ComponentRunState is one pipeline stage's live state.
type ComponentRunState struct {
	ID       ComponentID     `json:"id"`
	Status   ComponentStatus `json:"status"`
	Instance uint64          `json:"instance"`
	// Reason explains the status when the status alone is not enough.
	Reason     string     `json:"reason,omitempty"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	Outputs    []string   `json:"outputs"`
	// Attempt is the latest attempt. Retries replace it; history stays in the log.
	Attempt *AttemptState `json:"attempt"`
	// Attempts holds every attempt of the current instance, which is what keeps
	// committee siblings addressable after a fan-out.
	Attempts []AttemptState `json:"attempts"`
}

// RunState is the materialized view of one run.
type RunState struct {
	SchemaVersion uint64        `json:"schemaVersion"`
	RunID         string        `json:"runId"`
	ProjectID     string        `json:"projectId"`
	BoardID       string        `json:"boardId"`
	BoardRevision uint64        `json:"boardRevision"`
	Title         string        `json:"title"`
	Status        RunStatus     `json:"status"`
	CreatedAt     *time.Time    `json:"createdAt"`
	UpdatedAt     *time.Time    `json:"updatedAt"`
	FinishedAt    *time.Time    `json:"finishedAt"`
	Source        *SourceRecord `json:"source"`

	RunRoot           string `json:"runRoot,omitempty"`
	Branch            string `json:"branch,omitempty"`
	BaseBranch        string `json:"baseBranch,omitempty"`
	BaseSha           string `json:"baseSha,omitempty"`
	PublishBaseBranch string `json:"publishBaseBranch,omitempty"`
	PublishBaseSha    string `json:"publishBaseSha,omitempty"`
	RemoteURL         string `json:"remoteUrl,omitempty"`

	Components map[ComponentID]*ComponentRunState `json:"components"`
	Artifacts  []ArtifactRecord                   `json:"artifacts"`
	// Sessions lists every composite identity this run has bound, in the order
	// they were first seen, so a session card can link back to its run.
	Sessions    []contracts.SessionIdentity `json:"sessions,omitempty"`
	Change      *ChangeRecord               `json:"change"`
	Gate        *GateRecord                 `json:"gate"`
	Publication *PublicationRecord          `json:"publication"`
	Failure     *FailureRecord              `json:"failure"`
	LastSeq     uint64                      `json:"lastSeq"`
}

// RunSummary is the listing projection.
type RunSummary struct {
	RunID      string     `json:"runId"`
	ProjectID  string     `json:"projectId"`
	BoardID    string     `json:"boardId"`
	Title      string     `json:"title"`
	Status     RunStatus  `json:"status"`
	CreatedAt  *time.Time `json:"createdAt"`
	UpdatedAt  *time.Time `json:"updatedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// Summary projects a run for listing.
func (s *RunState) Summary() RunSummary {
	return RunSummary{
		RunID:      s.RunID,
		ProjectID:  s.ProjectID,
		BoardID:    s.BoardID,
		Title:      s.Title,
		Status:     s.Status,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
		FinishedAt: s.FinishedAt,
	}
}

// Component returns one stage's state, or nil for an unknown id.
func (s *RunState) Component(id ComponentID) *ComponentRunState { return s.Components[id] }

// IsTerminal reports whether no further work follows.
func (s *RunState) IsTerminal() bool { return slices.Contains(TerminalRunStatuses, s.Status) }

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// Event type names. These are durable strings in every run log ever written, so
// they are part of the contract and must not be reworded.
const (
	EventRunCreated             = "run_created"
	EventRunRootCreated         = "run_root_created"
	EventComponentReady         = "component_ready"
	EventComponentStarted       = "component_started"
	EventComponentSucceeded     = "component_succeeded"
	EventComponentFailed        = "component_failed"
	EventAttemptLaunchRequested = "attempt_launch_requested"
	EventAttemptLaunched        = "attempt_launched"
	EventAttemptSessionBound    = "attempt_session_bound"
	EventAttemptExited          = "attempt_exited"
	EventArtifactPromoted       = "artifact_promoted"
	EventChangeCaptured         = "change_captured"
	EventGateOpened             = "gate_opened"
	EventGateDecided            = "gate_decided"
	EventPublishAttempted       = "publish_attempted"
	EventPublishCompleted       = "publish_completed"
	EventCancelRequested        = "cancel_requested"
	EventTakeoverRequested      = "takeover_requested"
	EventHandbackCompleted      = "handback_completed"
	EventRunFinished            = "run_finished"
)

// Payload is one event's typed body.
type Payload interface {
	// EventType returns the durable event name this payload is stored under.
	EventType() string
}

// RunCreated records the run's identity and immutable inputs.
type RunCreated struct {
	ProjectID     string       `json:"projectId"`
	BoardID       string       `json:"boardId"`
	BoardRevision uint64       `json:"boardRevision"`
	Title         string       `json:"title"`
	Source        SourceRecord `json:"source"`
}

// RunRootCreated records where agents will work and what they branch from.
type RunRootCreated struct {
	RunRoot           string `json:"runRoot"`
	Branch            string `json:"branch"`
	BaseBranch        string `json:"baseBranch"`
	BaseSha           string `json:"baseSha"`
	RemoteURL         string `json:"remoteUrl,omitempty"`
	PublishBaseBranch string `json:"publishBaseBranch,omitempty"`
	PublishBaseSha    string `json:"publishBaseSha,omitempty"`
}

// ComponentReadyEvent arms a stage for its next instance.
type ComponentReadyEvent struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
	Reason      string      `json:"reason,omitempty"`
	Message     string      `json:"message,omitempty"`
}

// ComponentStartedEvent marks a stage as working.
type ComponentStartedEvent struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
}

// ComponentSucceededEvent records validated outputs.
type ComponentSucceededEvent struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
	Outputs     []string    `json:"outputs"`
}

// ComponentFailedEvent records a stage failure and its taxonomy reason.
type ComponentFailedEvent struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
	Reason      string      `json:"reason"`
	Message     string      `json:"message"`
}

// AttemptLaunchRequested is the intent recorded before an agent is spawned, so
// a crash mid-launch leaves a reconcilable record rather than an orphan.
type AttemptLaunchRequested struct {
	ComponentID ComponentID                `json:"componentId"`
	Instance    uint64                     `json:"instance"`
	SeatID      string                     `json:"seatId"`
	Attempt     uint64                     `json:"attempt"`
	AttemptID   string                     `json:"attemptId"`
	TmuxName    string                     `json:"tmuxName"`
	Session     *contracts.SessionIdentity `json:"session,omitempty"`
	Ownership   Ownership                  `json:"ownership,omitempty"`
}

// AttemptLaunched is the fact that the agent is running.
type AttemptLaunched struct {
	ComponentID ComponentID                `json:"componentId"`
	Instance    uint64                     `json:"instance"`
	SeatID      string                     `json:"seatId"`
	Attempt     uint64                     `json:"attempt"`
	AttemptID   string                     `json:"attemptId"`
	TmuxName    string                     `json:"tmuxName"`
	Session     *contracts.SessionIdentity `json:"session,omitempty"`
	Ownership   Ownership                  `json:"ownership,omitempty"`
}

// AttemptSessionBound records a session identity that could not be chosen up
// front. Codex allocates its thread id on the first structured stream line, so
// takeover and resume need this fact rather than a guess from cwd and time.
type AttemptSessionBound struct {
	ComponentID ComponentID               `json:"componentId"`
	Instance    uint64                    `json:"instance"`
	SeatID      string                    `json:"seatId"`
	Attempt     uint64                    `json:"attempt"`
	AttemptID   string                    `json:"attemptId"`
	Session     contracts.SessionIdentity `json:"session"`
}

// AttemptExited records a process exit.
type AttemptExited struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
	SeatID      string      `json:"seatId"`
	Attempt     uint64      `json:"attempt"`
	AttemptID   string      `json:"attemptId"`
	ExitCode    int         `json:"exitCode"`
	FinishedAt  time.Time   `json:"finishedAt"`
}

// ArtifactPromoted records a validated output moving into the run's artifacts.
type ArtifactPromoted struct {
	Artifact ArtifactRecord `json:"artifact"`
}

// ChangeCaptured records an immutable revision of the work tree.
type ChangeCaptured struct {
	ChangeRecord
}

// GateOpened records that the run is waiting on a human.
type GateOpened struct {
	ComponentID    ComponentID `json:"componentId"`
	Instance       uint64      `json:"instance"`
	Reason         string      `json:"reason"`
	Message        string      `json:"message"`
	ChangeRevision *uint64     `json:"changeRevision,omitempty"`
}

// GateDecided records the human's answer.
type GateDecided struct {
	ComponentID    ComponentID  `json:"componentId"`
	Instance       uint64       `json:"instance"`
	Decision       GateDecision `json:"decision"`
	ChangeRevision *uint64      `json:"changeRevision"`
	Message        string       `json:"message"`
}

// PublishAttempted is the intent recorded before any remote effect, carrying
// the idempotency key that makes a retry produce at most one pull request.
type PublishAttempted struct {
	ChangeRevision uint64 `json:"changeRevision"`
	IdempotencyKey string `json:"idempotencyKey"`
	Branch         string `json:"branch"`
}

// PublishCompleted records the publication that resulted.
type PublishCompleted struct {
	Publication PublicationRecord `json:"publication"`
}

// CancelRequested is intent only; the effect and its fact follow.
type CancelRequested struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
	SeatID      string      `json:"seatId"`
	Attempt     uint64      `json:"attempt"`
	AttemptID   string      `json:"attemptId"`
}

// TakeoverRequested is intent only; a human is claiming a live attempt.
type TakeoverRequested struct {
	ComponentID    ComponentID `json:"componentId"`
	Instance       uint64      `json:"instance"`
	SeatID         string      `json:"seatId"`
	Attempt        uint64      `json:"attempt"`
	AttemptID      string      `json:"attemptId"`
	PriorAttemptID string      `json:"priorAttemptId"`
}

// HandbackCompleted returns a human-controlled attempt to the controller.
type HandbackCompleted struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    uint64      `json:"instance"`
	SeatID      string      `json:"seatId"`
	Attempt     uint64      `json:"attempt"`
	AttemptID   string      `json:"attemptId"`
}

// RunFinished closes the run.
type RunFinished struct {
	Status  RunStatus `json:"status"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
}

func (RunCreated) EventType() string              { return EventRunCreated }
func (RunRootCreated) EventType() string          { return EventRunRootCreated }
func (ComponentReadyEvent) EventType() string     { return EventComponentReady }
func (ComponentStartedEvent) EventType() string   { return EventComponentStarted }
func (ComponentSucceededEvent) EventType() string { return EventComponentSucceeded }
func (ComponentFailedEvent) EventType() string    { return EventComponentFailed }
func (AttemptLaunchRequested) EventType() string  { return EventAttemptLaunchRequested }
func (AttemptLaunched) EventType() string         { return EventAttemptLaunched }
func (AttemptSessionBound) EventType() string     { return EventAttemptSessionBound }
func (AttemptExited) EventType() string           { return EventAttemptExited }
func (ArtifactPromoted) EventType() string        { return EventArtifactPromoted }
func (ChangeCaptured) EventType() string          { return EventChangeCaptured }
func (GateOpened) EventType() string              { return EventGateOpened }
func (GateDecided) EventType() string             { return EventGateDecided }
func (PublishAttempted) EventType() string        { return EventPublishAttempted }
func (PublishCompleted) EventType() string        { return EventPublishCompleted }
func (CancelRequested) EventType() string         { return EventCancelRequested }
func (TakeoverRequested) EventType() string       { return EventTakeoverRequested }
func (HandbackCompleted) EventType() string       { return EventHandbackCompleted }
func (RunFinished) EventType() string             { return EventRunFinished }

// decodePayload rebuilds a typed payload from a stored event.
//
// An unknown event type is an error rather than a skip. Replaying past an event
// this build cannot interpret would produce a state that silently disagrees
// with the history it claims to summarize.
func decodePayload(eventType string, data json.RawMessage) (Payload, error) {
	decode := func(target Payload) (Payload, error) {
		if len(data) == 0 {
			return nil, newError(CodeCorruptDocument, fmt.Sprintf("event %s carries no data", eventType))
		}
		if err := json.Unmarshal(data, target); err != nil {
			return nil, newError(CodeCorruptDocument, fmt.Sprintf("event %s could not be decoded", eventType)).
				withDetail(err.Error()).withCause(err)
		}
		return target, nil
	}

	switch eventType {
	case EventRunCreated:
		return decode(&RunCreated{})
	case EventRunRootCreated:
		return decode(&RunRootCreated{})
	case EventComponentReady:
		return decode(&ComponentReadyEvent{})
	case EventComponentStarted:
		return decode(&ComponentStartedEvent{})
	case EventComponentSucceeded:
		return decode(&ComponentSucceededEvent{})
	case EventComponentFailed:
		return decode(&ComponentFailedEvent{})
	case EventAttemptLaunchRequested:
		return decode(&AttemptLaunchRequested{})
	case EventAttemptLaunched:
		return decode(&AttemptLaunched{})
	case EventAttemptSessionBound:
		return decode(&AttemptSessionBound{})
	case EventAttemptExited:
		return decode(&AttemptExited{})
	case EventArtifactPromoted:
		return decode(&ArtifactPromoted{})
	case EventChangeCaptured:
		return decode(&ChangeCaptured{})
	case EventGateOpened:
		return decode(&GateOpened{})
	case EventGateDecided:
		return decode(&GateDecided{})
	case EventPublishAttempted:
		return decode(&PublishAttempted{})
	case EventPublishCompleted:
		return decode(&PublishCompleted{})
	case EventCancelRequested:
		return decode(&CancelRequested{})
	case EventTakeoverRequested:
		return decode(&TakeoverRequested{})
	case EventHandbackCompleted:
		return decode(&HandbackCompleted{})
	case EventRunFinished:
		return decode(&RunFinished{})
	default:
		return nil, newError(CodeCorruptDocument, fmt.Sprintf("event %s is not a known Atlas event", eventType))
	}
}

// ---------------------------------------------------------------------------
// Transition validation
// ---------------------------------------------------------------------------

// ValidateTransition refuses an event that the current state cannot produce.
//
// The reducer stays total — it applies whatever the log contains, because the
// log is the authority — so the guard belongs here, before an event is durable.
// The rules are deliberately narrow: they refuse moves that would make later
// "this already happened" conclusions unsound, and leave ordering the
// controller owns to the controller.
func ValidateTransition(state *RunState, payload Payload) error {
	if payload == nil {
		return newError(CodeInvalidState, "an event payload is required")
	}
	created := state != nil && state.CreatedAt != nil

	if _, isCreate := payload.(*RunCreated); isCreate {
		if created {
			return newError(CodeInvalidState, "the run was already created")
		}
		body := payload.(*RunCreated)
		if !ValidProjectID(body.ProjectID) || !ValidBoardID(body.BoardID) || body.BoardRevision == 0 {
			return newError(CodeInvalidState, "a run needs a valid project, board, and board revision")
		}
		if body.Title == "" {
			return newError(CodeInvalidState, "a run needs a title")
		}
		return nil
	}
	if !created {
		return newError(CodeInvalidState, "the run must be created before any other event")
	}
	// A succeeded or canceled run is finished. A failed one may still be
	// retried, which is why failure is not in this set.
	if state.IsTerminal() {
		return newError(CodeInvalidState, "the run is already finished")
	}

	switch body := payload.(type) {
	case *ComponentReadyEvent:
		return validComponent(body.ComponentID)
	case *ComponentStartedEvent:
		return validComponent(body.ComponentID)
	case *ComponentSucceededEvent:
		return validComponent(body.ComponentID)
	case *ComponentFailedEvent:
		if err := validComponent(body.ComponentID); err != nil {
			return err
		}
		if body.Reason == "" {
			return newError(CodeInvalidState, "a component failure needs a reason")
		}
		return nil

	case *AttemptLaunchRequested:
		if err := validComponent(body.ComponentID); err != nil {
			return err
		}
		if body.AttemptID == "" || body.SeatID == "" {
			return newError(CodeInvalidState, "an attempt needs a seat and an attempt identifier")
		}
		if findAttempt(state, body.ComponentID, body.AttemptID) != nil {
			return newError(CodeInvalidState, "the attempt identifier is already in use")
		}
		return nil
	case *AttemptLaunched:
		return requireAttempt(state, body.ComponentID, body.AttemptID)
	case *AttemptSessionBound:
		// A binding or an exit can arrive for an attempt the current instance no
		// longer tracks: a committee sibling can finish after its component has
		// already failed closed and been re-armed. Both are facts about a
		// process that really ran, and refusing to record a fact is worse than
		// recording one the reducer will apply to nothing.
		if err := validComponent(body.ComponentID); err != nil {
			return err
		}
		if body.Session.Agent == "" || body.Session.ID == "" {
			return newError(CodeInvalidState, "a bound session needs both an agent and an id")
		}
		return nil
	case *AttemptExited:
		return validComponent(body.ComponentID)
	case *CancelRequested:
		return requireAttempt(state, body.ComponentID, body.AttemptID)
	case *HandbackCompleted:
		return requireAttempt(state, body.ComponentID, body.AttemptID)
	case *TakeoverRequested:
		if err := validComponent(body.ComponentID); err != nil {
			return err
		}
		return requireAttempt(state, body.ComponentID, body.PriorAttemptID)

	case *GateOpened:
		if err := validComponent(body.ComponentID); err != nil {
			return err
		}
		if state.Gate != nil && state.Gate.Decision == "" {
			return newError(CodeInvalidState, "a gate is already open")
		}
		return nil
	case *GateDecided:
		if err := validComponent(body.ComponentID); err != nil {
			return err
		}
		if state.Gate == nil || state.Gate.Decision != "" {
			return newError(CodeInvalidState, "no gate is open")
		}
		if state.Gate.ComponentID != body.ComponentID {
			return newError(CodeInvalidState, "the decision does not match the open gate")
		}
		if body.Decision != GateApproved && body.Decision != GateRejected {
			return newError(CodeInvalidState, "a gate decision is either approved or rejected")
		}
		return nil

	case *PublishAttempted:
		if body.IdempotencyKey == "" {
			return newError(CodeInvalidState, "a publish attempt needs an idempotency key")
		}
		if state.Change == nil {
			return newError(CodeInvalidState, "publishing needs a captured revision")
		}
		return nil
	case *PublishCompleted:
		if state.Change == nil {
			return newError(CodeInvalidState, "publishing needs a captured revision")
		}
		return nil

	case *RunFinished:
		switch body.Status {
		case RunSucceeded, RunFailed, RunCanceled, RunInterruptedImport:
			return nil
		default:
			return newError(CodeInvalidState, "a run finishes as succeeded, failed, canceled, or interrupted_migration")
		}
	}
	return nil
}

func validComponent(id ComponentID) error {
	if !ValidComponentID(id) {
		return &Error{Code: CodeInvalidState, Message: "the event names a component that does not exist", Field: "componentId"}
	}
	return nil
}

func requireAttempt(state *RunState, componentID ComponentID, attemptID string) error {
	if err := validComponent(componentID); err != nil {
		return err
	}
	if findAttempt(state, componentID, attemptID) == nil {
		return newError(CodeInvalidState, "the event names an attempt that was never launched")
	}
	return nil
}

func findAttempt(state *RunState, componentID ComponentID, attemptID string) *AttemptState {
	component := state.Components[componentID]
	if component == nil || attemptID == "" {
		return nil
	}
	for index := range component.Attempts {
		if component.Attempts[index].AttemptID == attemptID {
			return &component.Attempts[index]
		}
	}
	return nil
}
