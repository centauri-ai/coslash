package dagama

// The operator transition guards.
//
// Each function answers one question: would the controller accept this control
// against this run state, right now? They are named and exported because three
// callers need the same answer and must not drift:
//
//   - the controller, which applies them before mutating anything;
//   - the HTTP handler, which applies them synchronously so a refused control
//     is a refused request rather than a silent background failure;
//   - the frontend, which mirrors them so it only offers a control the backend
//     would accept (see frontend/src/plugins/canvas/dagama/runs.ts).
//
// A nil error means "accepted now". None of these reserve anything: the caller
// still holds the run lock across the check and the mutation.

// CanRetry reports whether a failed seat may be retried.
func CanRetry(state *RunState, componentID ComponentID) error {
	if state == nil {
		return newError(CodeInvalidState, "the run could not be read")
	}
	if isTerminal(state.Status) {
		return newError(CodeInvalidState, "a terminal run cannot be retried")
	}
	component := state.Components[componentID]
	if component == nil || component.Status != ComponentFailedStatus || !HasSeat(componentID) {
		return newError(CodeInvalidState, "only a failed agent component can be retried")
	}
	return nil
}

// CanTakeover reports whether the operator may take a seat's attempt.
//
// Takeover resumes the provider session, so it needs a running attempt that has
// already reported one. Without the session identity there is nothing to
// resume, and relaunching would start a second, unrelated turn.
func CanTakeover(state *RunState, componentID ComponentID) error {
	if state == nil {
		return newError(CodeInvalidState, "the run could not be read")
	}
	component := state.Components[componentID]
	if component == nil || component.Attempt == nil ||
		component.Attempt.Status != AttemptRunning || component.Attempt.SessionID == "" {
		return newError(CodeInvalidState, "takeover requires a running attempt with a known provider session")
	}
	return nil
}

// CanHandback reports whether a human-controlled seat may be returned to the
// controller.
func CanHandback(state *RunState, componentID ComponentID) error {
	if state == nil {
		return newError(CodeInvalidState, "the run could not be read")
	}
	component := state.Components[componentID]
	if component == nil || component.Attempt == nil ||
		component.Attempt.Ownership != OwnershipHumanControlled ||
		component.Attempt.Status != AttemptRunning {
		return newError(CodeInvalidState, "handback requires a live human-controlled attempt")
	}
	return nil
}

// CanCancel reports whether the run may still be stopped. Cancel is a run-level
// operation: it stops whichever attempt is live, not a component the caller
// names.
func CanCancel(state *RunState) error {
	if state == nil {
		return newError(CodeInvalidState, "the run could not be read")
	}
	if isTerminal(state.Status) {
		return newError(CodeInvalidState, "the run has already finished")
	}
	return nil
}

// CanDecideGate reports whether the run has an undecided gate whose approval
// would still apply to the current change.
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
