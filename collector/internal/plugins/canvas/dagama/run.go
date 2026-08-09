package dagama

import (
	"encoding/json"
	"fmt"
	"time"
)

// RunSchemaVersion is the current durable run schema.
const RunSchemaVersion uint64 = 1

// Bounds. A run's whole event log has to stay replayable in memory, and an
// unbounded log is a symptom of a stuck controller rather than of a long run.
const (
	MaxEventsPerRun uint64 = 20_000
	MaxEventBytes   int64  = 64 << 10
	// MaxRepairRounds counts repair instances after the first Build. Instance 1
	// is the initial implementation; instances 2..1+N are repairs. Exhaustion
	// opens a human gate rather than failing the run outright.
	MaxRepairRounds = 2
)

// RunStatus is the run's lifecycle position.
type RunStatus string

const (
	RunPreparing        RunStatus = "preparing"
	RunRunning          RunStatus = "running"
	RunAwaitingApproval RunStatus = "awaiting_approval"
	RunSucceeded        RunStatus = "succeeded"
	RunFailed           RunStatus = "failed"
	RunCanceled         RunStatus = "canceled"
	// RunInterruptedImport is a run this collector never executed: legacy
	// history the migration imported. It is terminal by construction — the live
	// process it described ended in another product, so there is nothing any
	// control could resume. Atlas defines the same status.
	RunInterruptedImport RunStatus = "interrupted_migration"
)

// ComponentStatus is one stage's position.
type ComponentStatus string

const (
	ComponentBlocked          ComponentStatus = "blocked"
	ComponentReadyStatus      ComponentStatus = "ready"
	ComponentRunning          ComponentStatus = "running"
	ComponentValidating       ComponentStatus = "validating"
	ComponentAwaitingApproval ComponentStatus = "awaiting_approval"
	ComponentSucceededStatus  ComponentStatus = "succeeded"
	ComponentFailedStatus     ComponentStatus = "failed"
)

// Ownership distinguishes an automated attempt from one a human took over.
type Ownership string

const (
	OwnershipAutomated       Ownership = "automated"
	OwnershipHumanControlled Ownership = "human_controlled"
)

// AttemptStatus is the lifecycle of one agent launch.
type AttemptStatus string

const (
	AttemptLaunchRequestedStatus AttemptStatus = "launch_requested"
	AttemptRunning               AttemptStatus = "running"
	AttemptExitedStatus          AttemptStatus = "exited"
)

// GateDecision is a human's answer to an open gate.
type GateDecision string

const (
	GateApproved GateDecision = "approved"
	GateRejected GateDecision = "rejected"
)

// ChangedFileRecord is one path in a captured revision.
type ChangedFileRecord struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// ChangeRecord is the controller-captured revision identity.
type ChangeRecord struct {
	ChangeRevision uint64              `json:"changeRevision"`
	TreeOID        string              `json:"treeOid"`
	PatchSha256    string              `json:"patchSha256"`
	PatchBytes     int64               `json:"patchBytes"`
	Insertions     int64               `json:"insertions"`
	Deletions      int64               `json:"deletions"`
	ChangedFiles   []ChangedFileRecord `json:"changedFiles"`
	BaseSha        string              `json:"baseSha"`
}

// GateRecord is the latest open or decided human gate.
type GateRecord struct {
	ComponentID    ComponentID  `json:"componentId"`
	Instance       int          `json:"instance"`
	Reason         string       `json:"reason"`
	Message        string       `json:"message"`
	Decision       GateDecision `json:"decision"`
	ChangeRevision *uint64      `json:"changeRevision"`
	OpenedAt       time.Time    `json:"openedAt"`
	DecidedAt      *time.Time   `json:"decidedAt"`
}

// PublicationRecord is the latest successful publication.
type PublicationRecord struct {
	ChangeRevision uint64    `json:"changeRevision"`
	CommitSha      string    `json:"commitSha"`
	Branch         string    `json:"branch"`
	Remote         string    `json:"remote"`
	PRURL          string    `json:"prUrl"`
	PRNumber       int       `json:"prNumber"`
	Action         string    `json:"action"`
	IdempotencyKey string    `json:"idempotencyKey"`
	PublishedAt    time.Time `json:"publishedAt"`
}

