package atlas

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

// Operator controls: the transitions a human drives.
//
// Every one of them checks its guard first and holds the run lock across the
// check and the mutation, so two operators — or an operator and a reconcile
// pass — cannot both act on the same attempt.

// GateDecisionOptions carries the intents an approval can express.
type GateDecisionOptions struct {
	// SkipPublication ends an approved publish gate without commit, push, or
	// pull request. It is a distinct intent, not a variant of approval: the
	// change is accepted and the operator has chosen to carry it out
	// themselves. Publishing anyway would perform the outward-facing action
	// they explicitly declined.
	SkipPublication bool
}

// PublishSkippedMessage is the recorded reason a succeeded run carries no
// publication record.
const PublishSkippedMessage = "the operator approved the change without publishing it"

// DecideGate records a gate decision and resumes the run.
func (c *Controller) DecideGate(
	ctx context.Context,
	projectID, runID string,
	decision GateDecision,
	message string,
) (*RunState, error) {
	return c.DecideGateWithOptions(ctx, projectID, runID, decision, message, GateDecisionOptions{})
}

// DecideGateWithOptions is DecideGate with the approval intents spelled out.
func (c *Controller) DecideGateWithOptions(
	ctx context.Context,
	projectID, runID string,
	decision GateDecision,
	message string,
	options GateDecisionOptions,
) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := CanDecideGate(state); err != nil {
		unlock()
		return nil, err
	}
	gate := *state.Gate

	var revision *uint64
	if state.Change != nil {
		value := state.Change.ChangeRevision
		revision = &value
	}
	state, err = c.runs.Append(ctx, projectID, runID, &GateDecided{
		ComponentID: gate.ComponentID, Instance: gate.Instance,
		Decision: decision, ChangeRevision: revision, Message: message,
	})
	if err != nil {
		unlock()
		return state, err
	}

	if decision == GateRejected {
		finished, finishErr := c.runs.Append(ctx, projectID, runID, &RunFinished{
			Status: RunFailed, Reason: ReasonGateRejected, Message: message,
		})
		unlock()
		if finishErr != nil {
			return finished, finishErr
		}
		_ = c.runtime.Cleanup(ctx, finished)
		return finished, nil
	}

	board, err := c.boardForRun(ctx, state)
	if err != nil {
		unlock()
		return state, err
	}
	source, err := c.sourceForRun(ctx, state)
	if err != nil {
		unlock()
		return state, err
	}
	unlock()

	switch gate.Reason {
	case ReasonBlockedByGate:
		if options.SkipPublication {
			return c.finishWithoutPublication(ctx, state)
		}
		return c.publish(ctx, board, state)
	case ReasonWaitingForRepair:
		// The operator bought one more Build round.
		build := state.Component(ComponentBuild)
		next := uint64(1)
		if build != nil {
			next = build.Instance + 1
		}
		return settle(c.runPipeline(ctx, board, state, source, next))
	default:
		// A manual trigger. The decided gate is the go; the pipeline resumes
		// from wherever the run currently is.
		return settle(c.runPipeline(ctx, board, state, source, currentBuildInstance(state)))
	}
}

// Retry re-runs a failed committee stage as a whole.
//
// A committee retries whole because retrying one sibling would leave the refine
// turn reconciling drafts from two different instances.
func (c *Controller) Retry(ctx context.Context, projectID, runID string, componentID ComponentID) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := CanRetry(state, componentID); err != nil {
		unlock()
		return nil, err
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		unlock()
		return state, err
	}
	source, err := c.sourceForRun(ctx, state)
	if err != nil {
		unlock()
		return state, err
	}
	component := state.Component(componentID)
	instance := component.Instance
	unlock()

	state, err = c.runCommittee(ctx, board, state, source, componentID, instance)
	if err != nil || failed(state, componentID) {
		return settle(state, err)
	}
	return settle(c.runPipeline(ctx, board, state, source, currentBuildInstance(state)))
}

