package atlas

import (
	"context"
	"errors"
	"fmt"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

// The stage machine.
//
// Atlas runs the same fixed pipeline as DaGama — intake, plan, build, verify,
// review, publish — but plan, build, and review are committees. A committee
// stage launches every worker against its own isolated output directory, then
// runs one refine turn that reconciles the drafts into the promoted artifact.
//
// Every transition the operator can influence is a gate the machine stops at
// rather than a decision it makes: a manual trigger between stages, an
// exhausted repair bound, and the publish gate all park the run.

// runPipeline advances a prepared run until it finishes or parks at a gate.
func (c *Controller) runPipeline(
	ctx context.Context,
	board *Board,
	state *RunState,
	source CapturedSource,
	buildInstance uint64,
) (*RunState, error) {
	var err error
	if !componentSucceeded(state, ComponentPlan, 1) {
		if state, err = c.gateTrigger(ctx, board, state, ComponentIntake, ComponentPlan, 1); err != nil || parked(state) {
			return state, err
		}
		if state, err = c.runCommittee(ctx, board, state, source, ComponentPlan, 1); err != nil || failed(state, ComponentPlan) {
			return state, err
		}
	}

	for {
		if state, err = c.gateTrigger(ctx, board, state, ComponentPlan, ComponentBuild, buildInstance); err != nil || parked(state) {
			return state, err
		}
		if state, err = c.runCommittee(ctx, board, state, source, ComponentBuild, buildInstance); err != nil || failed(state, ComponentBuild) {
			return state, err
		}

		// The revision is captured once per Build instance and everything
		// downstream attests that exact number, so verification and review can
		// never be reported against a tree that has moved on.
		state, err = c.captureChange(ctx, state, buildInstance)
		if err != nil || failed(state, ComponentBuild) {
			return state, err
		}

		state, err = c.runVerify(ctx, board, state, buildInstance)
		if err != nil {
			return state, err
		}
		if verificationFailed(state) {
			next, stop, gateErr := c.repairOrGate(ctx, board, state, ComponentVerify, buildInstance,
				"the project checks did not pass")
			if gateErr != nil || stop {
				return next, gateErr
			}
			state, buildInstance = next, buildInstance+1
			continue
		}

		if state, err = c.gateTrigger(ctx, board, state, ComponentVerify, ComponentReview, buildInstance); err != nil || parked(state) {
			return state, err
		}
		if state, err = c.runCommittee(ctx, board, state, source, ComponentReview, buildInstance); err != nil || failed(state, ComponentReview) {
			return state, err
		}

		approved, err := c.reviewApproved(ctx, state)
		if err != nil {
			return c.fail(ctx, state, ComponentReview, buildInstance, "invalid_output", err)
		}
		if !approved {
			next, stop, gateErr := c.repairOrGate(ctx, board, state, ComponentReview, buildInstance,
				"review requested changes")
			if gateErr != nil || stop {
				return next, gateErr
			}
			state, buildInstance = next, buildInstance+1
			continue
		}
		return c.openPublishGate(ctx, state, buildInstance)
	}
}

// gateTrigger stops the run when the edge between two stages is manual.
//
// An automatic edge advances silently. A manual one parks the run with the
// reason the card already shows, so the operator's Go is what resumes it — the
// controller never decides on their behalf.
func (c *Controller) gateTrigger(
	ctx context.Context,
	board *Board,
	state *RunState,
	from, to ComponentID,
	instance uint64,
) (*RunState, error) {
	if board.TriggerModeBetween(from, to) != TriggerManual {
		return state, nil
	}
	if component := state.Component(to); component != nil && component.Instance == instance &&
		(component.Status == ComponentSucceeded || component.Status == ComponentRunning) {
		return state, nil
	}
	if state.Gate != nil && state.Gate.ComponentID == to && state.Gate.Instance == instance {
		// Already gated here: either still waiting, or decided — and a decided
		// trigger gate is the operator's go, so re-opening it would park the
		// run again on the answer it just received.
		return state, nil
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &GateOpened{
		ComponentID: to, Instance: instance,
		Reason:  ReasonWaitingForTrigger,
		Message: fmt.Sprintf("%s waits for an explicit go from %s", to, from),
	})
}

// repairOrGate either starts another bounded Build round or parks the run.
//
// The bound comes from the board's feedback edge. Exhausting it is not a
// failure: it is the point where the operator decides whether to spend another
// round, so the run parks instead of ending.
func (c *Controller) repairOrGate(
	ctx context.Context,
	board *Board,
	state *RunState,
	from ComponentID,
	buildInstance uint64,
	message string,
) (*RunState, bool, error) {
	maxRounds := board.FeedbackMaxRoundsToBuild()
	automatic := board.FeedbackModeToBuild() == TriggerAuto
	if automatic && buildInstance < maxRounds {
		return state, false, nil
	}
	var revision *uint64
	if state.Change != nil {
		value := state.Change.ChangeRevision
		revision = &value
	}
	next, err := c.runs.Append(ctx, state.ProjectID, state.RunID, &GateOpened{
		ComponentID: from, Instance: buildInstance,
		Reason: ReasonWaitingForRepair, Message: message, ChangeRevision: revision,
	})
	return next, true, err
}

// captureChange freezes the tree Build produced.
func (c *Controller) captureChange(ctx context.Context, state *RunState, instance uint64) (*RunState, error) {
	revision := uint64(1)
	if state.Change != nil {
		revision = state.Change.ChangeRevision + 1
	}
	record, err := c.runtime.CaptureChange(ctx, state.RunRoot, revision)
	if err != nil {
		return c.fail(ctx, state, ComponentBuild, instance, "revision_stale", err)
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ChangeCaptured{ChangeRecord: record})
	if err != nil {
		return state, err
	}
	// The changeset is promoted as a controller artifact so Review reads the
	// exact revision that was captured, rather than re-deriving one from a tree
	// that may have moved.
	artifact, err := c.runtime.RecordControllerArtifact(ctx, state.RunRoot, "CHANGESET.patch", nil,
		ArtifactProducer{ComponentID: ComponentBuild, Instance: instance})
	if err != nil {
		return c.fail(ctx, state, ComponentBuild, instance, "invalid_output", err)
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &ArtifactPromoted{Artifact: artifact})
}

// runVerify runs the board's checks against the captured revision.
func (c *Controller) runVerify(
	ctx context.Context,
	board *Board,
	state *RunState,
	instance uint64,
) (*RunState, error) {
	projectID, runID := state.ProjectID, state.RunID
	state, err := c.runs.Append(ctx, projectID, runID, &ComponentReadyEvent{ComponentID: ComponentVerify, Instance: instance})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, projectID, runID, &ComponentStartedEvent{ComponentID: ComponentVerify, Instance: instance})
	if err != nil {
		return state, err
	}
	changeRevision := uint64(0)
	if state.Change != nil {
		changeRevision = state.Change.ChangeRevision
	}
	document, artifact, err := c.runtime.Verify(ctx, VerifyRequest{
		RunRoot: state.RunRoot, Checks: boardChecks(board), ChangeRevision: changeRevision,
	})
	if err != nil {
		return c.fail(ctx, state, ComponentVerify, instance, "verification_failed", err)
	}
	state, err = c.runs.Append(ctx, projectID, runID, &ArtifactPromoted{Artifact: artifact})
	if err != nil {
		return state, err
	}
	if document.Verdict == verification.VerdictFailed {
		return c.fail(ctx, state, ComponentVerify, instance, "verification_failed", nil)
	}
	return c.runs.Append(ctx, projectID, runID, &ComponentSucceededEvent{
		ComponentID: ComponentVerify, Instance: instance, Outputs: []string{artifact.Name},
	})
}