// SourceRecord is the run's intake material.
type SourceRecord struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Sha256 string `json:"sha256"`
}

// ArtifactRecord mirrors a promoted artifact into run state.
type ArtifactRecord struct {
	ArtifactID string           `json:"artifactId"`
	Kind       string           `json:"kind"`
	Name       string           `json:"name"`
	Path       string           `json:"path"`
	Sha256     string           `json:"sha256"`
	Bytes      int64            `json:"bytes"`
	CreatedAt  time.Time        `json:"createdAt"`
	Producer   ArtifactProducer `json:"producer"`
}

// ArtifactProducer identifies what created an artifact.
type ArtifactProducer struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    int         `json:"instance"`
	SeatID      string      `json:"seatId,omitempty"`
	Attempt     int         `json:"attempt,omitempty"`
}

// AttemptState is the latest attempt for a component. Retries replace it;
// history remains in the event log.
type AttemptState struct {
	AttemptID   string        `json:"attemptId"`
	ComponentID ComponentID   `json:"componentId"`
	Instance    int           `json:"instance"`
	SeatID      string        `json:"seatId"`
	Attempt     int           `json:"attempt"`
	TmuxName    string        `json:"tmuxName"`
	SessionID   string        `json:"sessionId"`
	Ownership   Ownership     `json:"ownership"`
	Status      AttemptStatus `json:"status"`
	ExitCode    *int          `json:"exitCode"`
	StartedAt   *time.Time    `json:"startedAt"`
	FinishedAt  *time.Time    `json:"finishedAt"`
}

// ComponentRunState is one stage's durable state.
type ComponentRunState struct {
	ID       ComponentID     `json:"id"`
	Status   ComponentStatus `json:"status"`
	Instance int             `json:"instance"`
	// Reason explains the status when the status alone is not enough:
	// blocked_by_gate, waiting_for_repair, or a failure reason from the taxonomy.
	Reason     string        `json:"reason"`
	Message    string        `json:"message"`
	StartedAt  *time.Time    `json:"startedAt"`
	FinishedAt *time.Time    `json:"finishedAt"`
	Outputs    []string      `json:"outputs"`
	Attempt    *AttemptState `json:"attempt"`
}

// RunFailure records why a run ended badly.
type RunFailure struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// RunState is the materialized view of one run. It is always rebuildable from
// the event log, so nothing derived is ever written into the log and no clock is
// read during materialization.
type RunState struct {
	SchemaVersion uint64     `json:"schemaVersion"`
	RunID         string     `json:"runId"`
	ProjectID     string     `json:"projectId"`
	BoardID       string     `json:"boardId"`
	BoardRevision uint64     `json:"boardRevision"`
	Title         string     `json:"title"`
	Status        RunStatus  `json:"status"`
	CreatedAt     *time.Time `json:"createdAt"`
	UpdatedAt     *time.Time `json:"updatedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`

	Source     *SourceRecord `json:"source"`
	RunRoot    string        `json:"runRoot"`
	Branch     string        `json:"branch"`
	BaseBranch string        `json:"baseBranch"`
	BaseSha    string        `json:"baseSha"`
	RemoteURL  string        `json:"remoteUrl"`

	Components map[ComponentID]*ComponentRunState `json:"components"`
	Artifacts  []ArtifactRecord                   `json:"artifacts"`
	// Change is the latest controller-captured revision, nil before the first
	// successful Build.
	Change      *ChangeRecord      `json:"change"`
	Gate        *GateRecord        `json:"gate"`
	Publication *PublicationRecord `json:"publication"`
	Failure     *RunFailure        `json:"failure"`
	LastSeq     uint64             `json:"lastSeq"`
}

// RunSummary is the list projection.
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

