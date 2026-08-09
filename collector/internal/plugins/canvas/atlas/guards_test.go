package atlas

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
)

// The guards are the single source of truth three callers share, so what these
// tests pin is the exact boundary of each transition — both that it is offered
// when it should be, and that it is refused when it should not.

func committeeRun(t *testing.T, attempts ...AttemptState) *RunState {
	t.Helper()
	components := map[ComponentID]*ComponentRunState{}
	for _, id := range ComponentIDs {
		components[id] = &ComponentRunState{ID: id, Status: ComponentBlocked, Instance: 1}
	}
	if len(attempts) > 0 {
		plan := components[ComponentPlan]
		plan.Status = ComponentRunning
		plan.Attempts = attempts
		plan.Attempt = &attempts[len(attempts)-1]
	}
	return &RunState{
		SchemaVersion: RunSchemaVersion,
		RunID:         "run-20260809t070000-0a1b2c3d",
		ProjectID:     "demo",
		BoardID:       "board-1",
		Status:        RunRunning,
		Components:    components,
	}
}

func workerAttempt(seat string, status AttemptStatus, ownership Ownership, session string) AttemptState {
	attempt := AttemptState{
		AttemptID:   "run-1-plan-1-" + seat,
		ComponentID: ComponentPlan,
		Instance:    1,
		SeatID:      seat,
		Attempt:     1,
		Ownership:   ownership,
		Status:      status,
	}
	if session != "" {
		attempt.Session = &contracts.SessionIdentity{Agent: "claude", ID: session}
	}
	return attempt
}

func TestRetryIsOfferedOnlyForAFailedCommitteeStage(t *testing.T) {
	state := committeeRun(t)
	state.Components[ComponentPlan].Status = ComponentFailed
	if err := CanRetry(state, ComponentPlan); err != nil {
		t.Fatalf("a failed committee stage refused retry: %v", err)
	}

	state.Components[ComponentPlan].Status = ComponentRunning
	if CanRetry(state, ComponentPlan) == nil {
		t.Fatal("a running stage accepted retry")
	}
	// A committee is retried whole; a deterministic stage has no committee.
	state.Components[ComponentVerify].Status = ComponentFailed
	if CanRetry(state, ComponentVerify) == nil {
		t.Fatal("a deterministic stage accepted retry")
	}
	state.Components[ComponentPlan].Status = ComponentFailed
	state.Status = RunCanceled
	if CanRetry(state, ComponentPlan) == nil {
		t.Fatal("a terminal run accepted retry")
	}
}

func TestTakeoverNeedsARunningAttemptWithAProviderSession(t *testing.T) {
	live := workerAttempt("plan-1", AttemptStatusRunning, OwnedByController, "session-1")
	state := committeeRun(t, live)
	if err := CanTakeover(state, live.AttemptID); err != nil {
		t.Fatalf("a running attempt refused takeover: %v", err)
	}

	for _, attempt := range []AttemptState{
		workerAttempt("plan-1", AttemptStatusRunning, OwnedByController, ""),
		workerAttempt("plan-1", AttemptStatusLaunchRequested, OwnedByController, "session-1"),
		workerAttempt("plan-1", AttemptStatusExited, OwnedByController, "session-1"),
		workerAttempt("plan-1", AttemptStatusRunning, OwnedByHuman, "session-1"),
	} {
		if CanTakeover(committeeRun(t, attempt), attempt.AttemptID) == nil {
			t.Fatalf("takeover was accepted for %+v", attempt)
		}
	}
}

func TestHandbackNeedsALiveHumanControlledAttempt(t *testing.T) {
	human := workerAttempt("plan-1", AttemptStatusRunning, OwnedByHuman, "session-1")
	if err := CanHandback(committeeRun(t, human), human.AttemptID); err != nil {
		t.Fatalf("a human-controlled attempt refused handback: %v", err)
	}
	automated := workerAttempt("plan-1", AttemptStatusRunning, OwnedByController, "session-1")
	if CanHandback(committeeRun(t, automated), automated.AttemptID) == nil {
		t.Fatal("an automated attempt accepted handback")
	}
	exited := workerAttempt("plan-1", AttemptStatusExited, OwnedByHuman, "session-1")
	if CanHandback(committeeRun(t, exited), exited.AttemptID) == nil {
		t.Fatal("an exited attempt accepted handback")
	}
}