// openPublishGate parks the run for the operator's publication decision.
func (c *Controller) openPublishGate(ctx context.Context, state *RunState, instance uint64) (*RunState, error) {
	var revision *uint64
	if state.Change != nil {
		value := state.Change.ChangeRevision
		revision = &value
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &GateOpened{
		ComponentID: ComponentPublish, Instance: instance,
		Reason:         ReasonBlockedByGate,
		Message:        "the change is verified and reviewed and is waiting for your approval",
		ChangeRevision: revision,
	})
}

// ---------------------------------------------------------------------------
// Committee execution
// ---------------------------------------------------------------------------

// runCommittee executes one committee stage: every worker, then the refine turn.
func (c *Controller) runCommittee(
	ctx context.Context,
	board *Board,
	state *RunState,
	source CapturedSource,
	component ComponentID,
	instance uint64,
) (*RunState, error) {
	committee, err := CommitteeFor(board, component)
	if err != nil {
		return c.fail(ctx, state, component, instance, "invalid_board", err)
	}

	projectID, runID := state.ProjectID, state.RunID
	state, err = c.runs.Append(ctx, projectID, runID, &ComponentReadyEvent{ComponentID: component, Instance: instance})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, projectID, runID, &ComponentStartedEvent{ComponentID: component, Instance: instance})
	if err != nil {
		return state, err
	}

	artifacts, err := c.promptArtifacts(ctx, state)
	if err != nil {
		return c.fail(ctx, state, component, instance, "invalid_output", err)
	}

	// Every worker runs against the same inputs and its own output directory.
	drafts := make([]DraftInput, 0, len(committee.Workers))
	produced := 0
	for index, worker := range committee.Workers {
		seatID := AttemptSeatID(component, index)
		next, record, runErr := c.runSeat(ctx, state, seatRequest{
			Committee: committee, Worker: worker, SeatID: seatID,
			Instance: instance, Attempt: 1, Source: source, Artifacts: artifacts,
			Refine: false, Repair: instance > 1,
		})
		state = next
		if runErr != nil {
			return state, runErr
		}
		if record == nil {
			// A sibling that produced nothing is carried as an explicit
			// absence rather than dropped, so the refine turn consolidates the
			// committee the operator configured.
			drafts = append(drafts, DraftInput{SeatID: seatID, Failed: true})
			continue
		}
		contents, readErr := c.runtime.ReadArtifact(ctx, state.RunRoot, *record)
		if readErr != nil {
			drafts = append(drafts, DraftInput{SeatID: seatID, Failed: true})
			continue
		}
		drafts = append(drafts, DraftInput{SeatID: seatID, Contents: contents})
		produced++
	}

	if produced == 0 {
		// Nothing to reconcile. Failing here is the honest outcome: a refine
		// turn over zero drafts would invent the stage's output.
		return c.fail(ctx, state, component, instance, "missing_output", nil)
	}

	if committee.SkipMainRefine() {
		return c.finishCommittee(ctx, state, committee, instance)
	}

	next, record, err := c.runSeat(ctx, state, seatRequest{
		Committee: committee, Worker: committee.Main(), SeatID: MainRefineSeatID(component),
		Instance: instance, Attempt: 1, Source: source, Artifacts: artifacts,
		Refine: true, Repair: instance > 1, Drafts: drafts,
	})
	state = next
	if err != nil {
		return state, err
	}
	if record == nil {
		return c.fail(ctx, state, component, instance, "missing_output", nil)
	}
	return c.finishCommittee(ctx, state, committee, instance)
}

