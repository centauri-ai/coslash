package dagama

import "context"

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
	if attempt := liveAttempt(state); attempt != nil {
		state, err = c.runs.Append(ctx, projectID, runID, &CancelRequested{AttemptRef: refOfAttempt(*attempt)})
		if err != nil {
			return state, err
		}
	}
	snapshot, err := c.runtime.Cancel(ctx, state)
	if err != nil {
		return state, err
	}
	if snapshot != nil {
		state, err = c.runs.Append(ctx, projectID, runID, &ArtifactPromoted{Artifact: *snapshot})
		if err != nil {
			return state, err
		}
	}
	reportState := *state
	reportState.Failure = &RunFailure{Reason: "canceled", Message: "the run was canceled by the operator"}
	state, err = c.writeReport(ctx, &reportState, RunCanceled)
	if err != nil {
		return state, err
	}
	return c.runs.Append(ctx, projectID, runID, &RunFinished{Status: RunCanceled, Reason: "canceled", Message: "the run was canceled by the operator"})
}

func liveAttempt(state *RunState) *AttemptState {
	for _, id := range ComponentIDs {
		component := state.Components[id]
		if component != nil && component.Attempt != nil && component.Attempt.Status != AttemptExitedStatus {
			return component.Attempt
		}
	}
	return nil
}

func refOfAttempt(attempt AttemptState) AttemptRef {
	return AttemptRef{ComponentInstance: ComponentInstance{ComponentID: attempt.ComponentID, Instance: attempt.Instance}, SeatID: attempt.SeatID, Attempt: attempt.Attempt, AttemptID: attempt.AttemptID}
}
