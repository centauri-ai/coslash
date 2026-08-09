package atlas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

// The controller is exercised against the real board and run stores with a fake
// runtime, so what these tests pin is the durable ordering and the committee
// semantics — not a mock of them.

// fakeRuntime records what the controller asked for and returns what a test
// told it to. Every artifact lives in memory, keyed by name.
type fakeRuntime struct {
	mu sync.Mutex

	runRoot string
	// workers is the committee size the fixture configured. The fake needs it
	// to know whether a turn writes a draft or the promoted name; inferring it
	// from launches already seen would miscount on a second instance.
	workers int
	// artifacts is the run root's contents, by artifact name.
	artifacts map[string][]byte
	// launches is every attempt the controller executed, in order.
	launches []AttemptRequest
	// failSeats names seats whose turn fails outright.
	failSeats map[string]bool
	// silentSeats names seats that exit cleanly having written nothing.
	silentSeats map[string]bool
	// holdSeat blocks one seat's turn until released, so a test can act on a
	// genuinely live attempt rather than a simulated one.
	holdSeat string
	hold     chan struct{}
	// verdicts is the verification verdict per call, consumed in order.
	verdicts []verification.Verdict
	// reviews is the review verdict per review turn, consumed in order.
	reviews   []string
	publishes int
	cleanups  int
	released  int
}

func newFakeRuntime(root string, workers int) *fakeRuntime {
	return &fakeRuntime{
		runRoot:     filepath.Join(root, "run"),
		workers:     workers,
		artifacts:   map[string][]byte{},
		failSeats:   map[string]bool{},
		silentSeats: map[string]bool{},
		hold:        make(chan struct{}),
	}
}

func (r *fakeRuntime) Prepare(_ context.Context, request PrepareRequest) (PreparedRun, error) {
	return PreparedRun{
		Root: revision.RunRoot{
			Path: r.runRoot, Branch: request.Branch,
			BaseSha:           strings.Repeat("a", 40),
			PublishBaseBranch: "main",
			PublishBaseSha:    strings.Repeat("a", 40),
		},
		RemoteURL: "https://github.com/example/demo.git",
	}, nil
}

func (r *fakeRuntime) RecordControllerArtifact(
	_ context.Context, _ string, name string, contents []byte, producer ArtifactProducer,
) (ArtifactRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if contents != nil {
		r.artifacts[name] = contents
	}
	body := r.artifacts[name]
	digest := sha256.Sum256(body)
	return ArtifactRecord{
		ArtifactID: name + "-" + producer.SeatID,
		Kind:       "file", Name: name,
		Path:   filepath.Join("out", name),
		SHA256: hex.EncodeToString(digest[:]),
		Bytes:  int64(len(body)),
		// A fixed clock keeps replay comparisons stable.
		CreatedAt: time.Unix(1, 0).UTC(),
		Producer:  producer,
	}, nil
}

func (r *fakeRuntime) ReadArtifact(_ context.Context, _ string, record ArtifactRecord) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	contents, ok := r.artifacts[record.Name]
	if !ok {
		return nil, newError(CodeNotFound, "no such artifact")
	}
	return contents, nil
}

func (r *fakeRuntime) Execute(
	_ context.Context, request AttemptRequest, record LaunchRecorder,
) (AttemptResult, error) {
	r.mu.Lock()
	r.launches = append(r.launches, request)
	fail := r.failSeats[request.SeatID]
	silent := r.silentSeats[request.SeatID]
	r.mu.Unlock()

	session := contracts.SessionIdentity{Agent: string(request.Seat.Vendor), ID: "session-" + request.SeatID}
	if err := record(session); err != nil {
		return AttemptResult{}, err
	}
	r.mu.Lock()
	held := r.holdSeat != "" && r.holdSeat == request.SeatID && request.Attempt == 1
	r.mu.Unlock()
	if held {
		<-r.hold
	}
	if fail {
		return AttemptResult{}, newError(CodeInvalidState, "the seat failed")
	}
	if silent {
		return AttemptResult{ExitCode: 0, FinishedAt: time.Unix(2, 0).UTC(), Session: session}, nil
	}

	outputs := r.writeSeatOutputs(request)
	return AttemptResult{
		ExitCode: 0, FinishedAt: time.Unix(2, 0).UTC(), Session: session, Outputs: outputs,
	}, nil
}