// Summary projects the list view.
func (s *RunState) Summary() RunSummary {
	return RunSummary{
		RunID: s.RunID, ProjectID: s.ProjectID, BoardID: s.BoardID, Title: s.Title,
		Status: s.Status, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, FinishedAt: s.FinishedAt,
	}
}

// ---------------------------------------------------------------------------
// Events
//
// Event names are the durable wire format. Each payload is a separate type so
// the reducer can decode exactly the fields its case reads, and an unknown or
// malformed payload is a decode failure rather than a silently empty struct.
// ---------------------------------------------------------------------------

// Event names.
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

// Payload is one decoded event body.
type Payload interface{ EventType() string }

// RunCreated opens a run.
type RunCreated struct {
	ProjectID     string       `json:"projectId"`
	BoardID       string       `json:"boardId"`
	BoardRevision uint64       `json:"boardRevision"`
	Title         string       `json:"title"`
	Source        SourceRecord `json:"source"`
}

// RunRootCreated records the isolated workspace a run was given.
type RunRootCreated struct {
	RunRoot    string `json:"runRoot"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"baseBranch"`
	BaseSha    string `json:"baseSha"`
	RemoteURL  string `json:"remoteUrl"`
}

// ComponentInstance is the shared header of every component-scoped event.
type ComponentInstance struct {
	ComponentID ComponentID `json:"componentId"`
	Instance    int         `json:"instance"`
}

// ComponentReady marks a component runnable at an instance.
type ComponentReady struct{ ComponentInstance }

// ComponentStarted marks a component running.
type ComponentStarted struct{ ComponentInstance }

// ComponentSucceeded records the outputs an instance produced.
type ComponentSucceeded struct {
	ComponentInstance
	Outputs []string `json:"outputs"`
}