// finishCommittee records the stage's promoted outputs.
func (c *Controller) finishCommittee(
	ctx context.Context,
	state *RunState,
	committee CommitteeConfig,
	instance uint64,
) (*RunState, error) {
	authored := SeatAuthoredOutputs(committee.ComponentID, committee.RequiredOutputs)
	promoted := make([]string, 0, len(authored))
	for _, name := range authored {
		for _, artifact := range state.Artifacts {
			if artifact.Name == name && artifact.Producer.ComponentID == committee.ComponentID &&
				artifact.Producer.Instance == instance {
				promoted = append(promoted, name)
				break
			}
		}
	}
	if len(promoted) < len(authored) {
		return c.fail(ctx, state, committee.ComponentID, instance, "missing_output", nil)
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentSucceededEvent{
		ComponentID: committee.ComponentID, Instance: instance, Outputs: promoted,
	})
}

// seatRequest is one turn the controller is about to launch.
type seatRequest struct {
	Committee CommitteeConfig
	Worker    WorkerSeat
	SeatID    string
	Instance  uint64
	Attempt   uint64
	Source    CapturedSource
	Artifacts map[string][]byte
	Drafts    []DraftInput
	Refine    bool
	Repair    bool
}

// errSuperseded reports that the attempt this turn was driving is no longer the
// controller's to drive: an operator took it over, or the run finished while it
// was in flight. The caller stops advancing rather than treating it as a
// failure — the takeover path owns the run from that point.
var errSuperseded = errors.New("atlas: the attempt was superseded")

