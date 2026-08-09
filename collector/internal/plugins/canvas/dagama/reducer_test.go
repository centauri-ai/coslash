package dagama

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

const testRunID = "run-20260809t004512-0a1b2c3d"

var epoch = time.Date(2026, 8, 9, 0, 45, 12, 0, time.UTC)

// eventsOf turns payloads into a durable log with monotonic sequences and
// deterministic timestamps.
func eventsOf(t *testing.T, payloads ...Payload) []runfs.Event {
	t.Helper()
	events := make([]runfs.Event, 0, len(payloads))
	for index, payload := range payloads {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %T: %v", payload, err)
		}
		events = append(events, runfs.Event{
			Seq:  uint64(index + 1),
			At:   epoch.Add(time.Duration(index) * time.Second),
			Type: payload.EventType(),
			Data: encoded,
		})
	}
	return events
}

func componentRef(id ComponentID, instance int) ComponentInstance {
	return ComponentInstance{ComponentID: id, Instance: instance}
}

func attemptRef(id ComponentID, instance int, attemptID string) AttemptRef {
	return AttemptRef{
		ComponentInstance: componentRef(id, instance),
		SeatID:            "seat-1", Attempt: 1, AttemptID: attemptID,
	}
}

func createdRun() Payload {
	return &RunCreated{
		ProjectID: "project-1", BoardID: "board-1", BoardRevision: 2, Title: "Ship it",
		Source: SourceRecord{Kind: "text", Title: "task", Bytes: 4, Sha256: "abc"},
	}
}

func mustReduce(t *testing.T, events []runfs.Event) *RunState {
	t.Helper()
	state, err := Reduce(testRunID, events)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	return state
}

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

func TestReplayIsDeterministic(t *testing.T) {
	events := eventsOf(t,
		createdRun(),
		&RunRootCreated{RunRoot: "/runs/run-1", Branch: "dagama/run-1", BaseBranch: "main", BaseSha: "abc"},
		&ComponentReady{ComponentInstance: componentRef(ComponentBuild, 1)},
		&AttemptLaunchRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "tmux-1"},
		&AttemptLaunched{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "tmux-1"},
		&AttemptExited{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), ExitCode: 0, FinishedAt: epoch},
		&ComponentSucceeded{ComponentInstance: componentRef(ComponentBuild, 1), Outputs: []string{"IMPLEMENTATION.md"}},
		&ChangeCaptured{ChangeRevision: 1, TreeOID: "tree", PatchSha256: "digest", BaseSha: "abc"},
	)

	first := mustReduce(t, events)
	second := mustReduce(t, events)

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("two reductions of the same log disagree")
	}

	// Reduction reads no clock: a replay hours later must produce the same bytes.
	time.Sleep(time.Millisecond)
	third := mustReduce(t, events)
	thirdJSON, err := json.Marshal(third)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(firstJSON) != string(thirdJSON) {
		t.Fatal("reduction depends on wall-clock time")
	}
}

func TestReduceIncrementallyMatchesFullReplay(t *testing.T) {
	payloads := []Payload{
		createdRun(),
		&ComponentReady{ComponentInstance: componentRef(ComponentPlan, 1)},
		&ComponentStarted{ComponentInstance: componentRef(ComponentPlan, 1)},
		&ComponentSucceeded{ComponentInstance: componentRef(ComponentPlan, 1), Outputs: []string{"PLAN.md"}},
		&ComponentReady{ComponentInstance: componentRef(ComponentBuild, 1)},
	}
	events := eventsOf(t, payloads...)

	// Prefix reductions must agree with the full replay at every point, which is
	// what makes the materialized view safe to update after each append.
	for cut := 1; cut <= len(events); cut++ {
		prefix := mustReduce(t, events[:cut])
		if prefix.LastSeq != uint64(cut) {
			t.Fatalf("prefix %d LastSeq = %d", cut, prefix.LastSeq)
		}
	}
	full := mustReduce(t, events)
	if full.Components[ComponentPlan].Status != ComponentSucceededStatus {
		t.Fatalf("plan status = %q", full.Components[ComponentPlan].Status)
	}
}

