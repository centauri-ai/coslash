package atlas

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

// The operator controls, exercised against a real parked run.
//
// What matters in every case is that the control does exactly what the operator
// asked and nothing more: an approval that skips publication does not publish,
// a cancel does not discard the work in flight, and a rejection ends the run
// rather than quietly continuing it.

// parkedAtPublish drives a run to the publish gate.
func parkedAtPublish(t *testing.T, workers int) (*controllerFixture, *RunState) {
	t.Helper()
	fixture := newControllerFixture(t, workers)
	state := fixture.start(t)
	if state.Gate == nil || state.Gate.ComponentID != ComponentPublish {
		t.Fatalf("the run is not at the publish gate: %+v", state.Gate)
	}
	return fixture, state
}

func TestApprovingThePublishGatePublishesExactlyOnce(t *testing.T) {
	fixture, state := parkedAtPublish(t, 1)
	next, err := fixture.controller.DecideGate(fixture.ctx, state.ProjectID, state.RunID, GateApproved, "ship it")
	if err != nil {
		t.Fatalf("DecideGate: %v", err)
	}
	if next.Status != RunSucceeded || next.Publication == nil {
		t.Fatalf("approval did not publish: %+v", next)
	}
	if fixture.runtime.publishes != 1 {
		t.Fatalf("published %d times, want 1", fixture.runtime.publishes)
	}
	// A second decision has nothing left to decide.
	if _, err := fixture.controller.DecideGate(
		fixture.ctx, state.ProjectID, state.RunID, GateApproved, "again",
	); err == nil {
		t.Fatal("a decided gate accepted a second decision")
	}
	if fixture.runtime.publishes != 1 {
		t.Fatalf("a second decision published again: %d", fixture.runtime.publishes)
	}
}

func TestApprovingWithoutPublishingDoesNotPublish(t *testing.T) {
	fixture, state := parkedAtPublish(t, 1)
	next, err := fixture.controller.DecideGateWithOptions(
		fixture.ctx, state.ProjectID, state.RunID, GateApproved, "accepted",
		GateDecisionOptions{SkipPublication: true},
	)
	if err != nil {
		t.Fatalf("DecideGateWithOptions: %v", err)
	}
	// The operator accepted the change and declined the outward-facing action.
	// Doing it anyway would be the worst possible reading of that.
	if fixture.runtime.publishes != 0 {
		t.Fatalf("approve-without-publish still published %d times", fixture.runtime.publishes)
	}
	if next.Status != RunSucceeded {
		t.Fatalf("the run did not complete: %+v", next)
	}
	if next.Publication != nil {
		t.Fatalf("a skipped publication recorded a publication: %+v", next.Publication)
	}
	if publish := next.Component(ComponentPublish); publish == nil || publish.Status != ComponentSucceeded {
		t.Fatalf("publish stayed unresolved: %+v", publish)
	}
}

func TestRejectingThePublishGateEndsTheRun(t *testing.T) {
	fixture, state := parkedAtPublish(t, 1)
	next, err := fixture.controller.DecideGate(
		fixture.ctx, state.ProjectID, state.RunID, GateRejected, "not this change",
	)
	if err != nil {
		t.Fatalf("DecideGate: %v", err)
	}
	if next.Status != RunFailed || next.Failure == nil || next.Failure.Reason != ReasonGateRejected {
		t.Fatalf("rejection did not end the run: %+v", next)
	}
	if fixture.runtime.publishes != 0 {
		t.Fatal("a rejected gate published")
	}
	if fixture.runtime.cleanups == 0 {
		t.Fatal("a rejected run was not cleaned up")
	}
}

func TestApprovingARepairGateBuysExactlyOneMoreRound(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	// The default board grants no automatic repair, so the run parks and the
	// operator's approval is what spends the round.
	fixture.runtime.verdicts = []verification.Verdict{verification.VerdictFailed, verification.VerdictPassed}
	state := fixture.start(t)
	if state.Gate == nil || state.Gate.Reason != ReasonWaitingForRepair {
		t.Fatalf("the run did not park at a repair gate: %+v", state.Gate)
	}
	buildsBefore := fixture.componentLaunches(ComponentBuild)

	next, err := fixture.controller.DecideGate(
		fixture.ctx, state.ProjectID, state.RunID, GateApproved, "try once more",
	)
	if err != nil {
		t.Fatalf("DecideGate: %v", err)
	}
	if fixture.componentLaunches(ComponentBuild) != buildsBefore+1 {
		t.Fatalf("the approval spent %d extra build rounds, want 1",
			fixture.componentLaunches(ComponentBuild)-buildsBefore)
	}
	if next.Gate == nil || next.Gate.ComponentID != ComponentPublish {
		t.Fatalf("the repaired run did not reach the publish gate: %+v", next.Gate)
	}
}

func TestAManualTriggerParksAndItsApprovalIsTheGo(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	fixture.setTriggerManual(t, ComponentPlan, ComponentBuild)
	state := fixture.start(t)

	if state.Gate == nil || state.Gate.Reason != ReasonWaitingForTrigger {
		t.Fatalf("a manual edge did not park the run: %+v", state.Gate)
	}
	if fixture.componentLaunches(ComponentBuild) != 0 {
		t.Fatal("a manual edge ran build anyway")
	}

	next, err := fixture.controller.DecideGate(fixture.ctx, state.ProjectID, state.RunID, GateApproved, "go")
	if err != nil {
		t.Fatalf("DecideGate: %v", err)
	}
	if fixture.componentLaunches(ComponentBuild) == 0 {
		t.Fatal("the operator's go did not start build")
	}
	// A decided trigger gate must not be re-opened, or the run would park again
	// on the answer it just received.
	if next.Gate != nil && next.Gate.Reason == ReasonWaitingForTrigger && next.Gate.Decision == "" {
		t.Fatalf("the trigger gate re-opened after its decision: %+v", next.Gate)
	}
}