// runSeat launches one turn and records its exact outcome.
//
// The returned record is the primary artifact the turn was contracted to write,
// or nil when it wrote none. A nil record is not an error: a committee tolerates
// a failed sibling, and only the caller knows whether this particular absence
// ends the stage.
func (c *Controller) runSeat(
	ctx context.Context,
	state *RunState,
	request seatRequest,
) (*RunState, *ArtifactRecord, error) {
	component := request.Committee.ComponentID
	projectID, runID := state.ProjectID, state.RunID

	prompt, err := ComposePrompt(PromptInput{
		Committee: request.Committee, Instance: request.Instance, Attempt: request.Attempt,
		SeatID: request.SeatID, Refine: request.Refine, Source: request.Source,
		Artifacts: request.Artifacts, Drafts: request.Drafts, Repair: request.Repair,
	})
	if err != nil {
		next, failErr := c.fail(ctx, state, component, request.Instance, "invalid_output", err)
		return next, nil, failErr
	}

	attemptID := attemptIDFor(runID, component, request.Instance, request.SeatID, request.Attempt)
	tmuxName, err := terminalName(attemptID)
	if err != nil {
		next, failErr := c.fail(ctx, state, component, request.Instance, "launch_failed", err)
		return next, nil, failErr
	}

	// The intent is recorded before the process exists, so a crash mid-launch
	// leaves a reconcilable record rather than an orphan.
	state, err = c.runs.Append(ctx, projectID, runID, &AttemptLaunchRequested{
		ComponentID: component, Instance: request.Instance, SeatID: request.SeatID,
		Attempt: request.Attempt, AttemptID: attemptID, TmuxName: tmuxName,
		Ownership: OwnedByController,
	})
	if err != nil {
		return state, nil, err
	}

	seat := Seat{
		Vendor: request.Worker.Vendor, Model: request.Worker.Model,
		Effort: request.Worker.Effort, Permission: request.Worker.Permission,
	}
	directory := AttemptOutputDirectory(component, request.SeatID, request.Attempt)
	result, executeErr := c.runtime.Execute(ctx, AttemptRequest{
		ProjectID: projectID, RunID: runID, RunRoot: state.RunRoot, BaseSha: state.BaseSha,
		Component: component, Instance: request.Instance, Attempt: request.Attempt,
		AttemptID: attemptID, SeatID: request.SeatID, Seat: seat, Prompt: prompt,
		OutputDirectory: directory,
	}, func(session contracts.SessionIdentity) error {
		if session.ID != "" && session.Agent != string(seat.Vendor) {
			return fmt.Errorf("the attempt returned a cross-vendor session identity")
		}
		var appendErr error
		state, appendErr = c.runs.Append(ctx, projectID, runID, &AttemptLaunched{
			ComponentID: component, Instance: request.Instance, SeatID: request.SeatID,
			Attempt: request.Attempt, AttemptID: attemptID, TmuxName: tmuxName,
			Session: sessionOrNil(session), Ownership: OwnedByController,
		})
		return appendErr
	})
	if executeErr != nil {
		next, failErr := c.fail(ctx, state, component, request.Instance, classify(executeErr), executeErr)
		return next, nil, failErr
	}

	finished := result.FinishedAt
	if finished.IsZero() {
		finished = c.now().UTC()
	}
	state, err = c.runs.Append(ctx, projectID, runID, &AttemptExited{
		ComponentID: component, Instance: request.Instance, SeatID: request.SeatID,
		Attempt: request.Attempt, AttemptID: attemptID,
		ExitCode: result.ExitCode, FinishedAt: finished,
	})
	if err != nil {
		return state, nil, err
	}
	// A turn that finished only because an operator took it over must not also
	// advance the run: the handback path is driving now, and both continuing
	// would run the next stage twice.
	if superseded(state, component, attemptID) {
		return state, nil, errSuperseded
	}
	if attempt := findAttempt(state, component, attemptID); attempt != nil {
		if releaseErr := c.runtime.Release(ctx, *attempt); releaseErr != nil {
			next, failErr := c.fail(ctx, state, component, request.Instance, "cleanup_failed", releaseErr)
			return next, nil, failErr
		}
	}

	return c.promoteSeatOutputs(ctx, state, request, attemptID, result)
}