// ---------------------------------------------------------------------------
// Invariants
// ---------------------------------------------------------------------------

func TestRepairInstanceClearsPriorOutputs(t *testing.T) {
	events := eventsOf(t,
		createdRun(),
		&ComponentReady{ComponentInstance: componentRef(ComponentBuild, 1)},
		&ComponentSucceeded{ComponentInstance: componentRef(ComponentBuild, 1), Outputs: []string{"old.md"}},
		&ComponentReady{ComponentInstance: componentRef(ComponentBuild, 2)},
	)
	state := mustReduce(t, events)

	build := state.Components[ComponentBuild]
	// Stale outputs would let a later gate read an artifact from an earlier revision.
	if len(build.Outputs) != 0 {
		t.Fatalf("outputs = %v, want cleared for the new instance", build.Outputs)
	}
	if build.Instance != 2 || build.Attempt != nil || build.FinishedAt != nil {
		t.Fatalf("build = %+v, want a reset instance", build)
	}
}

func TestSessionBindingNeverRetargetsAnotherAttempt(t *testing.T) {
	bound := &AttemptSessionBound{
		AttemptRef: attemptRef(ComponentBuild, 1, "attempt-OTHER"),
		SessionID:  "11111111-1111-4111-8111-111111111111",
	}
	events := eventsOf(t,
		createdRun(),
		&AttemptLaunchRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "tmux-1"},
		bound,
	)
	state := mustReduce(t, events)

	// Binding a different attempt id would silently retarget takeover onto
	// somebody else's live session.
	if got := state.Components[ComponentBuild].Attempt.SessionID; got != "" {
		t.Fatalf("SessionID = %q, want the binding ignored", got)
	}
}

func TestSessionBindingDoesNotOverwriteADifferentSession(t *testing.T) {
	events := eventsOf(t,
		createdRun(),
		&AttemptLaunchRequested{
			AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"),
			TmuxName:   "tmux-1", SessionID: "11111111-1111-4111-8111-111111111111",
		},
		&AttemptSessionBound{
			AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"),
			SessionID:  "22222222-2222-4222-8222-222222222222",
		},
	)
	state := mustReduce(t, events)
	if got := state.Components[ComponentBuild].Attempt.SessionID; got != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("SessionID = %q, want the original binding kept", got)
	}
}

func TestAttemptLaunchedPreservesRequestStartAndOwnership(t *testing.T) {
	events := eventsOf(t,
		createdRun(),
		&AttemptLaunchRequested{
			AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"),
			TmuxName:   "tmux-1", Ownership: OwnershipHumanControlled,
		},
		&AttemptLaunched{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "tmux-1"},
	)
	state := mustReduce(t, events)

	attempt := state.Components[ComponentBuild].Attempt
	if attempt.Ownership != OwnershipHumanControlled {
		t.Fatalf("Ownership = %q, want the request's ownership carried forward", attempt.Ownership)
	}
	if !attempt.StartedAt.Equal(epoch.Add(time.Second)) {
		t.Fatalf("StartedAt = %v, want the request time", attempt.StartedAt)
	}
}

func TestPublishGatePausesTheRunAndRepairGateDoesNot(t *testing.T) {
	publish := mustReduce(t, eventsOf(t,
		createdRun(),
		&GateOpened{ComponentInstance: componentRef(ComponentPublish, 1), Reason: "approval", Message: "ready"},
	))
	if publish.Status != RunAwaitingApproval {
		t.Fatalf("status = %q, want awaiting_approval", publish.Status)
	}

	repair := mustReduce(t, eventsOf(t,
		createdRun(),
		&ComponentSucceeded{ComponentInstance: componentRef(ComponentPlan, 1), Outputs: nil},
		&GateOpened{ComponentInstance: componentRef(ComponentBuild, 2), Reason: "repair_exhausted", Message: "two rounds"},
	))
	// A repair gate leaves the run running so the user can still inspect live
	// seats and retry.
	if repair.Status != RunRunning {
		t.Fatalf("status = %q, want running", repair.Status)
	}
}