func TestAnAttemptIsAddressableAcrossTheWholeFanOut(t *testing.T) {
	// A committee means the attempt id is not addressable by component alone,
	// so a control that names a sibling must still find it.
	first := workerAttempt("plan-1", AttemptStatusRunning, OwnedByController, "session-1")
	second := workerAttempt("plan-2", AttemptStatusRunning, OwnedByController, "session-2")
	third := workerAttempt("plan-3", AttemptStatusRunning, OwnedByController, "session-3")
	state := committeeRun(t, first, second, third)

	for _, attempt := range []AttemptState{first, second, third} {
		if err := CanTakeover(state, attempt.AttemptID); err != nil {
			t.Fatalf("sibling %s was not addressable: %v", attempt.SeatID, err)
		}
	}
	if CanTakeover(state, "run-1-plan-1-plan-9") == nil {
		t.Fatal("an unknown attempt was accepted")
	}
}

func TestLiveAttemptsReportsEverySiblingACancelMustStop(t *testing.T) {
	running := workerAttempt("plan-1", AttemptStatusRunning, OwnedByController, "session-1")
	launching := workerAttempt("plan-2", AttemptStatusLaunchRequested, OwnedByController, "")
	exited := workerAttempt("plan-3", AttemptStatusExited, OwnedByController, "session-3")
	live := LiveAttempts(committeeRun(t, running, launching, exited))
	if len(live) != 2 {
		t.Fatalf("LiveAttempts returned %d attempts, want 2: %+v", len(live), live)
	}
}

func TestCancelIsRefusedOnlyOnceTheRunHasFinished(t *testing.T) {
	state := committeeRun(t)
	if err := CanCancel(state); err != nil {
		t.Fatalf("a running run refused cancel: %v", err)
	}
	for _, status := range []RunStatus{RunSucceeded, RunFailed, RunCanceled, RunInterruptedImport} {
		state.Status = status
		if CanCancel(state) == nil {
			t.Fatalf("a %s run accepted cancel", status)
		}
	}
}

func TestGateDecisionsRefuseAStaleApproval(t *testing.T) {
	state := committeeRun(t)
	state.Status = RunAwaitingApproval
	revision := uint64(2)
	state.Gate = &GateRecord{ComponentID: ComponentPublish, Instance: 1, ChangeRevision: &revision}
	state.Change = &ChangeRecord{ChangeRevision: 2}
	if err := CanDecideGate(state); err != nil {
		t.Fatalf("a current gate refused a decision: %v", err)
	}

	// The change moved on, so approving would attest a revision the operator
	// never saw.
	state.Change = &ChangeRecord{ChangeRevision: 3}
	if CanDecideGate(state) == nil {
		t.Fatal("a stale gate accepted a decision")
	}

	state.Change = &ChangeRecord{ChangeRevision: 2}
	state.Gate.Decision = GateApproved
	if CanDecideGate(state) == nil {
		t.Fatal("an already decided gate accepted a second decision")
	}
	state.Gate = nil
	if CanDecideGate(state) == nil {
		t.Fatal("a run with no gate accepted a decision")
	}
}

func TestOnlyOneRunPerProjectMayBeLive(t *testing.T) {
	// Two live runs would race for the same publication target, and the
	// operator could not tell which one a pull request came from.
	if err := CanStartRun(nil); err != nil {
		t.Fatalf("an empty project refused a run: %v", err)
	}
	finished := []RunSummary{
		{RunID: "a", Status: RunSucceeded},
		{RunID: "b", Status: RunFailed},
		{RunID: "c", Status: RunCanceled},
		{RunID: "d", Status: RunInterruptedImport},
	}
	if err := CanStartRun(finished); err != nil {
		t.Fatalf("a project with only finished runs refused a run: %v", err)
	}
	for _, status := range []RunStatus{RunPreparing, RunRunning, RunAwaitingApproval} {
		if CanStartRun(append(finished, RunSummary{RunID: "live", Status: status})) == nil {
			t.Fatalf("a project with a %s run accepted a second run", status)
		}
	}
}

func TestAnImportedRunIsTerminalAndAcceptsNoControl(t *testing.T) {
	// A migrated run is history. Resuming one would start an agent turn against
	// a revision from another machine's past.
	state := committeeRun(t, workerAttempt("plan-1", AttemptStatusRunning, OwnedByController, "session-1"))
	state.Status = RunInterruptedImport
	if !state.IsTerminal() {
		t.Fatal("an imported run is not terminal")
	}
	if CanCancel(state) == nil {
		t.Fatal("an imported run accepted cancel")
	}
	if CanRetry(state, ComponentPlan) == nil {
		t.Fatal("an imported run accepted retry")
	}
	if CanTakeover(state, "run-1-plan-1-plan-1") == nil {
		t.Fatal("an imported run accepted takeover")
	}
}
