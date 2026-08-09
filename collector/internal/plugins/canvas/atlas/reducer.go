package atlas

import (
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Reduce is a pure, total function from events to state.
//
// This is the only place run state is derived. Deleting the materialized view
// and replaying yields an identical document, so nothing derived is ever
// written into the log and no clock is read here — every timestamp comes from
// the event that carried it.
//
// Total means it never fails on ordering: an event naming an attempt the
// current instance no longer tracks is applied as written, because the log is
// the authority and a reducer that second-guessed it would make replay disagree
// with history. Ordering is enforced when events are appended, not when they
// are replayed. The only errors returned are decoding errors, where the stored
// bytes are not an event this build understands.
func Reduce(runID string, events []runfs.Event) (*RunState, error) {
	state := emptyRunState(runID)
	for _, event := range events {
		payload, err := decodePayload(event.Type, event.Data)
		if err != nil {
			return nil, err
		}
		applyEvent(state, event, payload)
	}
	return state, nil
}

func emptyRunState(runID string) *RunState {
	components := make(map[ComponentID]*ComponentRunState, len(ComponentIDs))
	for _, id := range ComponentIDs {
		components[id] = &ComponentRunState{
			ID:       id,
			Status:   ComponentBlocked,
			Outputs:  []string{},
			Attempts: []AttemptState{},
		}
	}
	return &RunState{
		SchemaVersion: RunSchemaVersion,
		RunID:         runID,
		Status:        RunPreparing,
		Components:    components,
		Artifacts:     []ArtifactRecord{},
	}
}

func applyEvent(state *RunState, event runfs.Event, payload Payload) {
	at := event.At.UTC()
	state.LastSeq = event.Seq
	state.UpdatedAt = timePointer(at)

	switch body := payload.(type) {
	case *RunCreated:
		state.ProjectID = body.ProjectID
		state.BoardID = body.BoardID
		state.BoardRevision = body.BoardRevision
		state.Title = body.Title
		source := body.Source
		state.Source = &source
		state.CreatedAt = timePointer(at)
		state.Status = RunPreparing

	case *RunRootCreated:
		state.RunRoot = body.RunRoot
		state.Branch = body.Branch
		state.BaseBranch = body.BaseBranch
		state.BaseSha = body.BaseSha
		state.RemoteURL = body.RemoteURL
		// The publish target defaults to the run's own base. An in-place run
		// accumulates on a long-lived branch and names a different target.
		state.PublishBaseBranch = body.PublishBaseBranch
		if state.PublishBaseBranch == "" {
			state.PublishBaseBranch = body.BaseBranch
		}
		state.PublishBaseSha = body.PublishBaseSha
		if state.PublishBaseSha == "" {
			state.PublishBaseSha = body.BaseSha
		}

	case *ComponentReadyEvent:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		component.Status = ComponentReady
		component.Instance = body.Instance
		component.Reason = body.Reason
		component.Message = body.Message
		component.FinishedAt = nil
		// A repair round is a NEW instance, so the previous outputs must not
		// linger: they belong to the instance that produced them, and a stale
		// list here would let a later gate read an artifact from an earlier
		// revision.
		component.Outputs = []string{}
		component.Attempt = nil
		component.Attempts = []AttemptState{}

	case *ComponentStartedEvent:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		component.Status = ComponentRunning
		component.Instance = body.Instance
		component.StartedAt = timePointer(at)

	case *ComponentSucceededEvent:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		component.Status = ComponentSucceeded
		component.Instance = body.Instance
		component.FinishedAt = timePointer(at)
		component.Outputs = append([]string(nil), body.Outputs...)
		if component.Outputs == nil {
			component.Outputs = []string{}
		}
		component.Reason = ""
		component.Message = ""
		if state.Status == RunPreparing {
			state.Status = RunRunning
		}

	case *ComponentFailedEvent:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		component.Status = ComponentFailed
		component.Instance = body.Instance
		component.FinishedAt = timePointer(at)
		component.Reason = body.Reason
		component.Message = body.Message

	case *AttemptLaunchRequested:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		component.Status = ComponentRunning
		component.Instance = body.Instance
		component.Reason = ""
		component.Message = ""
		if component.StartedAt == nil {
			component.StartedAt = timePointer(at)
		}
		attempt := AttemptState{
			AttemptID:   body.AttemptID,
			ComponentID: body.ComponentID,
			Instance:    body.Instance,
			SeatID:      body.SeatID,
			Attempt:     body.Attempt,
			TmuxName:    body.TmuxName,
			Session:     cloneSession(body.Session),
			Ownership:   ownershipOr(body.Ownership, OwnedByController),
			Status:      AttemptStatusLaunchRequested,
			StartedAt:   timePointer(at),
		}
		upsertAttempt(component, attempt)
		rememberSession(state, attempt.Session)
		// Reopen a failed run when a seat is retried; otherwise the exit handler
		// treats the run as terminal and ignores the new attempt.
		if state.Status == RunPreparing || state.Status == RunFailed {
			state.Status = RunRunning
			state.FinishedAt = nil
			state.Failure = nil
		}

	case *AttemptLaunched:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		component.Status = ComponentRunning
		prior := findAttempt(state, body.ComponentID, body.AttemptID)
		attempt := AttemptState{
			AttemptID:   body.AttemptID,
			ComponentID: body.ComponentID,
			Instance:    body.Instance,
			SeatID:      body.SeatID,
			Attempt:     body.Attempt,
			TmuxName:    body.TmuxName,
			Session:     cloneSession(body.Session),
			Ownership:   ownershipOr(body.Ownership, OwnedByController),
			Status:      AttemptStatusRunning,
			StartedAt:   timePointer(at),
		}
		if prior != nil {
			if body.Ownership == "" {
				attempt.Ownership = prior.Ownership
			}
			if prior.StartedAt != nil {
				attempt.StartedAt = prior.StartedAt
			}
			if attempt.Session == nil {
				attempt.Session = cloneSession(prior.Session)
			}
		}
		upsertAttempt(component, attempt)
		rememberSession(state, attempt.Session)

	case *AttemptSessionBound:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		session := body.Session
		bind := func(current *AttemptState) {
			// Never overwrite a different identity — that would silently
			// retarget a takeover at another agent's session.
			if current.Session != nil && *current.Session != session {
				return
			}
			bound := session
			current.Session = &bound
		}
		if component.Attempt != nil && component.Attempt.AttemptID == body.AttemptID {
			bind(component.Attempt)
		}
		// A committee fan-out keeps every worker in Attempts; the live pointer
		// alone is not enough for the Plan UI to open session details.
		for index := range component.Attempts {
			if component.Attempts[index].AttemptID == body.AttemptID {
				bind(&component.Attempts[index])
			}
		}
		rememberSession(state, &session)

	case *AttemptExited:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		// A late sibling exit must not clear a terminal component outcome:
		// committee workers can finish after their component has already failed
		// closed or succeeded.
		if component.Status != ComponentFailed && component.Status != ComponentSucceeded {
			component.Status = ComponentValidating
		}
		exitCode := body.ExitCode
		finishedAt := body.FinishedAt.UTC()
		exit := func(current *AttemptState) {
			current.Status = AttemptStatusExited
			current.ExitCode = &exitCode
			current.FinishedAt = &finishedAt
		}
		if component.Attempt != nil && component.Attempt.AttemptID == body.AttemptID {
			exit(component.Attempt)
		}
		for index := range component.Attempts {
			if component.Attempts[index].AttemptID == body.AttemptID {
				exit(&component.Attempts[index])
			}
		}

	case *ArtifactPromoted:
		state.Artifacts = append(state.Artifacts, body.Artifact)

	case *ChangeCaptured:
		change := body.ChangeRecord
		if change.ChangedFiles == nil {
			change.ChangedFiles = []ChangedFileRecord{}
		}
		state.Change = &change

	case *GateOpened:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		component.Status = ComponentAwaitingApproval
		component.Instance = body.Instance
		component.Reason = body.Reason
		component.Message = body.Message
		revision := body.ChangeRevision
		if revision == nil && state.Change != nil {
			current := state.Change.ChangeRevision
			revision = &current
		}
		state.Gate = &GateRecord{
			ComponentID:    body.ComponentID,
			Instance:       body.Instance,
			Reason:         body.Reason,
			Message:        body.Message,
			ChangeRevision: revision,
			OpenedAt:       at,
		}
		// A publish approval pauses the run. A repair-exhaustion gate leaves it
		// running so the user can still inspect live seats and retry.
		if body.ComponentID == ComponentPublish {
			state.Status = RunAwaitingApproval
		}

	case *GateDecided:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		isRepairGate := state.Gate != nil && state.Gate.Reason == ReasonWaitingForRepair &&
			(body.ComponentID == ComponentVerify || body.ComponentID == ComponentReview)
		component.Reason = ReasonGateRejected
		if body.Decision == GateApproved || (body.Decision == GateRejected && isRepairGate) {
			component.Reason = ""
		}
		component.Message = body.Message
		if state.Gate != nil && state.Gate.ComponentID == body.ComponentID {
			state.Gate.Decision = body.Decision
			state.Gate.ChangeRevision = body.ChangeRevision
			state.Gate.DecidedAt = timePointer(at)
		}
		switch {
		case body.Decision == GateRejected && isRepairGate:
			// The operator closed the run after repair exhaustion. That is Done,
			// not Failed: a new run can start without a failure stigma.
			component.Status = ComponentSucceeded
			component.FinishedAt = timePointer(at)
			state.Status = RunSucceeded
			state.FinishedAt = timePointer(at)
			state.Failure = nil
		case body.Decision == GateRejected:
			component.Status = ComponentFailed
			component.FinishedAt = timePointer(at)
			state.Status = RunFailed
			state.FinishedAt = timePointer(at)
			state.Failure = &FailureRecord{Reason: ReasonGateRejected, Message: body.Message}
		case body.ComponentID == ComponentPublish:
			component.Status = ComponentRunning
			if component.StartedAt == nil {
				component.StartedAt = timePointer(at)
			}
			if state.Status == RunAwaitingApproval {
				state.Status = RunRunning
			}
		default:
			// Repair-exhaustion approve: leave the gated component so the next
			// Build instance can run. Verify and Review already produced their
			// last artifacts, so restore succeeded and clear the waiting reason
			// so the card drops its gate.
			component.Status = ComponentSucceeded
			component.Reason = ""
			component.Message = ""
		}

	case *PublishAttempted:
		component := state.Components[ComponentPublish]
		component.Status = ComponentRunning
		if component.StartedAt == nil {
			component.StartedAt = timePointer(at)
		}
		if state.Status == RunAwaitingApproval || state.Status == RunPreparing {
			state.Status = RunRunning
		}

	case *PublishCompleted:
		publication := body.Publication
		state.Publication = &publication
		component := state.Components[ComponentPublish]
		component.Status = ComponentSucceeded
		component.FinishedAt = timePointer(at)
		component.Outputs = []string{"publication.json"}
		component.Reason = ""
		component.Message = ""

	case *CancelRequested, *TakeoverRequested:
		// Intent only. The effect and its matching fact follow in the service.

	case *HandbackCompleted:
		component := state.Components[body.ComponentID]
		if component == nil {
			return
		}
		if component.Attempt != nil && component.Attempt.AttemptID == body.AttemptID {
			component.Attempt.Ownership = OwnedByController
		}
		for index := range component.Attempts {
			if component.Attempts[index].AttemptID == body.AttemptID {
				component.Attempts[index].Ownership = OwnedByController
			}
		}

	case *RunFinished:
		state.Status = body.Status
		state.FinishedAt = timePointer(at)
		if body.Status == RunSucceeded {
			state.Failure = nil
			return
		}
		state.Failure = &FailureRecord{Reason: body.Reason, Message: body.Message}
	}
}

// upsertAttempt replaces an attempt by identifier and repoints the live
// attempt, so a retry never leaves two records claiming the same launch.
func upsertAttempt(component *ComponentRunState, attempt AttemptState) {
	for index := range component.Attempts {
		if component.Attempts[index].AttemptID == attempt.AttemptID {
			component.Attempts[index] = attempt
			live := attempt
			component.Attempt = &live
			return
		}
	}
	component.Attempts = append(component.Attempts, attempt)
	live := attempt
	component.Attempt = &live
}

// rememberSession records a composite identity the first time it is seen, in
// order, so a session card can link back to the run that launched it.
func rememberSession(state *RunState, session *contracts.SessionIdentity) {
	if session == nil || session.Agent == "" || session.ID == "" {
		return
	}
	for _, known := range state.Sessions {
		if known == *session {
			return
		}
	}
	state.Sessions = append(state.Sessions, *session)
}

func cloneSession(session *contracts.SessionIdentity) *contracts.SessionIdentity {
	if session == nil || session.Agent == "" || session.ID == "" {
		return nil
	}
	copied := *session
	return &copied
}

func ownershipOr(value, fallback Ownership) Ownership {
	if value == OwnedByController || value == OwnedByHuman {
		return value
	}
	return fallback
}

func timePointer(value time.Time) *time.Time {
	copied := value
	return &copied
}
