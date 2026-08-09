package dagama

import (
	"context"
	"fmt"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

func (c *Controller) Takeover(ctx context.Context, projectID, runID string, componentID ComponentID) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	defer unlock()
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	component := state.Components[componentID]
	if component == nil || component.Attempt == nil || component.Attempt.Status != AttemptRunning || component.Attempt.SessionID == "" {
		return nil, newError(CodeInvalidState, "takeover requires a running attempt with a known provider session")
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		return nil, err
	}
	prior := *component.Attempt
	attempt := prior.Attempt + 1
	attemptID := fmt.Sprintf("%s-%s-%d-%d", runID, componentID, component.Instance, attempt)
	tmuxName, err := terminal.Name("dagama", attemptID)
	if err != nil {
		return nil, err
	}
	ref := AttemptRef{ComponentInstance: ComponentInstance{ComponentID: componentID, Instance: component.Instance}, SeatID: prior.SeatID, Attempt: attempt, AttemptID: attemptID}
	state, err = c.runs.Append(ctx, projectID, runID, &TakeoverRequested{AttemptRef: ref, PriorAttemptID: prior.AttemptID})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, projectID, runID, &AttemptLaunchRequested{AttemptRef: ref, TmuxName: tmuxName, SessionID: prior.SessionID, Ownership: OwnershipHumanControlled})
	if err != nil {
		return state, err
	}
	seat := seatFor(board, componentID)
	request := AttemptRequest{ProjectID: projectID, RunID: runID, RunRoot: state.RunRoot, BaseSha: state.BaseSha, Component: componentID, Instance: component.Instance, Attempt: attempt, AttemptID: attemptID, SeatID: prior.SeatID, Seat: seat, Resume: &contracts.SessionIdentity{Agent: string(seat.Vendor), ID: prior.SessionID}}
	result, err := c.runtime.Takeover(ctx, request, prior)
	if err != nil {
		return c.finishFailure(ctx, state, componentID, component.Instance, "launch_failed", err)
	}
	if result.Session.ID != "" && result.Session.Agent != string(seat.Vendor) {
		return c.finishFailure(ctx, state, componentID, component.Instance, "invalid_output", fmt.Errorf("takeover returned a cross-vendor session identity"))
	}
	sessionID := prior.SessionID
	if result.Session.ID != "" {
		sessionID = result.Session.ID
	}
	return c.runs.Append(ctx, projectID, runID, &AttemptLaunched{AttemptRef: ref, TmuxName: tmuxName, SessionID: sessionID, Ownership: OwnershipHumanControlled})
}

func (c *Controller) Handback(ctx context.Context, projectID, runID string, componentID ComponentID) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		unlock()
		return nil, err
	}
	component := state.Components[componentID]
	if component == nil || component.Attempt == nil || component.Attempt.Ownership != OwnershipHumanControlled || component.Attempt.Status != AttemptRunning {
		unlock()
		return nil, newError(CodeInvalidState, "handback requires a live human-controlled attempt")
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		unlock()
		return nil, err
	}
	attempt := *component.Attempt
	request := AttemptRequest{ProjectID: projectID, RunID: runID, RunRoot: state.RunRoot, BaseSha: state.BaseSha, Component: componentID, Instance: component.Instance, Attempt: attempt.Attempt, AttemptID: attempt.AttemptID, SeatID: attempt.SeatID, Seat: seatFor(board, componentID), Resume: &contracts.SessionIdentity{Agent: string(seatFor(board, componentID).Vendor), ID: attempt.SessionID}}
	unlock()
	result, err := c.runtime.Handback(ctx, request, attempt)
	unlock = c.lock(projectID, runID)
	state, readErr := c.runs.Read(ctx, projectID, runID)
	if readErr != nil {
		unlock()
		return nil, readErr
	}
	current := state.Components[componentID]
	if isTerminal(state.Status) || current == nil || current.Attempt == nil || current.Attempt.AttemptID != attempt.AttemptID {
		unlock()
		return state, nil
	}
	if err != nil {
		failed, failureErr := c.finishFailure(ctx, state, componentID, component.Instance, classifyError(err), err)
		unlock()
		return failed, failureErr
	}
	ref := refOfAttempt(attempt)
	state, err = c.runs.Append(ctx, projectID, runID, &HandbackCompleted{AttemptRef: ref})
	if err != nil {
		unlock()
		return state, err
	}
	finished := result.FinishedAt
	if finished.IsZero() {
		finished = c.now().UTC()
	}
	state, err = c.runs.Append(ctx, projectID, runID, &AttemptExited{AttemptRef: ref, ExitCode: result.ExitCode, FinishedAt: finished})
	if err != nil {
		unlock()
		return state, err
	}
	if releaseErr := c.runtime.Release(ctx, *state.Components[componentID].Attempt); releaseErr != nil {
		failed, failureErr := c.finishFailure(ctx, state, componentID, component.Instance, "cleanup_failed", releaseErr)
		unlock()
		return failed, failureErr
	}
	state, err = c.finalizeSeatResult(ctx, state, componentID, component.Instance, attempt.Attempt, result)
	if err != nil {
		unlock()
		return state, err
	}
	source, sourceErr := c.sourceForRun(ctx, state)
	if sourceErr != nil {
		unlock()
		return state, sourceErr
	}
	unlock()
	return c.continueAfterAgent(ctx, board, state, source, componentID, component.Instance)
}
