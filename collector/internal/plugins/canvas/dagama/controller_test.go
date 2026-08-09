package dagama

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/publication"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

type fakeControllerRuntime struct {
	mu              sync.Mutex
	root            string
	artifacts       map[string][]byte
	missing         map[ComponentID]bool
	verifyVerdicts  []verification.Verdict
	reviewVerdicts  []ReviewVerdict
	reviewerMutated bool
	handbackMissing bool
	executions      map[ComponentID]int
	models          map[ComponentID]string
	sessionAgent    string
	publishes       int
	cancels         int
	rearms          int
	probe           ProbeResult
	executeStarted  chan struct{}
	executeRelease  chan struct{}
}

func newFakeControllerRuntime(root string) *fakeControllerRuntime {
	return &fakeControllerRuntime{root: root, artifacts: map[string][]byte{}, missing: map[ComponentID]bool{}, executions: map[ComponentID]int{}, models: map[ComponentID]string{}}
}

func (f *fakeControllerRuntime) Prepare(_ context.Context, request PrepareRequest) (PreparedRun, error) {
	if err := os.MkdirAll(request.RunRoot, 0o700); err != nil {
		return PreparedRun{}, err
	}
	return PreparedRun{Root: revision.RunRoot{Path: request.RunRoot, Branch: request.Branch, BaseSha: testOID('a'), PublishBaseBranch: "main", PublishBaseSha: testOID('a')}, RemoteURL: "https://github.com/example/repo.git"}, nil
}

func (f *fakeControllerRuntime) RecordControllerArtifact(_ context.Context, _ string, name string, contents []byte, producer ProducerRef) (ArtifactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record(name, string(producer.Component), producer.Instance, producer.Attempt, contents), nil
}

func (f *fakeControllerRuntime) ReadArtifact(_ context.Context, _ string, record ArtifactRecord) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	contents, ok := f.artifacts[record.ArtifactID]
	if !ok {
		return nil, fmt.Errorf("artifact missing")
	}
	return append([]byte(nil), contents...), nil
}