// Cancel stops every live attempt and ends the run.
func (c *Controller) Cancel(ctx context.Context, projectID, runID string) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	defer unlock()
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	if err := CanCancel(state); err != nil {
		return nil, err
	}

	// Intent is recorded for every live sibling before anything is stopped, so
	// a crash mid-cancel leaves a log that says what was being cancelled.
	for _, attempt := range LiveAttempts(state) {
		state, err = c.runs.Append(ctx, projectID, runID, &CancelRequested{
			ComponentID: attempt.ComponentID, Instance: attempt.Instance,
			SeatID: attempt.SeatID, Attempt: attempt.Attempt, AttemptID: attempt.AttemptID,
		})
		if err != nil {
			return state, err
		}
	}

	snapshot, err := c.runtime.Cancel(ctx, state)
	if err != nil {
		return state, err
	}
	if snapshot != nil {
		// The work in flight is preserved as an artifact rather than discarded:
		// a cancel is the operator stopping a run, not throwing it away.
		state, err = c.runs.Append(ctx, projectID, runID, &ArtifactPromoted{Artifact: *snapshot})
		if err != nil {
			return state, err
		}
	}
	state, err = c.runs.Append(ctx, projectID, runID, &RunFinished{
		Status: RunCanceled, Reason: "canceled", Message: "the run was canceled by the operator",
	})
	if err != nil {
		return state, err
	}
	_ = c.runtime.Cleanup(ctx, state)
	return state, nil
}

// Takeover hands one live attempt to the operator.
func (c *Controller) Takeover(ctx context.Context, projectID, runID, attemptID string) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	defer unlock()
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	if err := CanTakeover(state, attemptID); err != nil {
		return nil, err
	}
	prior, err := attemptAnywhere(state, attemptID)
	if err != nil {
		return nil, err
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		return state, err
	}
	committee, err := CommitteeFor(board, prior.ComponentID)
	if err != nil {
		return state, err
	}

	attempt := prior.Attempt + 1
	nextID := attemptIDFor(runID, prior.ComponentID, prior.Instance, prior.SeatID, attempt)
	tmuxName, err := terminalName(nextID)
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, projectID, runID, &TakeoverRequested{
		ComponentID: prior.ComponentID, Instance: prior.Instance, SeatID: prior.SeatID,
		Attempt: attempt, AttemptID: nextID, PriorAttemptID: prior.AttemptID,
	})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, projectID, runID, &AttemptLaunchRequested{
		ComponentID: prior.ComponentID, Instance: prior.Instance, SeatID: prior.SeatID,
		Attempt: attempt, AttemptID: nextID, TmuxName: tmuxName,
		Session: prior.Session, Ownership: OwnedByHuman,
	})
	if err != nil {
		return state, err
	}

	seat := seatFor(committee, prior.SeatID)
	result, err := c.runtime.Takeover(ctx, AttemptRequest{
		ProjectID: projectID, RunID: runID, RunRoot: state.RunRoot, BaseSha: state.BaseSha,
		Component: prior.ComponentID, Instance: prior.Instance, Attempt: attempt,
		AttemptID: nextID, SeatID: prior.SeatID, Seat: seat,
		OutputDirectory: AttemptOutputDirectory(prior.ComponentID, prior.SeatID, attempt),
		Resume:          prior.Session,
	}, prior)
	if err != nil {
		return c.fail(ctx, state, prior.ComponentID, prior.Instance, classify(err), err)
	}
	// A takeover resumes the same provider session, so a runtime that reports a
	// different vendor has resumed the wrong conversation.
	if result.Session.ID != "" && prior.Session != nil && result.Session.Agent != prior.Session.Agent {
		return c.fail(ctx, state, prior.ComponentID, prior.Instance, "invalid_output",
			fmt.Errorf("takeover returned a cross-vendor session identity"))
	}
	session := prior.Session
	if result.Session.ID != "" {
		session = sessionOrNil(result.Session)
	}
	return c.runs.Append(ctx, projectID, runID, &AttemptLaunched{
		ComponentID: prior.ComponentID, Instance: prior.Instance, SeatID: prior.SeatID,
		Attempt: attempt, AttemptID: nextID, TmuxName: tmuxName,
		Session: session, Ownership: OwnedByHuman,
	})
}