func TestGateRejectionFailsTheRun(t *testing.T) {
	state := mustReduce(t, eventsOf(t,
		createdRun(),
		&GateOpened{ComponentInstance: componentRef(ComponentPublish, 1), Reason: "approval", Message: "ready"},
		&GateDecided{
			ComponentInstance: componentRef(ComponentPublish, 1),
			Decision:          GateRejected, Message: "not this one",
		},
	))

	if state.Status != RunFailed || state.Failure == nil || state.Failure.Reason != "gate_rejected" {
		t.Fatalf("state = %+v, want a rejected run", state.Failure)
	}
	if state.Components[ComponentPublish].Status != ComponentFailedStatus {
		t.Fatalf("publish status = %q", state.Components[ComponentPublish].Status)
	}
	if state.Gate.Decision != GateRejected || state.Gate.DecidedAt == nil {
		t.Fatalf("gate = %+v, want a decided gate", state.Gate)
	}
}

func TestRepairGateApprovalRestoresTheComponent(t *testing.T) {
	state := mustReduce(t, eventsOf(t,
		createdRun(),
		&GateOpened{ComponentInstance: componentRef(ComponentVerify, 1), Reason: "repair_exhausted", Message: "two rounds"},
		&GateDecided{ComponentInstance: componentRef(ComponentVerify, 1), Decision: GateApproved, Message: "continue"},
	))

	verify := state.Components[ComponentVerify]
	// Verify already produced its last artifacts, so approval restores succeeded
	// and clears the waiting reason so the card drops the gate UI.
	if verify.Status != ComponentSucceededStatus || verify.Reason != "" || verify.Message != "" {
		t.Fatalf("verify = %+v, want a restored component", verify)
	}
	if state.Status == RunFailed {
		t.Fatal("an approved repair gate failed the run")
	}
}

func TestPublishApprovalResumesTheRun(t *testing.T) {
	state := mustReduce(t, eventsOf(t,
		createdRun(),
		&GateOpened{ComponentInstance: componentRef(ComponentPublish, 1), Reason: "approval", Message: "ready"},
		&GateDecided{ComponentInstance: componentRef(ComponentPublish, 1), Decision: GateApproved, Message: "go"},
	))
	if state.Status != RunRunning {
		t.Fatalf("status = %q, want running", state.Status)
	}
	if state.Components[ComponentPublish].Status != ComponentRunning {
		t.Fatalf("publish status = %q", state.Components[ComponentPublish].Status)
	}
}

func TestRunFinishedClearsFailureOnlyOnSuccess(t *testing.T) {
	succeeded := mustReduce(t, eventsOf(t,
		createdRun(),
		&ComponentFailed{ComponentInstance: componentRef(ComponentBuild, 1), Reason: "x", Message: "y"},
		&RunFinished{Status: RunSucceeded},
	))
	if succeeded.Failure != nil {
		t.Fatalf("Failure = %+v, want cleared on success", succeeded.Failure)
	}

	canceled := mustReduce(t, eventsOf(t,
		createdRun(),
		&RunFinished{Status: RunCanceled, Reason: "user", Message: "stopped"},
	))
	if canceled.Failure == nil || canceled.Failure.Reason != "user" {
		t.Fatalf("Failure = %+v, want recorded on cancel", canceled.Failure)
	}
}

func TestIntentEventsDoNotChangeComponentState(t *testing.T) {
	before := mustReduce(t, eventsOf(t,
		createdRun(),
		&AttemptLaunchRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "tmux-1"},
	))
	after := mustReduce(t, eventsOf(t,
		createdRun(),
		&AttemptLaunchRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "tmux-1"},
		&CancelRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1")},
		&TakeoverRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), PriorAttemptID: "attempt-0"},
	))

	// Intents record that something was asked for; the effect follows in the
	// controller before the matching fact.
	if after.Components[ComponentBuild].Status != before.Components[ComponentBuild].Status {
		t.Fatal("an intent event changed component status")
	}
	if after.Components[ComponentBuild].Attempt.Status != AttemptLaunchRequestedStatus {
		t.Fatal("an intent event changed attempt status")
	}
}