// ComponentFailed records a taxonomy reason and a safe message.
type ComponentFailed struct {
	ComponentInstance
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// AttemptRef identifies one attempt within a component instance.
type AttemptRef struct {
	ComponentInstance
	SeatID    string `json:"seatId"`
	Attempt   int    `json:"attempt"`
	AttemptID string `json:"attemptId"`
}

// AttemptLaunchRequested is recorded before the process is spawned, so a crash
// between intent and effect leaves the intent on disk.
type AttemptLaunchRequested struct {
	AttemptRef
	TmuxName  string    `json:"tmuxName"`
	SessionID string    `json:"sessionId"`
	Ownership Ownership `json:"ownership,omitempty"`
}

// AttemptLaunched is the matching fact.
type AttemptLaunched struct {
	AttemptRef
	TmuxName  string    `json:"tmuxName"`
	SessionID string    `json:"sessionId"`
	Ownership Ownership `json:"ownership,omitempty"`
}

// AttemptSessionBound carries the vendor session identity.
//
// Codex cannot choose a session id up front; the thread id arrives on the first
// structured stream line. This fact updates the live attempt so takeover and
// resume can proceed without guessing from cwd plus launch time.
type AttemptSessionBound struct {
	AttemptRef
	SessionID string `json:"sessionId"`
}

// AttemptExited records the process outcome.
type AttemptExited struct {
	AttemptRef
	ExitCode   int       `json:"exitCode"`
	FinishedAt time.Time `json:"finishedAt"`
}

// ArtifactPromoted mirrors an artifacts-package promotion into the run.
type ArtifactPromoted struct {
	Artifact ArtifactRecord `json:"artifact"`
}

// ChangeCaptured records a frozen revision.
type ChangeCaptured struct {
	ChangeRevision uint64              `json:"changeRevision"`
	TreeOID        string              `json:"treeOid"`
	PatchSha256    string              `json:"patchSha256"`
	PatchBytes     int64               `json:"patchBytes"`
	Insertions     int64               `json:"insertions"`
	Deletions      int64               `json:"deletions"`
	ChangedFiles   []ChangedFileRecord `json:"changedFiles"`
	BaseSha        string              `json:"baseSha"`
}

// GateOpened pauses for a human.
type GateOpened struct {
	ComponentInstance
	Reason         string  `json:"reason"`
	Message        string  `json:"message"`
	ChangeRevision *uint64 `json:"changeRevision,omitempty"`
}

// GateDecided records the human's answer.
type GateDecided struct {
	ComponentInstance
	Decision       GateDecision `json:"decision"`
	ChangeRevision *uint64      `json:"changeRevision"`
	Message        string       `json:"message"`
}

// PublishAttempted is the intent recorded before any remote effect.
type PublishAttempted struct {
	ChangeRevision uint64 `json:"changeRevision"`
	IdempotencyKey string `json:"idempotencyKey"`
	Branch         string `json:"branch"`
}

// PublishCompleted is the matching fact.
type PublishCompleted struct {
	Publication PublicationRecord `json:"publication"`
}

// CancelRequested and TakeoverRequested are intents only; their effects follow
// in the controller before the matching fact.
type CancelRequested struct{ AttemptRef }

// TakeoverRequested hands a live attempt to a human.
type TakeoverRequested struct {
	AttemptRef
	PriorAttemptID string `json:"priorAttemptId"`
}

// HandbackCompleted returns a human-controlled attempt to automation.
type HandbackCompleted struct{ AttemptRef }

// RunFinished closes a run.
type RunFinished struct {
	Status  RunStatus `json:"status"`
	Reason  string    `json:"reason"`
	Message string    `json:"message"`
}

func (RunCreated) EventType() string             { return EventRunCreated }
func (RunRootCreated) EventType() string         { return EventRunRootCreated }
func (ComponentReady) EventType() string         { return EventComponentReady }
func (ComponentStarted) EventType() string       { return EventComponentStarted }
func (ComponentSucceeded) EventType() string     { return EventComponentSucceeded }
func (ComponentFailed) EventType() string        { return EventComponentFailed }
func (AttemptLaunchRequested) EventType() string { return EventAttemptLaunchRequested }
func (AttemptLaunched) EventType() string        { return EventAttemptLaunched }
func (AttemptSessionBound) EventType() string    { return EventAttemptSessionBound }
func (AttemptExited) EventType() string          { return EventAttemptExited }
func (ArtifactPromoted) EventType() string       { return EventArtifactPromoted }
func (ChangeCaptured) EventType() string         { return EventChangeCaptured }
func (GateOpened) EventType() string             { return EventGateOpened }
func (GateDecided) EventType() string            { return EventGateDecided }
func (PublishAttempted) EventType() string       { return EventPublishAttempted }
func (PublishCompleted) EventType() string       { return EventPublishCompleted }
func (CancelRequested) EventType() string        { return EventCancelRequested }
func (TakeoverRequested) EventType() string      { return EventTakeoverRequested }
func (HandbackCompleted) EventType() string      { return EventHandbackCompleted }
func (RunFinished) EventType() string            { return EventRunFinished }

// decodePayload turns a durable event body into its typed payload. An unknown
// type is an error rather than a skip: silently ignoring an event a newer
// coSlash wrote would materialize a state that never existed.
func decodePayload(eventType string, data json.RawMessage) (Payload, error) {
	decode := func(target Payload) (Payload, error) {
		if err := json.Unmarshal(data, target); err != nil {
			return nil, newError(CodeCorruptDocument, "an event payload could not be decoded").
				withDetail(fmt.Sprintf("%s: %v", eventType, err)).withCause(err)
		}
		return target, nil
	}
	switch eventType {
	case EventRunCreated:
		return decode(&RunCreated{})
	case EventRunRootCreated:
		return decode(&RunRootCreated{})
	case EventComponentReady:
		return decode(&ComponentReady{})
	case EventComponentStarted:
		return decode(&ComponentStarted{})
	case EventComponentSucceeded:
		return decode(&ComponentSucceeded{})
	case EventComponentFailed:
		return decode(&ComponentFailed{})
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
		return nil, newError(CodeCorruptDocument, "the run log contains an unknown event type").
			withDetail(eventType)
	}
}