// Handback returns a human-controlled attempt to the controller and continues.
func (c *Controller) Handback(ctx context.Context, projectID, runID, attemptID string) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := CanHandback(state, attemptID); err != nil {
		unlock()
		return nil, err
	}
	attempt, err := attemptAnywhere(state, attemptID)
	if err != nil {
		unlock()
		return nil, err
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		unlock()
		return state, err
	}
	source, err := c.sourceForRun(ctx, state)
	if err != nil {
		unlock()
		return state, err
	}
	committee, err := CommitteeFor(board, attempt.ComponentID)
	if err != nil {
		unlock()
		return state, err
	}
	request := AttemptRequest{
		ProjectID: projectID, RunID: runID, RunRoot: state.RunRoot, BaseSha: state.BaseSha,
		Component: attempt.ComponentID, Instance: attempt.Instance, Attempt: attempt.Attempt,
		AttemptID: attempt.AttemptID, SeatID: attempt.SeatID, Seat: seatFor(committee, attempt.SeatID),
		OutputDirectory: AttemptOutputDirectory(attempt.ComponentID, attempt.SeatID, attempt.Attempt),
		Resume:          attempt.Session,
	}
	// The lock is released across the runtime call: a handback waits for the
	// operator's own turn to end, and holding the run lock for that would block
	// every other control on the run.
	unlock()

	result, runtimeErr := c.runtime.Handback(ctx, request, attempt)

	unlock = c.lock(projectID, runID)
	state, err = c.runs.Read(ctx, projectID, runID)
	if err != nil {
		unlock()
		return nil, err
	}
	// The run may have been cancelled or superseded while the turn finished.
	current, findErr := attemptAnywhere(state, attemptID)
	if state.IsTerminal() || findErr != nil || current.Status == AttemptStatusExited {
		unlock()
		return state, nil
	}
	if runtimeErr != nil {
		failed, failErr := c.fail(ctx, state, attempt.ComponentID, attempt.Instance, classify(runtimeErr), runtimeErr)
		unlock()
		return failed, failErr
	}

	state, err = c.runs.Append(ctx, projectID, runID, &HandbackCompleted{
		ComponentID: attempt.ComponentID, Instance: attempt.Instance,
		SeatID: attempt.SeatID, Attempt: attempt.Attempt, AttemptID: attempt.AttemptID,
	})
	if err != nil {
		unlock()
		return state, err
	}
	finishedAt := result.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = c.now().UTC()
	}
	state, err = c.runs.Append(ctx, projectID, runID, &AttemptExited{
		ComponentID: attempt.ComponentID, Instance: attempt.Instance, SeatID: attempt.SeatID,
		Attempt: attempt.Attempt, AttemptID: attempt.AttemptID,
		ExitCode: result.ExitCode, FinishedAt: finishedAt,
	})
	unlock()
	if err != nil {
		return state, err
	}
	return settle(c.runPipeline(ctx, board, state, source, currentBuildInstance(state)))
}

// ---------------------------------------------------------------------------
// Publication
// ---------------------------------------------------------------------------

// publish performs the one publication an approved revision earns.
func (c *Controller) publish(ctx context.Context, board *Board, state *RunState) (*RunState, error) {
	if state.Change == nil {
		return c.terminate(ctx, state, ComponentPublish, 1, "revision_stale",
			newError(CodeInvalidState, "the run has no current revision"))
	}
	verify, err := c.latestVerification(ctx, state)
	if err != nil {
		return c.terminate(ctx, state, ComponentPublish, 1, "revision_stale", err)
	}
	review, err := c.latestReview(ctx, state)
	if err != nil {
		return c.terminate(ctx, state, ComponentPublish, 1, "revision_stale", err)
	}

	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &PublishAttempted{
		ChangeRevision: state.Change.ChangeRevision,
		IdempotencyKey: fmt.Sprintf("%s-%d", state.RunID, state.Change.ChangeRevision),
		Branch:         state.Branch,
	})
	if err != nil {
		return state, err
	}

	record, artifact, publishErr := c.runtime.Publish(ctx, PublishRequest{
		State: state, Board: board,
		Review:       ReviewFact{Approved: review.Approved(), ChangeRevision: state.Change.ChangeRevision},
		Verification: verify,
		Title:        state.Title, Body: "Atlas run " + state.RunID,
	})
	if publishErr != nil {
		return c.terminate(ctx, state, ComponentPublish, 1, classify(publishErr), publishErr)
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ArtifactPromoted{Artifact: artifact})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &PublishCompleted{Publication: record})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &RunFinished{
		Status: RunSucceeded, Message: "the workflow completed successfully",
	})
	if err != nil {
		return state, err
	}
	_ = c.runtime.Cleanup(ctx, state)
	return state, nil
}

