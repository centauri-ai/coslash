package dagama

import (
	"context"
)

type ReconcileResult struct{ Rearmed, Drained, Failed, Resumed int }

func (c *Controller) Reconcile(ctx context.Context, projectID string) (ReconcileResult, error) {
	summaries, err := c.runs.List(ctx, projectID)
	if err != nil {
		return ReconcileResult{}, err
	}
	result := ReconcileResult{}
	for _, summary := range summaries {
		if isTerminal(summary.Status) {
			continue
		}
		unlock := c.lock(projectID, summary.RunID)
		state, readErr := c.runs.Read(ctx, projectID, summary.RunID)
		if readErr != nil {
			unlock()
			return result, readErr
		}
		attempt := liveAttempt(state)
		if attempt == nil {
			unlock()
			resumed, resumeErr := c.resumeReady(ctx, state)
			if resumeErr != nil {
				return result, resumeErr
			}
			if resumed {
				result.Resumed++
			}
			continue
		}
		probe, probeErr := c.runtime.Probe(ctx, state, *attempt)
		if probeErr != nil {
			unlock()
			return result, probeErr
		}
		switch {
		case attempt.Status == AttemptLaunchRequestedStatus:
			_, readErr = c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentFailed{
				ComponentInstance: ComponentInstance{ComponentID: attempt.ComponentID, Instance: attempt.Instance},
				Reason:            "unknown_after_restart", Message: "launch state was ambiguous after restart",
			})
			result.Failed++
		case probe.State == ProbeRunning:
			if err := c.runtime.Rearm(ctx, state, *attempt); err != nil {
				unlock()
				return result, err
			}
			result.Rearmed++
		case probe.State == ProbeExited && probe.Completion != nil:
			ref := refOfAttempt(*attempt)
			completion := probe.Completion
			finished := completion.FinishedAt
			if finished.IsZero() {
				finished = c.now().UTC()
			}
			state, readErr = c.runs.Append(ctx, projectID, state.RunID, &AttemptExited{AttemptRef: ref, ExitCode: completion.ExitCode, FinishedAt: finished})
			if readErr == nil {
				readErr = c.runtime.Release(ctx, *state.Components[attempt.ComponentID].Attempt)
			}
			if readErr == nil {
				state, readErr = c.finalizeSeatResult(ctx, state, attempt.ComponentID, attempt.Instance, attempt.Attempt, *completion)
			}
			var board *Board
			var source CapturedSource
			if readErr == nil {
				board, readErr = c.boardForRun(ctx, state)
			}
			if readErr == nil {
				source, readErr = c.sourceForRun(ctx, state)
			}
			result.Drained++
			unlock()
			if readErr != nil {
				return result, readErr
			}
			_, readErr = c.continueAfterAgent(ctx, board, state, source, attempt.ComponentID, attempt.Instance)
			if readErr != nil {
				return result, readErr
			}
			continue
		default:
			_, readErr = c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentFailed{
				ComponentInstance: ComponentInstance{ComponentID: attempt.ComponentID, Instance: attempt.Instance},
				Reason:            "unknown_after_restart", Message: "the attempt could not be classified after restart",
			})
			result.Failed++
		}
		unlock()
		if readErr != nil {
			return result, readErr
		}
	}
	return result, nil
}

func (c *Controller) resumeReady(ctx context.Context, state *RunState) (bool, error) {
	for _, id := range []ComponentID{ComponentPlan, ComponentBuild, ComponentReview} {
		component := state.Components[id]
		if component != nil && component.Status == ComponentReadyStatus {
			board, err := c.boardForRun(ctx, state)
			if err != nil {
				return false, err
			}
			source, err := c.sourceForRun(ctx, state)
			if err != nil {
				return false, err
			}
			state, err = c.runSeat(ctx, board, state, source, id, component.Instance, 1, nil)
			if err != nil {
				return true, err
			}
			_, err = c.continueAfterAgent(ctx, board, state, source, id, component.Instance)
			return true, err
		}
	}
	return false, nil
}