func TestCancelStopsEverySiblingAndKeepsTheWork(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	state := fixture.start(t)
	// The run is parked at the publish gate, which is still cancellable.
	next, err := fixture.controller.Cancel(fixture.ctx, state.ProjectID, state.RunID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if next.Status != RunCanceled {
		t.Fatalf("cancel did not end the run: %+v", next)
	}
	// Everything the run produced is still addressable: a cancel stops a run,
	// it does not throw the work away.
	if len(next.Artifacts) == 0 {
		t.Fatal("cancel discarded the run's artifacts")
	}
	if fixture.runtime.cleanups == 0 {
		t.Fatal("a cancelled run was not cleaned up")
	}
	if _, err := fixture.controller.Cancel(fixture.ctx, state.ProjectID, state.RunID); err == nil {
		t.Fatal("a cancelled run accepted a second cancel")
	}
}

func TestARetryRerunsTheWholeCommittee(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	for index := range 3 {
		fixture.runtime.failSeats[AttemptSeatID(ComponentPlan, index)] = true
	}
	state := fixture.start(t)
	if plan := state.Component(ComponentPlan); plan == nil || plan.Status != ComponentFailed {
		t.Fatalf("the plan stage did not fail: %+v", plan)
	}
	before := fixture.componentLaunches(ComponentPlan)

	// Let the retry succeed.
	fixture.runtime.failSeats = map[string]bool{}
	next, err := fixture.controller.Retry(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	// Three workers and a refine turn, not one seat.
	if launched := fixture.componentLaunches(ComponentPlan) - before; launched != 4 {
		t.Fatalf("the retry launched %d plan turns, want 4", launched)
	}
	if next.Gate == nil || next.Gate.ComponentID != ComponentPublish {
		t.Fatalf("the retried run did not continue to the publish gate: %+v", next.Gate)
	}
}

func TestRetryIsRefusedOnAStageThatDidNotFail(t *testing.T) {
	fixture, state := parkedAtPublish(t, 1)
	if _, err := fixture.controller.Retry(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan); err == nil {
		t.Fatal("a succeeded stage accepted a retry")
	}
	if _, err := fixture.controller.Retry(fixture.ctx, state.ProjectID, state.RunID, ComponentVerify); err == nil {
		t.Fatal("a deterministic stage accepted a retry")
	}
}

func TestTakeoverAndHandbackMoveOwnershipBackAndForth(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	// Hold the build seat open so there is a live attempt to take.
	fixture.runtime.holdSeat = AttemptSeatID(ComponentBuild, 0)
	go func() { _, _ = fixture.controller.Start(fixture.ctx, fixture.startRequest(t)) }()

	attempt := fixture.awaitLiveAttempt(t, ComponentBuild)
	state, err := fixture.controller.Takeover(fixture.ctx, "demo", attempt.runID, attempt.attemptID)
	if err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	// The prior automated attempt is still live — a takeover resumes the turn
	// rather than killing it — so the operator's attempt is found by ownership.
	taken := attemptByOwnership(state, ComponentBuild, OwnedByHuman)
	if taken == nil {
		t.Fatalf("takeover did not hand an attempt to the operator: %+v", state.Component(ComponentBuild))
	}
	// The provider session is resumed, not replaced: a new session would be a
	// second, unrelated conversation.
	if taken.Session == nil || taken.Session.ID != "session-"+attempt.seatID {
		t.Fatalf("takeover did not resume the provider session: %+v", taken.Session)
	}

	if _, err := fixture.controller.Handback(fixture.ctx, "demo", attempt.runID, taken.AttemptID); err != nil {
		t.Fatalf("Handback: %v", err)
	}
	// The released turn finishes on the goroutine that started the run, so the
	// assertion waits for the state rather than racing it.
	fixture.awaitNoLiveAttempt(t, attempt.runID, ComponentBuild)
}

func TestTakeoverIsRefusedWithoutALiveAttempt(t *testing.T) {
	fixture, state := parkedAtPublish(t, 1)
	if _, err := fixture.controller.Takeover(fixture.ctx, state.ProjectID, state.RunID, "no-such-attempt"); err == nil {
		t.Fatal("takeover accepted an unknown attempt")
	}
	// Every attempt in a parked run has exited.
	for _, component := range SeatComponentIDs {
		current := state.Component(component)
		if current == nil || current.Attempt == nil {
			continue
		}
		if _, err := fixture.controller.Takeover(
			fixture.ctx, state.ProjectID, state.RunID, current.Attempt.AttemptID,
		); err == nil {
			t.Fatalf("takeover accepted the exited attempt %s", current.Attempt.AttemptID)
		}
	}
}

func TestAControlIsRefusedOnATerminalRun(t *testing.T) {
	fixture, state := parkedAtPublish(t, 1)
	if _, err := fixture.controller.DecideGate(
		fixture.ctx, state.ProjectID, state.RunID, GateApproved, "ship it",
	); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"cancel": func() error {
			_, err := fixture.controller.Cancel(fixture.ctx, state.ProjectID, state.RunID)
			return err
		},
		"retry": func() error {
			_, err := fixture.controller.Retry(fixture.ctx, state.ProjectID, state.RunID, ComponentBuild)
			return err
		},
		"gate": func() error {
			_, err := fixture.controller.DecideGate(fixture.ctx, state.ProjectID, state.RunID, GateApproved, "again")
			return err
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("%s was accepted on a finished run", name)
		}
	}
}
