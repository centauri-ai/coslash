package atlas

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

func testEvent(t *testing.T, seq uint64, payload Payload) runfs.Event {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return runfs.Event{Seq: seq, At: time.Date(2026, 8, 9, 0, 0, int(seq), 0, time.UTC), Type: payload.EventType(), Data: data}
}

func TestReduceDeterministicallyReplaysCommitteeSessions(t *testing.T) {
	claude := contracts.SessionIdentity{Agent: "claude", ID: "shared-id"}
	codex := contracts.SessionIdentity{Agent: "codex", ID: "shared-id"}
	payloads := []Payload{
		&RunCreated{ProjectID: "project", BoardID: "board", BoardRevision: 1, Title: "run"},
		&ComponentReadyEvent{ComponentID: ComponentPlan, Instance: 1},
		&AttemptLaunchRequested{ComponentID: ComponentPlan, Instance: 1, SeatID: "plan-worker-1", Attempt: 1, AttemptID: "attempt-1", Session: &claude},
		&AttemptLaunched{ComponentID: ComponentPlan, Instance: 1, SeatID: "plan-worker-1", Attempt: 1, AttemptID: "attempt-1", Session: &claude},
		&AttemptLaunchRequested{ComponentID: ComponentPlan, Instance: 1, SeatID: "plan-worker-2", Attempt: 1, AttemptID: "attempt-2"},
		&AttemptLaunched{ComponentID: ComponentPlan, Instance: 1, SeatID: "plan-worker-2", Attempt: 1, AttemptID: "attempt-2"},
		&AttemptSessionBound{ComponentID: ComponentPlan, Instance: 1, SeatID: "plan-worker-2", Attempt: 1, AttemptID: "attempt-2", Session: codex},
		&ComponentSucceededEvent{ComponentID: ComponentPlan, Instance: 1, Outputs: []string{"PLAN.md"}},
	}
	events := make([]runfs.Event, len(payloads))
	for index, payload := range payloads {
		events[index] = testEvent(t, uint64(index+1), payload)
	}
	first, err := Reduce("run-20260809t000000-abcdef12", events)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Reduce("run-20260809t000000-abcdef12", events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("replay was not deterministic")
	}
	if !reflect.DeepEqual(first.Sessions, []contracts.SessionIdentity{claude, codex}) {
		t.Fatalf("composite sessions = %+v", first.Sessions)
	}
	if got := len(first.Component(ComponentPlan).Attempts); got != 2 {
		t.Fatalf("committee attempts = %d, want 2", got)
	}
}

func TestReduceKeepsSiblingArtifactsAcrossRetry(t *testing.T) {
	artifact := ArtifactRecord{ArtifactID: "artifact-1", Name: "draft.md", Path: "artifacts/draft.md", Producer: ArtifactProducer{ComponentID: ComponentPlan, Instance: 1, SeatID: "worker-2", Attempt: 1}}
	payloads := []Payload{
		&RunCreated{ProjectID: "project", BoardID: "board", BoardRevision: 1, Title: "run"},
		&ComponentReadyEvent{ComponentID: ComponentPlan, Instance: 1},
		&ArtifactPromoted{Artifact: artifact},
		&ComponentFailedEvent{ComponentID: ComponentPlan, Instance: 1, Reason: "worker_failed"},
		&ComponentReadyEvent{ComponentID: ComponentPlan, Instance: 2, Reason: "retry"},
	}
	events := make([]runfs.Event, len(payloads))
	for index, payload := range payloads {
		events[index] = testEvent(t, uint64(index+1), payload)
	}
	state, err := Reduce("run-20260809t000000-abcdef12", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Artifacts) != 1 || state.Artifacts[0].Producer.SeatID != "worker-2" {
		t.Fatalf("sibling artifact was lost: %+v", state.Artifacts)
	}
	if state.Component(ComponentPlan).Instance != 2 || state.Component(ComponentPlan).Status != ComponentReady {
		t.Fatalf("retry state = %+v", state.Component(ComponentPlan))
	}
}
