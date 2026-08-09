package atlas

// The operator transition guards.
//
// Each answers one question: would the controller accept this control against
// this run state, right now? They are exported and named because three callers
// need the same answer and must not drift — the controller applies them before
// mutating, an HTTP layer applies them synchronously so a refused control is a
// refused request, and the frontend mirrors them so it only offers a control
// the backend would accept.
//
// Atlas adds one rule DaGama has no need for: a committee stage is retried as a
// whole, not seat by seat. Retrying a single sibling would produce a refine
// turn reading drafts from two different instances.

// CanRetry reports whether a failed committee stage may be retried.
func CanRetry(state *RunState, componentID ComponentID) error {
	if state == nil {
		return newError(CodeInvalidState, "the run could not be read")
	}
	if state.IsTerminal() {
		return newError(CodeInvalidState, "a terminal run cannot be retried")
	}
	if !HasSeat(componentID) {
		return newError(CodeInvalidState, "only a committee stage can be retried")
	}
	component := state.Component(componentID)
	if component == nil || component.Status != ComponentFailed {
		return newError(CodeInvalidState, "only a failed committee stage can be retried")
	}
	return nil
}

// CanTakeover reports whether the operator may take one attempt.
//
// Takeover resumes the provider session, so it needs a running attempt that has
// already reported one. Without the session there is nothing to resume, and
// relaunching would start a second, unrelated turn.
func CanTakeover(state *RunState, attemptID string) error {
	attempt, err := attemptAnywhere(state, attemptID)
	if err != nil {
		return err
	}
	if attempt.Status != AttemptStatusRunning || attempt.Session == nil || attempt.Session.ID == "" {
		return newError(CodeInvalidState, "takeover requires a running attempt with a known provider session")
	}
	if attempt.Ownership == OwnedByHuman {
		return newError(CodeInvalidState, "the attempt is already human controlled")
	}
	return nil
}

// CanHandback reports whether a human-controlled attempt may be returned.
func CanHandback(state *RunState, attemptID string) error {
	attempt, err := attemptAnywhere(state, attemptID)
	if err != nil {
		return err
	}
	if attempt.Ownership != OwnedByHuman || attempt.Status != AttemptStatusRunning {
		return newError(CodeInvalidState, "handback requires a live human-controlled attempt")
	}
	return nil
}

// CanCancel reports whether the run may still be stopped. Cancel is a run-level
// operation: it stops every live attempt, including a whole committee fan-out.
func CanCancel(state *RunState) error {
	if state == nil {
		return newError(CodeInvalidState, "the run could not be read")
	}
	if state.IsTerminal() {
		return newError(CodeInvalidState, "the run has already finished")
	}
	if state.Status == RunFailed {
		return newError(CodeInvalidState, "the run has already finished")
	}
	return nil
}

// CanDecideGate reports whether the run has an undecided gate whose approval
// still applies to the current change.
//
// A gate recorded against an earlier change revision is refused rather than
// applied: approving it would attest a revision the operator never saw.
func CanDecideGate(state *RunState) error {
	if state == nil {
		return newError(CodeInvalidState, "the run could not be read")
	}
	if state.Gate == nil || state.Gate.Decision != "" {
		return newError(CodeInvalidState, "the run has no undecided gate")
	}
	if state.Gate.ChangeRevision != nil && state.Change != nil &&
		*state.Gate.ChangeRevision != state.Change.ChangeRevision {
		return newError(CodeInvalidState, "the gate approval is stale")
	}
	return nil
}

// CanStartRun reports whether a project may start another run.
//
// One live run per project is a product rule, not a resource limit: two runs
// against the same board would race for the same publication target and the
// operator could not tell which one a pull request came from.
func CanStartRun(summaries []RunSummary) error {
	for _, summary := range summaries {
		switch summary.Status {
		case RunSucceeded, RunFailed, RunCanceled, RunInterruptedImport:
			continue
		default:
			return newError(CodeInvalidState, "this project already has a run in progress")
		}
	}
	return nil
}

// attemptAnywhere locates one attempt across every committee seat.
//
// A fan-out means an attempt is not addressable by component alone, so an
// operator control names the attempt and the controller finds it. The run
// model's own findAttempt is component-scoped and stays that way; this is the
// control-plane lookup.
func attemptAnywhere(state *RunState, attemptID string) (AttemptState, error) {
	if state == nil {
		return AttemptState{}, newError(CodeInvalidState, "the run could not be read")
	}
	if state.IsTerminal() {
		return AttemptState{}, newError(CodeInvalidState, "the run has already finished")
	}
	for _, id := range ComponentIDs {
		component := state.Component(id)
		if component == nil {
			continue
		}
		for _, attempt := range component.Attempts {
			if attempt.AttemptID == attemptID {
				return attempt, nil
			}
		}
		if component.Attempt != nil && component.Attempt.AttemptID == attemptID {
			return *component.Attempt, nil
		}
	}
	return AttemptState{}, newError(CodeNotFound, "the run has no such attempt")
}

// LiveAttempts returns every attempt the runtime still owns a process for,
// which is what a cancel has to stop and a restart has to reconcile.
func LiveAttempts(state *RunState) []AttemptState {
	if state == nil {
		return nil
	}
	var live []AttemptState
	for _, id := range ComponentIDs {
		component := state.Component(id)
		if component == nil {
			continue
		}
		for _, attempt := range component.Attempts {
			if attempt.Status != AttemptStatusExited {
				live = append(live, attempt)
			}
		}
	}
	return live
}