func (f *fakeControllerRuntime) Execute(_ context.Context, request AttemptRequest, launched LaunchRecorder) (AttemptResult, error) {
	f.mu.Lock()
	f.executions[request.Component]++
	f.models[request.Component] = request.Seat.Model
	agent := string(request.Seat.Vendor)
	if f.sessionAgent != "" {
		agent = f.sessionAgent
	}
	result := AttemptResult{Session: contracts.SessionIdentity{Agent: agent, ID: "session-" + string(request.Component)}, FinishedAt: time.Unix(20, 0).UTC()}
	f.mu.Unlock()
	if err := launched(result.Session); err != nil {
		return AttemptResult{}, err
	}
	if f.executeStarted != nil {
		select {
		case f.executeStarted <- struct{}{}:
		default:
		}
		<-f.executeRelease
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing[request.Component] {
		return result, nil
	}
	switch request.Component {
	case ComponentPlan:
		result.Artifacts = []ArtifactRecord{f.record("PLAN.md", "plan", request.Instance, request.Attempt, []byte("plan\n"))}
	case ComponentBuild:
		result.Artifacts = []ArtifactRecord{f.record("IMPLEMENTATION.md", "implementation", request.Instance, request.Attempt, []byte("implemented\n"))}
		result.Change = &revision.CapturedRevision{Revision: revision.Revision{ChangeRevision: uint64(request.Instance), TreeOID: testOID('b'), PatchSha256: testDigest("patch" + fmt.Sprint(request.Instance)), PatchBytes: 12, ChangedFiles: []revision.ChangedFile{{Path: "README.md", Status: revision.StatusModified}}, Insertions: 1}, Patch: []byte("diff")}
		result.Artifacts = append(result.Artifacts, f.record("CHANGESET.patch", "patch", request.Instance, request.Attempt, []byte("diff\n")))
	case ComponentReview:
		verdict := ReviewApproved
		if len(f.reviewVerdicts) > 0 {
			verdict = f.reviewVerdicts[0]
			f.reviewVerdicts = f.reviewVerdicts[1:]
		}
		raw := struct {
			SchemaVersion uint64          `json:"schemaVersion"`
			Verdict       ReviewVerdict   `json:"verdict"`
			Summary       string          `json:"summary"`
			Findings      []ReviewFinding `json:"findings"`
		}{SchemaVersion: 1, Verdict: verdict, Summary: "reviewed", Findings: []ReviewFinding{}}
		encoded, _ := json.Marshal(raw)
		result.ReviewerMutated = f.reviewerMutated
		result.Artifacts = []ArtifactRecord{f.record("REVIEW.md", "review", request.Instance, request.Attempt, []byte("approved\n")), f.record("review.json", "review_outcome", request.Instance, request.Attempt, encoded)}
	}
	return result, nil
}
func (f *fakeControllerRuntime) Release(context.Context, AttemptState) error { return nil }

func (f *fakeControllerRuntime) Verify(_ context.Context, request VerifyRequest) (verification.Document, ArtifactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	verdict := verification.VerdictPassed
	if len(f.verifyVerdicts) > 0 {
		verdict = f.verifyVerdicts[0]
		f.verifyVerdicts = f.verifyVerdicts[1:]
	}
	document := verification.Document{SchemaVersion: 1, ChangeRevision: request.Change.ChangeRevision, PatchSha256: request.Change.PatchSha256, Verdict: verdict, Checks: []verification.Result{}, StartedAt: time.Unix(30, 0).UTC(), FinishedAt: time.Unix(31, 0).UTC()}
	encoded, _ := json.Marshal(document)
	return document, f.record("verification.json", "verification", request.Instance, 0, encoded), nil
}

func (f *fakeControllerRuntime) Publish(_ context.Context, request PublishRequest) (publication.Record, ArtifactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishes++
	record := publication.Record{ChangeRevision: request.State.Change.ChangeRevision, CommitSha: testOID('c'), Branch: request.State.Branch, Remote: request.State.RemoteURL, PRURL: "https://github.com/example/repo/pull/1", PRNumber: 1, Action: publication.ActionCreated, IdempotencyKey: publication.IdempotencyKey(request.State.RunID, request.State.Change.ChangeRevision), PublishedAt: time.Unix(40, 0).UTC()}
	encoded, _ := json.Marshal(record)
	return record, f.record("publication.json", "publication", 1, 0, encoded), nil
}

func (f *fakeControllerRuntime) Cancel(_ context.Context, _ *RunState) (*ArtifactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	record := f.record("cancel-snapshot.patch", "cancel_snapshot", 1, 1, []byte("partial\n"))
	return &record, nil
}

func (f *fakeControllerRuntime) Takeover(_ context.Context, request AttemptRequest, prior AttemptState) (AttemptResult, error) {
	return AttemptResult{Session: contracts.SessionIdentity{Agent: string(request.Seat.Vendor), ID: prior.SessionID}}, nil
}
func (f *fakeControllerRuntime) Handback(_ context.Context, request AttemptRequest, _ AttemptState) (AttemptResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handbackMissing {
		return AttemptResult{FinishedAt: time.Unix(50, 0).UTC()}, nil
	}
	return AttemptResult{Artifacts: []ArtifactRecord{f.record("PLAN.md", "plan", request.Instance, request.Attempt, []byte("human plan\n"))}, FinishedAt: time.Unix(50, 0).UTC()}, nil
}
func (f *fakeControllerRuntime) Probe(context.Context, *RunState, AttemptState) (ProbeResult, error) {
	return f.probe, nil
}
func (f *fakeControllerRuntime) Rearm(context.Context, *RunState, AttemptState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rearms++
	return nil
}
func (f *fakeControllerRuntime) Cleanup(context.Context, *RunState) error { return nil }

func (f *fakeControllerRuntime) record(name, kind string, instance, attempt int, contents []byte) ArtifactRecord {
	digest := sha256.Sum256(contents)
	hash := hex.EncodeToString(digest[:])
	id := name + "-" + hash[:12]
	f.artifacts[id] = append([]byte(nil), contents...)
	component := ComponentID(kind)
	if !ValidComponentID(component) {
		component = componentForArtifact(name)
	}
	return ArtifactRecord{ArtifactID: id, Kind: kind, Name: name, Path: ArtifactBlobPrefix + hash + filepath.Ext(name), Sha256: hash, Bytes: int64(len(contents)), CreatedAt: time.Unix(10, 0).UTC(), Producer: ArtifactProducer{ComponentID: component, Instance: instance, SeatID: string(component) + "-1", Attempt: attempt}}
}

func componentForArtifact(name string) ComponentID {
	switch name {
	case "SOURCE.md", "source.json", "PROBLEM.md":
		return ComponentIntake
	case "PLAN.md":
		return ComponentPlan
	case "IMPLEMENTATION.md", "CHANGESET.patch":
		return ComponentBuild
	case "verification.json":
		return ComponentVerify
	case "REVIEW.md", "review.json":
		return ComponentReview
	default:
		return ComponentPublish
	}
}
func testOID(character byte) string { return string(makeBytes(character, 40)) }
func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func makeBytes(character byte, count int) []byte {
	value := make([]byte, count)
	for i := range value {
		value[i] = character
	}
	return value
}

type controllerFixture struct {
	controller *Controller
	runtime    *fakeControllerRuntime
	runs       *RunStore
	board      *Board
	ctx        context.Context
}

func newControllerFixture(t *testing.T) controllerFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	boardRoot := filepath.Join(root, "boards")
	runRoot := filepath.Join(root, "state")
	os.MkdirAll(boardRoot, 0o700)
	os.MkdirAll(runRoot, 0o700)
	boardScope, err := runfs.OpenScope(boardRoot, runfs.ScopeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runScope, err := runfs.OpenScope(runRoot, runfs.ScopeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Unix(1, 0).UTC() }
	boards, _ := NewBoardStore(boardScope, now)
	runs, _ := NewRunStore(runScope, now)
	projectPath := filepath.Join(root, "project")
	os.MkdirAll(projectPath, 0o700)
	board := &Board{SchemaVersion: 1, ID: "board-1", Name: "Board", ProjectID: "project-1", ProjectPath: projectPath, Components: Components{Plan: SeatComponent{Seat: Seat{Vendor: VendorClaude, Model: "sonnet", Effort: "medium", Permission: "acceptEdits"}}, Build: SeatComponent{Seat: Seat{Vendor: VendorClaude, Model: "sonnet", Effort: "medium", Permission: "acceptEdits"}}, Review: SeatComponent{Seat: Seat{Vendor: VendorClaude, Model: "sonnet", Effort: "medium", Permission: "acceptEdits"}}, Publish: PublishComponent{Publish: PublishConfig{Base: "main", Draft: true}}}}
	board, err = boards.Save(ctx, board, 0)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newFakeControllerRuntime(root)
	controller, err := NewController(ControllerOptions{Boards: boards, Runs: runs, Runtime: runtime, RootsDirectory: root, Now: now, Suffix: func() (string, error) { return "deadbeef", nil }})
	if err != nil {
		t.Fatal(err)
	}
	return controllerFixture{controller: controller, runtime: runtime, runs: runs, board: board, ctx: ctx}
}

func (f controllerFixture) start(t *testing.T) (*RunState, error) {
	t.Helper()
	return f.controller.Start(f.ctx, StartRequest{ProjectID: f.board.ProjectID, BoardID: f.board.ID, Source: SourceInput{Kind: "text", Title: "Task", Text: "Change README"}})
}

func TestControllerHappyPathAndPublicationGate(t *testing.T) {
	fixture := newControllerFixture(t)
	state, err := fixture.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunAwaitingApproval || state.Gate == nil || state.Gate.ComponentID != ComponentPublish {
		t.Fatalf("not at publish gate: %+v", state)
	}
	if fixture.runtime.executions[ComponentPlan] != 1 || fixture.runtime.executions[ComponentBuild] != 1 || fixture.runtime.executions[ComponentReview] != 1 {
		t.Fatalf("unexpected executions: %+v", fixture.runtime.executions)
	}
	state, err = fixture.controller.DecideGate(fixture.ctx, state.ProjectID, state.RunID, GateApproved, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunSucceeded || state.Publication == nil || fixture.runtime.publishes != 1 {
		t.Fatalf("publication failed: %+v", state)
	}
	replayed, err := fixture.runs.Replay(fixture.ctx, state.ProjectID, state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.LastSeq != state.LastSeq || replayed.Publication.PRNumber != 1 {
		t.Fatal("replay diverged")
	}
}

func TestControllerBoundedRepairOpensGate(t *testing.T) {
	fixture := newControllerFixture(t)
	fixture.runtime.verifyVerdicts = []verification.Verdict{verification.VerdictFailed, verification.VerdictFailed, verification.VerdictFailed}
	state, err := fixture.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if state.Gate == nil || state.Gate.Reason != "waiting_for_repair" || fixture.runtime.executions[ComponentBuild] != 3 {
		t.Fatalf("repair bound not enforced: gate=%+v executions=%d", state.Gate, fixture.runtime.executions[ComponentBuild])
	}
	if fixture.runtime.publishes != 0 {
		t.Fatal("failed verification reached publication")
	}
}

func TestReviewChangesAndMutationFailClosed(t *testing.T) {
	t.Run("changes requested exhausts repairs", func(t *testing.T) {
		fixture := newControllerFixture(t)
		fixture.runtime.reviewVerdicts = []ReviewVerdict{ReviewChangesRequested, ReviewChangesRequested, ReviewChangesRequested}
		state, err := fixture.start(t)
		if err != nil {
			t.Fatal(err)
		}
		if state.Gate == nil || state.Gate.ComponentID != ComponentReview || fixture.runtime.executions[ComponentBuild] != 3 || fixture.runtime.publishes != 0 {
			t.Fatalf("review failure did not remain gated: state=%+v executions=%+v", state, fixture.runtime.executions)
		}
	})

	t.Run("reviewer mutation fails immediately", func(t *testing.T) {
		fixture := newControllerFixture(t)
		fixture.runtime.reviewerMutated = true
		state, err := fixture.start(t)
		if err == nil || state.Components[ComponentReview].Reason != "reviewer_mutated_worktree" || state.Gate != nil || fixture.runtime.publishes != 0 {
			t.Fatalf("reviewer mutation was not fail-closed: state=%+v err=%v", state, err)
		}
	})
}

func TestControllerFailureRetryAndMissingOutput(t *testing.T) {
	fixture := newControllerFixture(t)
	fixture.runtime.missing[ComponentPlan] = true
	state, err := fixture.start(t)
	if err == nil {
		t.Fatal("missing plan accepted")
	}
	if state.Status != RunRunning || state.Components[ComponentPlan].Reason != "missing_output" {
		t.Fatalf("unexpected failed state: %+v", state)
	}
	fixture.runtime.missing[ComponentPlan] = false
	state, err = fixture.controller.Retry(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunAwaitingApproval || fixture.runtime.executions[ComponentPlan] != 2 {
		t.Fatalf("retry did not advance: %+v", state)
	}
}

func TestRetryUsesImmutableBoardSnapshotAndCompositeSessionIdentity(t *testing.T) {
	t.Run("board snapshot", func(t *testing.T) {
		fixture := newControllerFixture(t)
		fixture.runtime.missing[ComponentPlan] = true
		state, err := fixture.start(t)
		if err == nil {
			t.Fatal("missing plan accepted")
		}
		updated := *fixture.board
		updated.Components.Plan.Seat.Model = "opus"
		if _, err := fixture.controller.boards.Save(fixture.ctx, &updated, fixture.board.Revision); err != nil {
			t.Fatal(err)
		}
		fixture.runtime.missing[ComponentPlan] = false
		state, err = fixture.controller.Retry(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan)
		if err != nil {
			t.Fatal(err)
		}
		if fixture.runtime.models[ComponentPlan] != "sonnet" {
			t.Fatalf("retry used edited board instead of snapshot: %q", fixture.runtime.models[ComponentPlan])
		}
	})

	t.Run("cross-vendor session", func(t *testing.T) {
		fixture := newControllerFixture(t)
		fixture.runtime.sessionAgent = string(VendorCodex)
		state, err := fixture.start(t)
		if err == nil || state.Components[ComponentPlan].Reason != "invalid_output" || state.Components[ComponentBuild].Status != ComponentBlocked {
			t.Fatalf("cross-vendor session accepted: state=%+v err=%v", state, err)
		}
	})
}

func TestRetryBuildContinuesThroughVerificationAndReview(t *testing.T) {
	fixture := newControllerFixture(t)
	fixture.runtime.missing[ComponentBuild] = true
	state, err := fixture.start(t)
	if err == nil || state.Components[ComponentBuild].Reason != "no_change_captured" {
		t.Fatalf("missing build change accepted: state=%+v err=%v", state, err)
	}
	fixture.runtime.missing[ComponentBuild] = false
	state, err = fixture.controller.Retry(fixture.ctx, state.ProjectID, state.RunID, ComponentBuild)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunAwaitingApproval || fixture.runtime.executions[ComponentBuild] != 2 || fixture.runtime.executions[ComponentReview] != 1 {
		t.Fatalf("build retry did not rejoin pipeline: state=%+v executions=%+v", state, fixture.runtime.executions)
	}
}

func TestCancelSnapshotsBeforeTerminalState(t *testing.T) {
	fixture := newControllerFixture(t)
	state := seedLiveAttempt(t, fixture, AttemptRunning)
	state, err := fixture.controller.Cancel(fixture.ctx, state.ProjectID, state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunCanceled || fixture.runtime.cancels != 1 {
		t.Fatalf("cancel failed: %+v", state)
	}
	if _, ok := latestArtifact(state, "cancel-snapshot.patch"); !ok {
		t.Fatal("cancel snapshot missing")
	}
	if _, ok := latestArtifact(state, "run-report.json"); !ok {
		t.Fatal("cancel report missing")
	}
	if _, err := fixture.controller.Cancel(fixture.ctx, state.ProjectID, state.RunID); err == nil {
		t.Fatal("second cancel accepted")
	}
}

func TestTakeoverAndHandbackDoNotAutoAdvanceEarly(t *testing.T) {
	fixture := newControllerFixture(t)
	state := seedLiveAttempt(t, fixture, AttemptRunning)
	state, err := fixture.controller.Takeover(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan)
	if err != nil {
		t.Fatal(err)
	}
	if state.Components[ComponentPlan].Attempt.Ownership != OwnershipHumanControlled || state.Components[ComponentBuild].Status != ComponentBlocked {
		t.Fatalf("takeover advanced unexpectedly: %+v", state)
	}
	state, err = fixture.controller.Handback(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunAwaitingApproval {
		t.Fatalf("handback did not rejoin workflow: %+v", state)
	}
}

func TestHandbackWithMissingArtifactFailsClosed(t *testing.T) {
	fixture := newControllerFixture(t)
	state := seedLiveAttempt(t, fixture, AttemptRunning)
	state, err := fixture.controller.Takeover(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan)
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.handbackMissing = true
	state, err = fixture.controller.Handback(fixture.ctx, state.ProjectID, state.RunID, ComponentPlan)
	if err == nil || state.Components[ComponentPlan].Reason != "missing_output" || state.Components[ComponentBuild].Status != ComponentBlocked {
		t.Fatalf("missing handback artifact advanced: state=%+v err=%v", state, err)
	}
}

func TestReconcileNeverRelaunchesAmbiguousAttempt(t *testing.T) {
	fixture := newControllerFixture(t)
	state := seedLiveAttempt(t, fixture, AttemptLaunchRequestedStatus)
	result, err := fixture.controller.Reconcile(fixture.ctx, state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = fixture.runs.Read(fixture.ctx, state.ProjectID, state.RunID)
	if result.Failed != 1 || state.Components[ComponentPlan].Reason != "unknown_after_restart" || fixture.runtime.executions[ComponentPlan] != 0 {
		t.Fatalf("ambiguous reconcile was unsafe: %+v %+v", result, state)
	}
}

func TestReconcileRunningAttemptOnlyRearms(t *testing.T) {
	fixture := newControllerFixture(t)
	state := seedLiveAttempt(t, fixture, AttemptRunning)
	fixture.runtime.probe = ProbeResult{State: ProbeRunning}
	for range 2 {
		result, err := fixture.controller.Reconcile(fixture.ctx, state.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Rearmed != 1 {
			t.Fatalf("running attempt not rearmed: %+v", result)
		}
	}
	if fixture.runtime.rearms != 2 || fixture.runtime.executions[ComponentPlan] != 0 {
		t.Fatalf("reconcile duplicated work: rearms=%d executions=%+v", fixture.runtime.rearms, fixture.runtime.executions)
	}
}

func TestReconcileExitedPlanDrainsOnceAndContinues(t *testing.T) {
	fixture := newControllerFixture(t)
	state := seedLiveAttempt(t, fixture, AttemptRunning)
	record, err := fixture.runtime.RecordControllerArtifact(fixture.ctx, state.RunRoot, "PLAN.md", []byte("recovered plan\n"), ProducerRef{Component: ComponentPlan, Instance: 1, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.probe = ProbeResult{State: ProbeExited, Completion: &AttemptResult{Artifacts: []ArtifactRecord{record}, ExitCode: 0, FinishedAt: time.Unix(60, 0).UTC()}}
	result, err := fixture.controller.Reconcile(fixture.ctx, state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.runs.Read(fixture.ctx, state.ProjectID, state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Drained != 1 || state.Status != RunAwaitingApproval || fixture.runtime.executions[ComponentPlan] != 0 || fixture.runtime.executions[ComponentBuild] != 1 {
		t.Fatalf("recovered plan did not drain safely: result=%+v state=%+v executions=%+v", result, state, fixture.runtime.executions)
	}
	result, err = fixture.controller.Reconcile(fixture.ctx, state.ProjectID)
	if err != nil || result != (ReconcileResult{}) || fixture.runtime.executions[ComponentBuild] != 1 {
		t.Fatalf("second reconcile duplicated work: result=%+v err=%v executions=%+v", result, err, fixture.runtime.executions)
	}
}

func TestRejectedPublishGateNeverPublishes(t *testing.T) {
	fixture := newControllerFixture(t)
	state, err := fixture.start(t)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.controller.DecideGate(fixture.ctx, state.ProjectID, state.RunID, GateRejected, "not now")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunFailed || fixture.runtime.publishes != 0 {
		t.Fatalf("rejected gate published: state=%+v publishes=%d", state, fixture.runtime.publishes)
	}
	reportArtifact, ok := latestArtifact(state, "run-report.json")
	if !ok {
		t.Fatal("rejected gate report missing")
	}
	contents, err := fixture.runtime.ReadArtifact(fixture.ctx, state.RunRoot, reportArtifact)
	if err != nil {
		t.Fatal(err)
	}
	var report RunReport
	if err := json.Unmarshal(contents, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != RunFailed || report.Gate == nil || report.Gate.Decision != GateRejected || report.Failure == nil || report.Review == nil || report.Verification == nil {
		t.Fatalf("rejected report is incomplete: %+v", report)
	}
}

func TestLiveAttemptDoesNotHoldRunLockAgainstCancellation(t *testing.T) {
	fixture := newControllerFixture(t)
	fixture.runtime.executeStarted = make(chan struct{}, 1)
	fixture.runtime.executeRelease = make(chan struct{})
	type startResult struct {
		state *RunState
		err   error
	}
	started := make(chan startResult, 1)
	go func() {
		state, err := fixture.controller.Start(fixture.ctx, StartRequest{ProjectID: fixture.board.ProjectID, BoardID: fixture.board.ID, Source: SourceInput{Kind: "text", Title: "Task", Text: "Change README"}})
		started <- startResult{state: state, err: err}
	}()
	select {
	case <-fixture.runtime.executeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("attempt did not launch")
	}
	runID := "run-19700101t000001-deadbeef"
	canceled := make(chan startResult, 1)
	go func() {
		state, err := fixture.controller.Cancel(fixture.ctx, fixture.board.ProjectID, runID)
		canceled <- startResult{state: state, err: err}
	}()
	select {
	case result := <-canceled:
		if result.err != nil || result.state.Status != RunCanceled {
			t.Fatalf("cancel result = %+v, err = %v", result.state, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel blocked behind the live attempt")
	}
	close(fixture.runtime.executeRelease)
	select {
	case result := <-started:
		if result.err != nil || result.state.Status != RunCanceled {
			t.Fatalf("start result after cancel = %+v, err = %v", result.state, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start did not observe the canceled terminal state")
	}
}

func TestLiveAttemptCanBeTakenOverWithoutTheOldCompletionWinning(t *testing.T) {
	fixture := newControllerFixture(t)
	fixture.runtime.executeStarted = make(chan struct{}, 1)
	fixture.runtime.executeRelease = make(chan struct{})
	type startResult struct {
		state *RunState
		err   error
	}
	started := make(chan startResult, 1)
	go func() {
		state, err := fixture.controller.Start(fixture.ctx, StartRequest{ProjectID: fixture.board.ProjectID, BoardID: fixture.board.ID, Source: SourceInput{Kind: "text", Title: "Task", Text: "Change README"}})
		started <- startResult{state: state, err: err}
	}()
	select {
	case <-fixture.runtime.executeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("attempt did not launch")
	}
	runID := "run-19700101t000001-deadbeef"
	taken, err := fixture.controller.Takeover(fixture.ctx, fixture.board.ProjectID, runID, ComponentPlan)
	if err != nil {
		t.Fatal(err)
	}
	if taken.Components[ComponentPlan].Attempt.Ownership != OwnershipHumanControlled || taken.Components[ComponentPlan].Attempt.Attempt != 2 {
		t.Fatalf("takeover state = %+v", taken.Components[ComponentPlan].Attempt)
	}
	close(fixture.runtime.executeRelease)
	select {
	case result := <-started:
		if result.err != nil || result.state.Components[ComponentPlan].Attempt.Ownership != OwnershipHumanControlled {
			t.Fatalf("old completion overrode takeover: state=%+v err=%v", result.state, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old attempt did not drain after takeover")
	}
}

func seedLiveAttempt(t *testing.T, fixture controllerFixture, status AttemptStatus) *RunState {
	t.Helper()
	projectID := fixture.board.ProjectID
	runID := "run-19700101t000001-feedface"
	source, _ := CaptureSource(SourceInput{Kind: "text", Title: "Task", Text: "work"})
	state, err := fixture.runs.Append(fixture.ctx, projectID, runID, &RunCreated{ProjectID: projectID, BoardID: fixture.board.ID, BoardRevision: fixture.board.Revision, Title: "Task", Source: source.Record})
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.runs.Append(fixture.ctx, projectID, runID, &RunRootCreated{RunRoot: fixture.runtime.root, Branch: "dagama/" + runID, BaseBranch: "main", BaseSha: testOID('a'), RemoteURL: "https://github.com/example/repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	boardSnapshot, err := json.Marshal(fixture.board)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := map[string][]byte{"SOURCE.md": []byte("data\n"), "PROBLEM.md": []byte("data\n"), "PLAN.md": []byte("data\n"), "board.snapshot.json": boardSnapshot}
	for _, name := range []string{"SOURCE.md", "PROBLEM.md", "PLAN.md", "board.snapshot.json"} {
		component := componentForArtifact(name)
		if name == "board.snapshot.json" {
			component = ComponentIntake
		}
		record, _ := fixture.runtime.RecordControllerArtifact(fixture.ctx, state.RunRoot, name, artifacts[name], ProducerRef{Component: component, Instance: 1})
		state, err = fixture.runs.Append(fixture.ctx, projectID, runID, &ArtifactPromoted{Artifact: record})
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err = fixture.runs.Append(fixture.ctx, projectID, runID, &ComponentReady{ComponentInstance{ComponentID: ComponentPlan, Instance: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ref := AttemptRef{ComponentInstance: ComponentInstance{ComponentID: ComponentPlan, Instance: 1}, SeatID: "plan-1", Attempt: 1, AttemptID: "attempt-1"}
	state, err = fixture.runs.Append(fixture.ctx, projectID, runID, &AttemptLaunchRequested{AttemptRef: ref, TmuxName: "coslash_dagama_0123456789abcdef", SessionID: "session-plan", Ownership: OwnershipAutomated})
	if err != nil {
		t.Fatal(err)
	}
	if status == AttemptRunning {
		state, err = fixture.runs.Append(fixture.ctx, projectID, runID, &AttemptLaunched{AttemptRef: ref, TmuxName: "coslash_dagama_0123456789abcdef", SessionID: "session-plan", Ownership: OwnershipAutomated})
		if err != nil {
			t.Fatal(err)
		}
	}
	return state
}