// writeSeatOutputs stands in for the agent writing its contracted files.
func (r *fakeRuntime) writeSeatOutputs(request AttemptRequest) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	names := map[ComponentID][]string{
		ComponentPlan:   {"PLAN.md"},
		ComponentBuild:  {"IMPLEMENTATION.md"},
		ComponentReview: {"REVIEW.md", "review.json"},
	}[request.Component]

	refine := IsMainRefineSeatID(request.Component, request.SeatID)
	written := make([]string, 0, len(names))
	for index, name := range names {
		effective := name
		if index == 0 && !refine && r.workers > 1 {
			effective = DraftArtifactName(name)
		}
		body := fmt.Sprintf("%s from %s", effective, request.SeatID)
		if name == "review.json" {
			verdict := ReviewApproved
			if len(r.reviews) > 0 {
				verdict = r.reviews[0]
				r.reviews = r.reviews[1:]
			}
			encoded, _ := json.Marshal(ReviewOutcome{SchemaVersion: 1, Verdict: verdict})
			body = string(encoded)
		}
		r.artifacts[effective] = []byte(body)
		written = append(written, effective)
	}
	return written
}

func (r *fakeRuntime) Release(context.Context, AttemptState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released++
	return nil
}

func (r *fakeRuntime) Verify(
	_ context.Context, request VerifyRequest,
) (verification.Document, ArtifactRecord, error) {
	r.mu.Lock()
	verdict := verification.VerdictPassed
	if len(r.verdicts) > 0 {
		verdict = r.verdicts[0]
		r.verdicts = r.verdicts[1:]
	}
	document := verification.Document{
		SchemaVersion: 1, Verdict: verdict, ChangeRevision: request.ChangeRevision,
	}
	encoded, _ := json.Marshal(document)
	r.artifacts["verification.json"] = encoded
	r.mu.Unlock()

	record, err := r.RecordControllerArtifact(context.Background(), request.RunRoot, "verification.json", encoded,
		ArtifactProducer{ComponentID: ComponentVerify, Instance: 1})
	return document, record, err
}

func (r *fakeRuntime) Publish(
	_ context.Context, request PublishRequest,
) (PublicationRecord, ArtifactRecord, error) {
	r.mu.Lock()
	r.publishes++
	r.mu.Unlock()
	record := PublicationRecord{
		ChangeRevision: request.Review.ChangeRevision,
		CommitSha:      strings.Repeat("b", 40), Branch: request.State.Branch,
		Remote: "origin", PRNumber: 1, Action: "created",
		IdempotencyKey: "key", PublishedAt: time.Unix(3, 0).UTC(),
	}
	encoded, _ := json.Marshal(record)
	artifact, err := r.RecordControllerArtifact(context.Background(), request.State.RunRoot,
		"publication.json", encoded, ArtifactProducer{ComponentID: ComponentPublish, Instance: 1})
	return record, artifact, err
}

func (r *fakeRuntime) CaptureChange(_ context.Context, _ string, revisionNumber uint64) (ChangeRecord, error) {
	patch := fmt.Sprintf("diff for revision %d", revisionNumber)
	r.mu.Lock()
	r.artifacts["CHANGESET.patch"] = []byte(patch)
	r.mu.Unlock()
	digest := sha256.Sum256([]byte(patch))
	return ChangeRecord{
		ChangeRevision: revisionNumber,
		TreeOID:        strings.Repeat("c", 40),
		PatchSHA256:    hex.EncodeToString(digest[:]),
		PatchBytes:     int64(len(patch)),
		ChangedFiles:   []ChangedFileRecord{{Path: "src/app.go", Status: "M"}},
		BaseSha:        strings.Repeat("a", 40),
	}, nil
}