// promoteSeatOutputs records what the turn actually wrote.
func (c *Controller) promoteSeatOutputs(
	ctx context.Context,
	state *RunState,
	request seatRequest,
	attemptID string,
	result AttemptResult,
) (*RunState, *ArtifactRecord, error) {
	component := request.Committee.ComponentID
	outputs := SeatAuthoredOutputs(component, request.Committee.RequiredOutputs)
	primary := outputs[0]
	if !request.Refine && !request.Committee.SkipMainRefine() {
		primary = DraftArtifactName(outputs[0])
	}

	var promoted *ArtifactRecord
	for _, name := range result.Outputs {
		record, err := c.runtime.RecordControllerArtifact(ctx, state.RunRoot, name, nil, ArtifactProducer{
			ComponentID: component, Instance: request.Instance,
			SeatID: request.SeatID, Attempt: request.Attempt,
		})
		if err != nil {
			next, failErr := c.fail(ctx, state, component, request.Instance, "invalid_output", err)
			return next, nil, failErr
		}
		next, appendErr := c.runs.Append(ctx, state.ProjectID, state.RunID, &ArtifactPromoted{Artifact: record})
		if appendErr != nil {
			return next, nil, appendErr
		}
		state = next
		if record.Name == primary {
			copied := record
			promoted = &copied
		}
	}
	_ = attemptID
	return state, promoted, nil
}

// promptArtifacts collects the promoted upstream outputs a stage may read.
func (c *Controller) promptArtifacts(ctx context.Context, state *RunState) (map[string][]byte, error) {
	artifacts := map[string][]byte{}
	for _, name := range promotedPromptArtifacts {
		for index := len(state.Artifacts) - 1; index >= 0; index-- {
			record := state.Artifacts[index]
			if record.Name != name {
				continue
			}
			contents, err := c.runtime.ReadArtifact(ctx, state.RunRoot, record)
			if err != nil {
				return nil, err
			}
			artifacts[name] = contents
			break
		}
	}
	return artifacts, nil
}

// ---------------------------------------------------------------------------
// Stage predicates
// ---------------------------------------------------------------------------

func componentSucceeded(state *RunState, component ComponentID, instance uint64) bool {
	current := state.Component(component)
	return current != nil && current.Instance == instance && current.Status == ComponentSucceeded
}

func failed(state *RunState, component ComponentID) bool {
	current := state.Component(component)
	return current != nil && current.Status == ComponentFailed
}

// parked reports that the run is waiting on a human and must not advance.
func parked(state *RunState) bool {
	return state.Gate != nil && state.Gate.Decision == ""
}

func verificationFailed(state *RunState) bool {
	current := state.Component(ComponentVerify)
	return current != nil && current.Status == ComponentFailed
}

// settle converts a supersession into a clean stop.
//
// Every entry point into the stage machine ends here, so exactly one caller —
// the one that superseded the attempt — carries the run forward, and the other
// returns the state it observed without reporting a failure.
func settle(state *RunState, err error) (*RunState, error) {
	if errors.Is(err, errSuperseded) {
		return state, nil
	}
	return state, err
}

// superseded reports that a newer attempt has replaced this one on the same
// seat, which is what a takeover records.
func superseded(state *RunState, component ComponentID, attemptID string) bool {
	current := state.Component(component)
	if current == nil {
		return true
	}
	if state.IsTerminal() {
		return true
	}
	return current.Attempt != nil && current.Attempt.AttemptID != attemptID
}

func sessionOrNil(session contracts.SessionIdentity) *contracts.SessionIdentity {
	if session.ID == "" {
		return nil
	}
	copied := session
	return &copied
}