func TestHandbackReturnsOwnershipToAutomation(t *testing.T) {
	state := mustReduce(t, eventsOf(t,
		createdRun(),
		&AttemptLaunchRequested{
			AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"),
			TmuxName:   "tmux-1", Ownership: OwnershipHumanControlled,
		},
		&HandbackCompleted{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1")},
	))
	if got := state.Components[ComponentBuild].Attempt.Ownership; got != OwnershipAutomated {
		t.Fatalf("Ownership = %q, want automated", got)
	}
}

func TestGateOpenedInheritsTheCurrentChangeRevision(t *testing.T) {
	state := mustReduce(t, eventsOf(t,
		createdRun(),
		&ChangeCaptured{ChangeRevision: 4, TreeOID: "tree", PatchSha256: "digest", BaseSha: "abc"},
		&GateOpened{ComponentInstance: componentRef(ComponentPublish, 1), Reason: "approval", Message: "ready"},
	))
	if state.Gate.ChangeRevision == nil || *state.Gate.ChangeRevision != 4 {
		t.Fatalf("gate revision = %v, want the captured revision", state.Gate.ChangeRevision)
	}
}

func TestArtifactsAccumulateInOrder(t *testing.T) {
	first := ArtifactRecord{ArtifactID: "a1", Name: "PLAN.md"}
	second := ArtifactRecord{ArtifactID: "a2", Name: "IMPLEMENTATION.md"}
	state := mustReduce(t, eventsOf(t,
		createdRun(),
		&ArtifactPromoted{Artifact: first},
		&ArtifactPromoted{Artifact: second},
	))
	if len(state.Artifacts) != 2 || state.Artifacts[0].ArtifactID != "a1" || state.Artifacts[1].ArtifactID != "a2" {
		t.Fatalf("artifacts = %+v", state.Artifacts)
	}
}

// ---------------------------------------------------------------------------
// Corruption
// ---------------------------------------------------------------------------

func TestReduceRejectsAnUnknownEventType(t *testing.T) {
	events := []runfs.Event{{Seq: 1, At: epoch, Type: "invented_by_a_newer_build", Data: json.RawMessage(`{}`)}}
	// Skipping the event would materialize a state that never existed.
	if _, err := Reduce(testRunID, events); codeOf(t, err) != CodeCorruptDocument {
		t.Fatal("an unknown event type was accepted")
	}
}

func TestReduceRejectsAMalformedPayload(t *testing.T) {
	events := []runfs.Event{{
		Seq: 1, At: epoch, Type: EventRunCreated,
		Data: json.RawMessage(`{"boardRevision": "not a number"}`),
	}}
	if _, err := Reduce(testRunID, events); codeOf(t, err) != CodeCorruptDocument {
		t.Fatal("a malformed payload was accepted")
	}
}

func TestReduceOfAnEmptyLogIsTheEmptyState(t *testing.T) {
	state := mustReduce(t, nil)
	if state.Status != RunPreparing || state.LastSeq != 0 {
		t.Fatalf("state = %+v", state)
	}
	if len(state.Components) != len(ComponentIDs) {
		t.Fatalf("components = %d, want the full pipeline", len(state.Components))
	}
	for _, id := range ComponentIDs {
		if state.Components[id].Status != ComponentBlocked {
			t.Errorf("%s = %q, want blocked", id, state.Components[id].Status)
		}
	}
}

func TestReduceDoesNotAliasCallerSlices(t *testing.T) {
	outputs := []string{"PLAN.md"}
	state := mustReduce(t, eventsOf(t,
		createdRun(),
		&ComponentSucceeded{ComponentInstance: componentRef(ComponentPlan, 1), Outputs: outputs},
	))
	outputs[0] = "MUTATED"
	if got := state.Components[ComponentPlan].Outputs[0]; got != "PLAN.md" {
		t.Fatalf("materialized outputs aliased the caller's slice: %q", got)
	}
}