func (r *fakeRuntime) Cancel(context.Context, *RunState) (*ArtifactRecord, error) { return nil, nil }
func (r *fakeRuntime) Takeover(_ context.Context, request AttemptRequest, prior AttemptState) (AttemptResult, error) {
	r.mu.Lock()
	r.launches = append(r.launches, request)
	r.mu.Unlock()
	// A takeover resumes the same provider session rather than opening a new one.
	session := contracts.SessionIdentity{}
	if prior.Session != nil {
		session = *prior.Session
	}
	return AttemptResult{ExitCode: 0, FinishedAt: time.Unix(4, 0).UTC(), Session: session}, nil
}
func (r *fakeRuntime) Handback(_ context.Context, request AttemptRequest, _ AttemptState) (AttemptResult, error) {
	// Releasing the hold lets the original turn finish, which is what a real
	// handback does: the operator stops typing and the turn ends.
	r.mu.Lock()
	holding := r.holdSeat != ""
	r.holdSeat = ""
	r.mu.Unlock()
	if holding {
		close(r.hold)
	}
	outputs := r.writeSeatOutputs(request)
	return AttemptResult{ExitCode: 0, FinishedAt: time.Unix(4, 0).UTC(), Outputs: outputs}, nil
}
func (r *fakeRuntime) Probe(context.Context, *RunState, AttemptState) (ProbeResult, error) {
	return ProbeResult{State: ProbeMissing}, nil
}
func (r *fakeRuntime) Rearm(context.Context, *RunState, AttemptState) error { return nil }
func (r *fakeRuntime) Cleanup(context.Context, *RunState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups++
	return nil
}

// ---------------------------------------------------------------------------

type controllerFixture struct {
	controller *Controller
	runtime    *fakeRuntime
	runs       *RunStore
	board      *BoardDocument
	ctx        context.Context
}

type boardLoader struct{ document *BoardDocument }

func (l boardLoader) Load(_ context.Context, projectID, boardID string) (*BoardDocument, error) {
	if l.document == nil || l.document.ProjectID != projectID || l.document.ID != boardID {
		return nil, newError(CodeNotFound, "no such board")
	}
	return l.document, nil
}