// finishWithoutPublication completes an approved run the operator chose not to
// publish. The change stays in the run root exactly as it was verified and
// reviewed; nothing is committed, pushed, or opened.
func (c *Controller) finishWithoutPublication(ctx context.Context, state *RunState) (*RunState, error) {
	state, err := c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentSucceededEvent{
		ComponentID: ComponentPublish, Instance: 1,
	})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &RunFinished{
		Status: RunSucceeded, Reason: "publish_skipped", Message: PublishSkippedMessage,
	})
	if err != nil {
		return state, err
	}
	_ = c.runtime.Cleanup(ctx, state)
	return state, nil
}

// ---------------------------------------------------------------------------
// Run context
// ---------------------------------------------------------------------------

// boardForRun returns the board snapshot the run was started against, never the
// live board: an edit made mid-run must not change what the run is executing.
func (c *Controller) boardForRun(ctx context.Context, state *RunState) (*Board, error) {
	document, err := c.runs.BoardSnapshot(ctx, state.ProjectID, state.RunID)
	if err != nil {
		return nil, err
	}
	return document.Board, nil
}

// sourceForRun rebuilds the captured source from the run's own artifacts.
func (c *Controller) sourceForRun(ctx context.Context, state *RunState) (CapturedSource, error) {
	if state.Source == nil {
		return CapturedSource{}, newError(CodeInvalidState, "the run has no recorded source")
	}
	for index := len(state.Artifacts) - 1; index >= 0; index-- {
		record := state.Artifacts[index]
		if record.Name != "SOURCE.md" {
			continue
		}
		body, err := c.runtime.ReadArtifact(ctx, state.RunRoot, record)
		if err != nil {
			return CapturedSource{}, err
		}
		return CapturedSource{Record: *state.Source, Body: body}, nil
	}
	return CapturedSource{}, newError(CodeNotFound, "the run has no source artifact")
}

// latestVerification returns the newest verification document the run promoted.
func (c *Controller) latestVerification(ctx context.Context, state *RunState) (verification.Document, error) {
	for index := len(state.Artifacts) - 1; index >= 0; index-- {
		record := state.Artifacts[index]
		if record.Name != "verification.json" {
			continue
		}
		contents, err := c.runtime.ReadArtifact(ctx, state.RunRoot, record)
		if err != nil {
			return verification.Document{}, err
		}
		var document verification.Document
		if err := json.Unmarshal(contents, &document); err != nil {
			return verification.Document{}, newError(CodeCorruptDocument, "the verification document is not valid JSON")
		}
		return document, nil
	}
	return verification.Document{}, newError(CodeNotFound, "the run has no verification document")
}

// currentBuildInstance is the Build round the run is on.
func currentBuildInstance(state *RunState) uint64 {
	if build := state.Component(ComponentBuild); build != nil && build.Instance > 0 {
		return build.Instance
	}
	return 1
}

// seatFor resolves the launch profile a run-log seat belongs to.
func seatFor(committee CommitteeConfig, seatID string) Seat {
	worker := committee.Main()
	if IsWorkerSeatID(committee.ComponentID, seatID) {
		for index, candidate := range committee.Workers {
			if AttemptSeatID(committee.ComponentID, index) == seatID {
				worker = candidate
				break
			}
		}
	}
	return Seat{
		Vendor: worker.Vendor, Model: worker.Model,
		Effort: worker.Effort, Permission: worker.Permission,
	}
}
