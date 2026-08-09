package dagama

import (
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Reduce is a pure, total function from events to state.
//
// This is the only place run state is derived. Deleting the materialized view
// and replaying yields a byte-identical document, so nothing derived is ever
// written into the log and no clock is read here — every timestamp comes from
// the event that carried it.
//
// The function is total in the sense that it never fails on event ordering: an
// event that refers to a component instance the reducer has not seen is applied
// as written, because the log is the authority and a reducer that second-guessed
// it would make replay disagree with history. Ordering is enforced when events
// are appended, not when they are replayed.
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
		components[id] = &ComponentRunState{ID: id, Status: ComponentBlocked, Outputs: []string{}}
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
	at := event.At
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

	case *ComponentReady:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentReadyStatus
		component.Instance = body.Instance
		component.Reason = ""
		component.Message = ""
		component.FinishedAt = nil
		// A repair round is a NEW component instance, so its previous outputs
		// must not linger: they belong to the instance that produced them, and a
		// stale list here would let a later gate read an artifact from an earlier
		// revision.
		component.Outputs = []string{}
		component.Attempt = nil

	case *ComponentStarted:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentRunning
		component.Instance = body.Instance
		component.StartedAt = timePointer(at)

	case *ComponentSucceeded:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentSucceededStatus
		component.Instance = body.Instance
		component.FinishedAt = timePointer(at)
		component.Outputs = appendCopy(body.Outputs)
		component.Reason = ""
		component.Message = ""
		if state.Status == RunPreparing {
			state.Status = RunRunning
		}

	case *ComponentFailed:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentFailedStatus
		component.Instance = body.Instance
		component.FinishedAt = timePointer(at)
		component.Reason = body.Reason
		component.Message = body.Message

	case *AttemptLaunchRequested:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentRunning
		component.Instance = body.Instance
		if component.StartedAt == nil {
			component.StartedAt = timePointer(at)
		}
		component.Attempt = &AttemptState{
			AttemptID: body.AttemptID, ComponentID: body.ComponentID, Instance: body.Instance,
			SeatID: body.SeatID, Attempt: body.Attempt, TmuxName: body.TmuxName,
			SessionID: body.SessionID, Ownership: ownershipOr(body.Ownership, OwnershipAutomated),
			Status: AttemptLaunchRequestedStatus, StartedAt: timePointer(at),
		}
		if state.Status == RunPreparing {
			state.Status = RunRunning
		}

	case *AttemptLaunched:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentRunning
		prior := component.Attempt
		attempt := &AttemptState{
			AttemptID: body.AttemptID, ComponentID: body.ComponentID, Instance: body.Instance,
			SeatID: body.SeatID, Attempt: body.Attempt, TmuxName: body.TmuxName,
			SessionID: body.SessionID, Status: AttemptRunning, StartedAt: timePointer(at),
		}
		attempt.Ownership = ownershipOr(body.Ownership, priorOwnership(prior))
		if prior != nil && prior.StartedAt != nil {
			attempt.StartedAt = prior.StartedAt
		}
		component.Attempt = attempt

	case *AttemptSessionBound:
		component := componentOf(state, body.ComponentID)
		if component.Attempt != nil && component.Attempt.AttemptID == body.AttemptID {
			// Never overwrite a different id — that would silently retarget a
			// takeover onto somebody else's live session.
			if component.Attempt.SessionID == "" || component.Attempt.SessionID == body.SessionID {
				component.Attempt.SessionID = body.SessionID
			}
		}

	case *AttemptExited:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentValidating
		if component.Attempt != nil && component.Attempt.AttemptID == body.AttemptID {
			exitCode := body.ExitCode
			finishedAt := body.FinishedAt
			component.Attempt.Status = AttemptExitedStatus
			component.Attempt.ExitCode = &exitCode
			component.Attempt.FinishedAt = &finishedAt
		}

	case *ArtifactPromoted:
		state.Artifacts = append(state.Artifacts, body.Artifact)

	case *ChangeCaptured:
		state.Change = &ChangeRecord{
			ChangeRevision: body.ChangeRevision, TreeOID: body.TreeOID,
			PatchSha256: body.PatchSha256, PatchBytes: body.PatchBytes,
			Insertions: body.Insertions, Deletions: body.Deletions,
			ChangedFiles: appendCopy(body.ChangedFiles), BaseSha: body.BaseSha,
		}

	case *GateOpened:
		component := componentOf(state, body.ComponentID)
		component.Status = ComponentAwaitingApproval
		component.Instance = body.Instance
		component.Reason = body.Reason
		component.Message = body.Message
		changeRevision := body.ChangeRevision
		if changeRevision == nil && state.Change != nil {
			revision := state.Change.ChangeRevision
			changeRevision = &revision
		}
		state.Gate = &GateRecord{
			ComponentID: body.ComponentID, Instance: body.Instance,
			Reason: body.Reason, Message: body.Message,
			ChangeRevision: changeRevision, OpenedAt: at,
		}
		// Publish approval pauses the run; a repair-exhaustion gate leaves it
		// running so the user can still inspect live seats and retry.
		if body.ComponentID == ComponentPublish {
			state.Status = RunAwaitingApproval
		}

	case *GateDecided:
		component := componentOf(state, body.ComponentID)
		component.Message = body.Message
		if body.Decision == GateApproved {
			component.Reason = ""
		} else {
			component.Reason = "gate_rejected"
		}
		if state.Gate != nil && state.Gate.ComponentID == body.ComponentID {
			decidedAt := at
			state.Gate.Decision = body.Decision
			state.Gate.ChangeRevision = body.ChangeRevision
			state.Gate.DecidedAt = &decidedAt
		}
		switch {
		case body.Decision == GateRejected:
			component.Status = ComponentFailedStatus
			component.FinishedAt = timePointer(at)
			state.Status = RunFailed
			state.FinishedAt = timePointer(at)
			state.Failure = &RunFailure{Reason: "gate_rejected", Message: body.Message}
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
			// so the card drops the gate UI.
			component.Status = ComponentSucceededStatus
			component.Reason = ""
			component.Message = ""
		}

	case *PublishAttempted:
		component := componentOf(state, ComponentPublish)
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
		component := componentOf(state, ComponentPublish)
		component.Status = ComponentSucceededStatus
		component.FinishedAt = timePointer(at)
		component.Outputs = []string{"publication.json"}
		component.Reason = ""
		component.Message = ""

	case *CancelRequested, *TakeoverRequested:
		// Intent only — effects follow in the controller before the matching fact.

	case *HandbackCompleted:
		component := componentOf(state, body.ComponentID)
		if component.Attempt != nil && component.Attempt.AttemptID == body.AttemptID {
			component.Attempt.Ownership = OwnershipAutomated
		}

	case *RunFinished:
		state.Status = body.Status
		state.FinishedAt = timePointer(at)
		if body.Status == RunSucceeded {
			state.Failure = nil
		} else {
			state.Failure = &RunFailure{Reason: body.Reason, Message: body.Message}
		}
	}
}

// componentOf returns the state for a component id, creating an entry for an id
// outside the fixed pipeline rather than panicking. A log that names an unknown
// component is corrupt, but replay must not crash the collector; the entry is
// visible so the corruption is diagnosable.
func componentOf(state *RunState, id ComponentID) *ComponentRunState {
	if component, ok := state.Components[id]; ok {
		return component
	}
	component := &ComponentRunState{ID: id, Status: ComponentBlocked, Outputs: []string{}}
	state.Components[id] = component
	return component
}

func timePointer(value time.Time) *time.Time {
	copied := value
	return &copied
}

func ownershipOr(value, fallback Ownership) Ownership {
	if value == OwnershipAutomated || value == OwnershipHumanControlled {
		return value
	}
	return fallback
}

func priorOwnership(prior *AttemptState) Ownership {
	if prior != nil && prior.Ownership != "" {
		return prior.Ownership
	}
	return OwnershipAutomated
}

// appendCopy returns a defensive copy so a caller mutating the slice it passed
// cannot reach into materialized state.
func appendCopy[T any](values []T) []T {
	if len(values) == 0 {
		return []T{}
	}
	copied := make([]T, len(values))
	copy(copied, values)
	return copied
}