// newControllerFixture builds a controller whose plan, build, and review seats
// each carry `workers` committee members.
func newControllerFixture(t *testing.T, workers int) *controllerFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	runRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(runRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	scope, err := runfs.OpenScope(runRoot, runfs.ScopeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Unix(1, 0).UTC() }
	runs, err := NewRunStore(scope, now)
	if err != nil {
		t.Fatal(err)
	}

	board := DefaultBoard()
	for _, role := range SeatComponentIDs {
		component := board.ComponentByLegacyRole(role)
		if component == nil {
			t.Fatalf("the default board has no %s seat", role)
		}
		seats := make([]WorkerSeat, 0, workers)
		for index := range workers {
			base := DefaultSeatForVendor(VendorClaude)
			seat := WorkerSeat{
				ID: WorkerSeatID(component.ID, index), Vendor: base.Vendor,
				Model: base.Model, Effort: base.Effort, Permission: base.Permission,
			}
			if index == 0 && workers > 1 {
				seat.Role = RoleMain
			}
			seats = append(seats, seat)
		}
		component.Seats = seats
	}
	Normalize(board)
	document := &BoardDocument{
		// The storage envelope and the graph carry separate schema versions;
		// the document takes the envelope's.
		SchemaVersion: DocumentSchemaVersion, ID: "board-1", ProjectID: "demo",
		Name: "Board", Revision: 1, Board: board,
	}

	runtime := newFakeRuntime(root, workers)
	controller, err := NewController(ControllerOptions{
		Boards: boardLoader{document: document}, Runs: runs, Runtime: runtime,
		RootsDirectory: root, Now: now,
		Suffix: func() (string, error) { return "deadbeef", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &controllerFixture{controller: controller, runtime: runtime, runs: runs, board: document, ctx: ctx}
}

// allowRepairRounds raises the board's feedback bound. The default board
// allows one Build round, so a repair test has to ask for the extra round it
// is testing rather than assume the default permits it.
func (f *controllerFixture) allowRepairRounds(t *testing.T, rounds uint64) {
	t.Helper()
	build := f.board.Board.ComponentByLegacyRole(ComponentBuild)
	if build == nil {
		t.Fatal("the board has no build seat")
	}
	raised := false
	for index := range f.board.Board.Edges {
		edge := &f.board.Board.Edges[index]
		if edge.Kind == EdgeFeedback && edge.To == build.ID {
			edge.MaxRounds = rounds
			edge.Mode = TriggerAuto
			raised = true
		}
	}
	if !raised {
		t.Fatal("the board has no feedback edge into build")
	}
	Normalize(f.board.Board)
	if got := f.board.Board.FeedbackMaxRoundsToBuild(); got != rounds {
		t.Fatalf("feedback bound is %d, want %d", got, rounds)
	}
}

func (f *controllerFixture) start(t *testing.T) *RunState {
	t.Helper()
	state, err := f.controller.Start(f.ctx, StartRequest{
		ProjectID: "demo", BoardID: "board-1", ProjectPath: t.TempDir(),
		Source: SourceInput{Kind: "text", Title: "Add a logout button", Text: "Please add one."},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return state
}

// startRequest is the standard run this fixture starts.
func (f *controllerFixture) startRequest(t *testing.T) StartRequest {
	t.Helper()
	return StartRequest{
		ProjectID: "demo", BoardID: "board-1", ProjectPath: t.TempDir(),
		Source: SourceInput{Kind: "text", Title: "Add a logout button", Text: "Please add one."},
	}
}

// setTriggerManual makes one pipeline edge wait for an explicit go.
func (f *controllerFixture) setTriggerManual(t *testing.T, from, to ComponentID) {
	t.Helper()
	board := f.board.Board
	fromComponent := board.ComponentByLegacyRole(from)
	toComponent := board.ComponentByLegacyRole(to)
	if fromComponent == nil || toComponent == nil {
		t.Fatalf("the board has no %s or %s seat", from, to)
	}
	changed := false
	for index := range board.Edges {
		edge := &board.Edges[index]
		if edge.Kind == EdgeTrigger && edge.From == fromComponent.ID && edge.To == toComponent.ID {
			edge.Mode = TriggerManual
			changed = true
		}
	}
	if !changed {
		t.Fatalf("the board has no trigger edge from %s to %s", from, to)
	}
	Normalize(board)
	if board.TriggerModeBetween(from, to) != TriggerManual {
		t.Fatalf("the %s to %s edge is still automatic", from, to)
	}
}

// componentLaunches counts the turns launched for one stage.
func (f *controllerFixture) componentLaunches(component ComponentID) int {
	f.runtime.mu.Lock()
	defer f.runtime.mu.Unlock()
	count := 0
	for _, launch := range f.runtime.launches {
		if launch.Component == component {
			count++
		}
	}
	return count
}

type liveAttemptRef struct{ runID, attemptID, seatID string }

// awaitLiveAttempt waits for a held seat to reach a running attempt.
func (f *controllerFixture) awaitLiveAttempt(t *testing.T, component ComponentID) liveAttemptRef {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		summaries, err := f.controller.ListRuns(f.ctx, "demo")
		if err == nil && len(summaries) > 0 {
			state, readErr := f.controller.ReadRun(f.ctx, "demo", summaries[0].RunID)
			if readErr == nil {
				if attempt := liveAttemptFor(state, component); attempt != nil &&
					attempt.Status == AttemptStatusRunning && attempt.Session != nil {
					return liveAttemptRef{
						runID: state.RunID, attemptID: attempt.AttemptID, seatID: attempt.SeatID,
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no live %s attempt appeared", component)
	return liveAttemptRef{}
}

// attemptByOwnership finds a stage's live attempt with a given owner.
func attemptByOwnership(state *RunState, component ComponentID, ownership Ownership) *AttemptState {
	current := state.Component(component)
	if current == nil {
		return nil
	}
	for index := range current.Attempts {
		attempt := &current.Attempts[index]
		if attempt.Ownership == ownership && attempt.Status != AttemptStatusExited {
			return attempt
		}
	}
	return nil
}

// awaitNoLiveAttempt waits until every attempt on a stage has exited.
func (f *controllerFixture) awaitNoLiveAttempt(t *testing.T, runID string, component ComponentID) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state, err := f.controller.ReadRun(f.ctx, "demo", runID)
		if err == nil && liveAttemptFor(state, component) == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a %s attempt is still live after handback", component)
}

// liveAttemptFor returns the stage's attempt that has not exited.
func liveAttemptFor(state *RunState, component ComponentID) *AttemptState {
	current := state.Component(component)
	if current == nil {
		return nil
	}
	for index := range current.Attempts {
		if current.Attempts[index].Status != AttemptStatusExited {
			return &current.Attempts[index]
		}
	}
	return nil
}

func (f *controllerFixture) seatsFor(component ComponentID) []string {
	var seats []string
	for _, launch := range f.runtime.launches {
		if launch.Component == component {
			seats = append(seats, launch.SeatID)
		}
	}
	return seats
}

// ---------------------------------------------------------------------------

func TestASoleWorkerRunsWithoutARefineTurn(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	state := fixture.start(t)

	if state.Status != RunAwaitingApproval || state.Gate == nil || state.Gate.ComponentID != ComponentPublish {
		t.Fatalf("the run did not reach the publish gate: %+v", state)
	}
	for _, component := range SeatComponentIDs {
		seats := fixture.seatsFor(component)
		if len(seats) != 1 || IsMainRefineSeatID(component, seats[0]) {
			t.Fatalf("%s launched %v, want exactly one worker turn", component, seats)
		}
	}
}

func TestACommitteeFansOutAndThenRefines(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	state := fixture.start(t)

	if state.Status != RunAwaitingApproval {
		t.Fatalf("the run did not reach the publish gate: %+v", state)
	}
	seats := fixture.seatsFor(ComponentPlan)
	if len(seats) != 4 {
		t.Fatalf("plan launched %v, want three workers and one refine turn", seats)
	}
	for index, seat := range seats[:3] {
		if seat != AttemptSeatID(ComponentPlan, index) {
			t.Fatalf("worker %d launched as %q", index, seat)
		}
	}
	// The refine turn must run last: it reconciles what the workers wrote.
	if !IsMainRefineSeatID(ComponentPlan, seats[3]) {
		t.Fatalf("the last plan turn was %q, want the refine turn", seats[3])
	}
}

func TestEverySiblingGetsItsOwnOutputDirectory(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	fixture.start(t)

	seen := map[string]string{}
	for _, launch := range fixture.runtime.launches {
		if previous, taken := seen[launch.OutputDirectory]; taken {
			t.Fatalf("seats %s and %s shared %s", previous, launch.SeatID, launch.OutputDirectory)
		}
		seen[launch.OutputDirectory] = launch.SeatID
	}
}

func TestTheRefineTurnSeesEverySiblingDraft(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	fixture.start(t)

	var refine *AttemptRequest
	for index, launch := range fixture.runtime.launches {
		if launch.Component == ComponentPlan && IsMainRefineSeatID(ComponentPlan, launch.SeatID) {
			refine = &fixture.runtime.launches[index]
			break
		}
	}
	if refine == nil {
		t.Fatal("no plan refine turn was launched")
	}
	for index := range 3 {
		seat := AttemptSeatID(ComponentPlan, index)
		if !strings.Contains(refine.Prompt, "committee draft from "+seat) {
			t.Fatalf("the refine prompt is missing %s:\n%s", seat, refine.Prompt)
		}
	}
}

func TestAPartialCommitteeFailureIsExplicitAndStillRefines(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	// The second plan worker fails outright; the committee must continue with
	// the two drafts it has and say so.
	fixture.runtime.failSeats[AttemptSeatID(ComponentPlan, 1)] = true
	state := fixture.start(t)

	var refine *AttemptRequest
	for index, launch := range fixture.runtime.launches {
		if launch.Component == ComponentPlan && IsMainRefineSeatID(ComponentPlan, launch.SeatID) {
			refine = &fixture.runtime.launches[index]
		}
	}
	if refine == nil {
		t.Fatalf("a partial committee did not refine: %+v", state)
	}
	if !strings.Contains(refine.Prompt, "its turn failed") {
		t.Fatalf("the refine turn was not told a sibling failed:\n%s", refine.Prompt)
	}
	if !strings.Contains(refine.Prompt, AttemptSeatID(ComponentPlan, 1)) {
		t.Fatalf("the failed sibling was not named:\n%s", refine.Prompt)
	}
}

func TestAStageWhereEverySiblingFailsDoesNotRefine(t *testing.T) {
	fixture := newControllerFixture(t, 2)
	for index := range 2 {
		fixture.runtime.failSeats[AttemptSeatID(ComponentPlan, index)] = true
	}
	state := fixture.start(t)

	// Refining zero drafts would invent the stage's output.
	for _, seat := range fixture.seatsFor(ComponentPlan) {
		if IsMainRefineSeatID(ComponentPlan, seat) {
			t.Fatal("a stage with no drafts still ran a refine turn")
		}
	}
	plan := state.Component(ComponentPlan)
	if plan == nil || plan.Status != ComponentFailed || plan.Reason != "missing_output" {
		t.Fatalf("the stage did not fail as missing_output: %+v", plan)
	}
}

func TestASiblingThatWritesNothingIsCarriedAsAFailure(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	// A clean exit having written nothing is the quiet failure the exit
	// protocol exists to catch.
	fixture.runtime.silentSeats[AttemptSeatID(ComponentPlan, 2)] = true
	fixture.start(t)

	var refine *AttemptRequest
	for index, launch := range fixture.runtime.launches {
		if launch.Component == ComponentPlan && IsMainRefineSeatID(ComponentPlan, launch.SeatID) {
			refine = &fixture.runtime.launches[index]
		}
	}
	if refine == nil {
		t.Fatal("the stage did not refine")
	}
	if !strings.Contains(refine.Prompt, AttemptSeatID(ComponentPlan, 2)) ||
		!strings.Contains(refine.Prompt, "its turn failed") {
		t.Fatalf("a silent sibling was not reported as failed:\n%s", refine.Prompt)
	}
}

func TestEveryAttemptBindsACompositeSessionIdentity(t *testing.T) {
	fixture := newControllerFixture(t, 2)
	state := fixture.start(t)

	// A bare id is never enough: Claude and Codex allocate ids independently.
	if len(state.Sessions) == 0 {
		t.Fatal("the run bound no sessions")
	}
	for _, session := range state.Sessions {
		if session.Agent == "" || session.ID == "" {
			t.Fatalf("a half identity was bound: %+v", session)
		}
	}
}

func TestAFailedVerificationSpendsABoundedRepairRound(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	fixture.allowRepairRounds(t, 2)
	fixture.runtime.verdicts = []verification.Verdict{verification.VerdictFailed, verification.VerdictPassed}
	state := fixture.start(t)

	builds := 0
	for _, launch := range fixture.runtime.launches {
		if launch.Component == ComponentBuild {
			builds++
		}
	}
	if builds != 2 {
		t.Fatalf("a failed verification produced %d build turns, want 2", builds)
	}
	if state.Status != RunAwaitingApproval {
		t.Fatalf("the repaired run did not reach the publish gate: %+v", state)
	}
}

func TestRepairIsBoundedAndThenParksForTheOperator(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	fixture.allowRepairRounds(t, 2)
	// Never passes. The controller must stop spending rounds and ask.
	fixture.runtime.verdicts = []verification.Verdict{
		verification.VerdictFailed, verification.VerdictFailed,
		verification.VerdictFailed, verification.VerdictFailed,
	}
	state := fixture.start(t)

	if state.Gate == nil || state.Gate.Reason != ReasonWaitingForRepair {
		t.Fatalf("the run did not park at a repair gate: %+v", state.Gate)
	}
	builds := 0
	for _, launch := range fixture.runtime.launches {
		if launch.Component == ComponentBuild {
			builds++
		}
	}
	if builds != 2 {
		t.Fatalf("the bound spent %d build rounds, want 2", builds)
	}
	if fixture.runtime.publishes != 0 {
		t.Fatal("a run that never verified reached publication")
	}
}

func TestTheDefaultBoardSpendsNoAutomaticRepairRound(t *testing.T) {
	// The default board allows one Build round. A failure there is the
	// operator's decision to make, not a round the controller spends on their
	// behalf, so the run parks immediately.
	fixture := newControllerFixture(t, 1)
	fixture.runtime.verdicts = []verification.Verdict{verification.VerdictFailed}
	state := fixture.start(t)

	builds := 0
	for _, launch := range fixture.runtime.launches {
		if launch.Component == ComponentBuild {
			builds++
		}
	}
	if builds != 1 {
		t.Fatalf("the default board spent %d build rounds, want 1", builds)
	}
	if state.Gate == nil || state.Gate.Reason != ReasonWaitingForRepair {
		t.Fatalf("the run did not park for the operator: %+v", state.Gate)
	}
}

func TestAReviewThatRequestsChangesReturnsToBuild(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	fixture.allowRepairRounds(t, 2)
	fixture.runtime.reviews = []string{ReviewChangesRequested, ReviewApproved}
	state := fixture.start(t)

	builds := 0
	for _, launch := range fixture.runtime.launches {
		if launch.Component == ComponentBuild {
			builds++
		}
	}
	if builds != 2 {
		t.Fatalf("a changes_requested review produced %d build turns, want 2", builds)
	}
	if state.Status != RunAwaitingApproval {
		t.Fatalf("the run did not reach the publish gate: %+v", state)
	}
}

func TestAnApprovalCarryingABlockingFindingIsNotAnApproval(t *testing.T) {
	// The verdict and the findings are written by the same turn and can
	// disagree. Publishing on that contradiction is the one failure that cannot
	// be undone, so the stricter half wins.
	outcome := ReviewOutcome{
		Verdict:  ReviewApproved,
		Findings: []ReviewFinding{{Severity: SeverityBlocking, Summary: "unsafe"}},
	}
	if outcome.Approved() {
		t.Fatal("an approval with a blocking finding was accepted")
	}
	clean := ReviewOutcome{Verdict: ReviewApproved}
	if !clean.Approved() {
		t.Fatal("a clean approval was refused")
	}
	if (ReviewOutcome{Verdict: "APPROVED"}).Approved() != true {
		t.Fatal("verdict comparison is case sensitive")
	}
	if (ReviewOutcome{}).Approved() {
		t.Fatal("an empty verdict was read as approval")
	}
}

func TestTheRunNeverPublishesWithoutTheOperator(t *testing.T) {
	fixture := newControllerFixture(t, 2)
	state := fixture.start(t)

	if state.Status != RunAwaitingApproval || state.Gate == nil {
		t.Fatalf("the run did not park: %+v", state)
	}
	if fixture.runtime.publishes != 0 {
		t.Fatal("the controller published without a gate decision")
	}
}

func TestOnlyOneRunPerProjectIsAccepted(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	// Park the first run at its gate, then try to start another.
	fixture.start(t)
	_, err := fixture.controller.Start(fixture.ctx, StartRequest{
		ProjectID: "demo", BoardID: "board-1", ProjectPath: t.TempDir(),
		Source: SourceInput{Kind: "text", Title: "Second", Text: "Another."},
	})
	if err == nil {
		t.Fatal("a second live run was accepted")
	}
}

func TestAStartIsRefusedBeforeTheRunExists(t *testing.T) {
	fixture := newControllerFixture(t, 1)
	for _, request := range []StartRequest{
		{ProjectID: "demo", BoardID: "board-1", Source: SourceInput{Kind: "text", Title: "", Text: "x"}},
		{ProjectID: "demo", BoardID: "board-1", Source: SourceInput{Kind: "text", Title: "t", Text: ""}},
		{ProjectID: "demo", BoardID: "missing", Source: SourceInput{Kind: "text", Title: "t", Text: "x"}},
		{ProjectID: "../etc", BoardID: "board-1", Source: SourceInput{Kind: "text", Title: "t", Text: "x"}},
		{ProjectID: "demo", BoardID: "board-1", Source: SourceInput{Kind: "carrier-pigeon", Title: "t", Text: "x"}},
	} {
		if _, err := fixture.controller.Start(fixture.ctx, request); err == nil {
			t.Fatalf("Start accepted %+v", request.Source)
		}
	}
	summaries, err := fixture.controller.ListRuns(fixture.ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("a refused start created %d runs", len(summaries))
	}
}

func TestTheRunReplaysToTheSameState(t *testing.T) {
	fixture := newControllerFixture(t, 3)
	state := fixture.start(t)

	replayed, err := fixture.runs.Rebuild(fixture.ctx, state.ProjectID, state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.LastSeq != state.LastSeq || replayed.Status != state.Status {
		t.Fatalf("replay diverged: %d/%s versus %d/%s",
			replayed.LastSeq, replayed.Status, state.LastSeq, state.Status)
	}
	if len(replayed.Artifacts) != len(state.Artifacts) {
		t.Fatalf("replay produced %d artifacts, want %d", len(replayed.Artifacts), len(state.Artifacts))
	}
}

func TestEveryAttemptIsReleased(t *testing.T) {
	fixture := newControllerFixture(t, 2)
	fixture.start(t)
	launched := len(fixture.runtime.launches)
	if fixture.runtime.released < launched {
		t.Fatalf("%d attempts launched but only %d released", launched, fixture.runtime.released)
	}
}
