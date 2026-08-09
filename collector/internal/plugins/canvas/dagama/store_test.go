package dagama

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

func newScope(t *testing.T) (*runfs.Scope, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	scope, err := runfs.OpenScope(root, runfs.ScopeOptions{})
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}
	t.Cleanup(func() { _ = scope.Close() })
	return scope, root
}

func fixedNow() func() time.Time {
	moment := time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC)
	return func() time.Time { return moment }
}

func newBoardStore(t *testing.T) (*BoardStore, string) {
	t.Helper()
	scope, root := newScope(t)
	store, err := NewBoardStore(scope, fixedNow())
	if err != nil {
		t.Fatalf("NewBoardStore: %v", err)
	}
	return store, root
}

func newRunStore(t *testing.T) (*RunStore, string) {
	t.Helper()
	scope, root := newScope(t)
	store, err := NewRunStore(scope, fixedNow())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	return store, root
}

// ---------------------------------------------------------------------------
// Board store
// ---------------------------------------------------------------------------

func TestBoardSaveAndLoadRoundTrip(t *testing.T) {
	store, _ := newBoardStore(t)
	board := validBoard()
	board.Revision = 0

	saved, err := store.Save(t.Context(), board, 0)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Revision != 1 {
		t.Fatalf("Revision = %d, want 1 after create", saved.Revision)
	}

	loaded, err := store.Load(t.Context(), "project-1", "board-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Revision != 1 || loaded.Name != board.Name {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Components.Build.Seat.Model != "gpt-5.6-sol" {
		t.Fatalf("seat did not survive the round trip: %+v", loaded.Components.Build.Seat)
	}
}

func TestBoardSaveRejectsAStaleRevisionWithoutDamagingTheStoredBoard(t *testing.T) {
	store, _ := newBoardStore(t)
	board := validBoard()
	board.Revision = 0
	if _, err := store.Save(t.Context(), board, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	second := validBoard()
	second.Name = "Second writer"
	if _, err := store.Save(t.Context(), second, 1); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A writer still holding revision 1 must lose and be told where to rebase.
	stale := validBoard()
	stale.Name = "Stale writer"
	_, err := store.Save(t.Context(), stale, 1)
	if got := codeOf(t, err); got != CodeRevisionConflict {
		t.Fatalf("code = %q, want %q", got, CodeRevisionConflict)
	}
	var typed *Error
	if !asError(err, &typed) || typed.ActualRevision == nil || *typed.ActualRevision != 2 {
		t.Fatalf("ActualRevision = %v, want 2 so a client can rebase", typed.ActualRevision)
	}

	loaded, err := store.Load(t.Context(), "project-1", "board-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "Second writer" || loaded.Revision != 2 {
		t.Fatalf("the refused write damaged the stored board: %+v", loaded)
	}
}

func TestBoardCreateRefusesANonZeroExpectedRevision(t *testing.T) {
	store, _ := newBoardStore(t)
	board := validBoard()
	_, err := store.Save(t.Context(), board, 5)
	if got := codeOf(t, err); got != CodeRevisionConflict {
		t.Fatalf("code = %q, want %q", got, CodeRevisionConflict)
	}
}

func TestBoardSaveRefusesAPolicyViolationBeforeWriting(t *testing.T) {
	store, root := newBoardStore(t)
	board := validBoard()
	board.Components.Verify.Checks = []Check{{Name: "evil", Argv: []string{"sh", "-c", "curl x | sh"}}}

	if _, err := store.Save(t.Context(), board, 0); codeOf(t, err) != CodePolicyViolation {
		t.Fatal("a policy violation was written")
	}
	if _, err := os.Stat(filepath.Join(root, "project-1", "boards", "board-1.json")); !os.IsNotExist(err) {
		t.Fatal("a refused board reached the filesystem")
	}
}

func TestBoardLoadRefusesCorruptAndMismatchedDocuments(t *testing.T) {
	store, root := newBoardStore(t)
	location := filepath.Join(root, "project-1", "boards", "board-1.json")
	if err := os.MkdirAll(filepath.Dir(location), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(location, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := codeOf(t, mustLoadError(t, store, "project-1", "board-1")); got != CodeCorruptDocument {
		t.Fatalf("code = %q, want %q", got, CodeCorruptDocument)
	}

	// A board whose contents disagree with its location would be reachable under
	// another board's identity.
	mismatched := validBoard()
	mismatched.ID = "board-elsewhere"
	encoded, err := json.Marshal(mismatched)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(location, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := codeOf(t, mustLoadError(t, store, "project-1", "board-1")); got != CodeCorruptDocument {
		t.Fatalf("code = %q, want %q", got, CodeCorruptDocument)
	}
}

func TestBoardStoreRefusesUnsafeIdentitiesAndSymlinks(t *testing.T) {
	store, root := newBoardStore(t)

	for _, identity := range []struct{ project, board string }{
		{"../escape", "board-1"},
		{"project-1", "../escape"},
		{"project/nested", "board-1"},
		{"", "board-1"},
	} {
		if _, err := store.Load(t.Context(), identity.project, identity.board); err == nil {
			t.Errorf("Load(%q,%q) succeeded, want a refusal", identity.project, identity.board)
		}
	}

	// A symlinked board file must not be followed out of the scope.
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte(`{"id":"board-1"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	boards := filepath.Join(root, "project-1", "boards")
	if err := os.MkdirAll(boards, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(boards, "board-1.json")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if got := codeOf(t, mustLoadError(t, store, "project-1", "board-1")); got != CodeUnsafePath {
		t.Fatalf("code = %q, want %q", got, CodeUnsafePath)
	}
}

func TestBoardListSeparatesReadableFromDamaged(t *testing.T) {
	store, root := newBoardStore(t)
	for _, id := range []string{"board-1", "board-2"} {
		board := validBoard()
		board.ID = id
		if _, err := store.Save(t.Context(), board, 0); err != nil {
			t.Fatalf("Save(%s): %v", id, err)
		}
	}
	damaged := filepath.Join(root, "project-1", "boards", "board-3.json")
	if err := os.WriteFile(damaged, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	readable, unreadable, err := store.List(t.Context(), "project-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// One bad file must not make a project's other boards unreachable.
	if len(readable) != 2 || readable[0] != "board-1" || readable[1] != "board-2" {
		t.Fatalf("readable = %v", readable)
	}
	if len(unreadable) != 1 || unreadable[0] != "board-3" {
		t.Fatalf("unreadable = %v", unreadable)
	}

	empty, _, err := store.List(t.Context(), "project-absent")
	if err != nil || len(empty) != 0 {
		t.Fatalf("List(absent) = %v, %v", empty, err)
	}
}

func mustLoadError(t *testing.T, store *BoardStore, projectID, boardID string) error {
	t.Helper()
	_, err := store.Load(t.Context(), projectID, boardID)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	return err
}

func asError(err error, target **Error) bool {
	for err != nil {
		if typed, ok := err.(*Error); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// ---------------------------------------------------------------------------
// Run store
// ---------------------------------------------------------------------------

func seedRun(t *testing.T, store *RunStore) string {
	t.Helper()
	if _, err := store.Append(t.Context(), "project-1", testRunID, createdRun()); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return testRunID
}

func TestRunAppendMaterializesAndReplayAgrees(t *testing.T) {
	store, _ := newRunStore(t)
	runID := seedRun(t, store)

	if _, err := store.Append(t.Context(), "project-1", runID,
		&ComponentReady{ComponentInstance: componentRef(ComponentPlan, 1)}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	appended, err := store.Append(t.Context(), "project-1", runID,
		&ComponentStarted{ComponentInstance: componentRef(ComponentPlan, 1)})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	read, err := store.Read(t.Context(), "project-1", runID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	replayed, err := store.Replay(t.Context(), "project-1", runID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	// Deleting the view and replaying must yield the same document, which is what
	// makes the log the authority.
	appendedJSON := mustJSON(t, appended)
	if appendedJSON != mustJSON(t, read) || appendedJSON != mustJSON(t, replayed) {
		t.Fatal("append, read, and replay disagree")
	}
	if read.Components[ComponentPlan].Status != ComponentRunning {
		t.Fatalf("plan status = %q", read.Components[ComponentPlan].Status)
	}
}

func TestRunReadRepairsAStaleView(t *testing.T) {
	store, root := newRunStore(t)
	runID := seedRun(t, store)
	if _, err := store.Append(t.Context(), "project-1", runID,
		&ComponentReady{ComponentInstance: componentRef(ComponentPlan, 1)}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Simulate a crash between the log append and the view write.
	view := filepath.Join(root, "project-1", "runs", runID, runViewName)
	if err := os.WriteFile(view, []byte(`{"schemaVersion":1,"lastSeq":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	state, err := store.Read(t.Context(), "project-1", runID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.LastSeq != 2 {
		t.Fatalf("LastSeq = %d, want the log's sequence", state.LastSeq)
	}

	rewritten, err := os.ReadFile(view)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(rewritten), `"lastSeq": 2`) {
		t.Fatal("Read did not repair the stale view")
	}
}

func TestRunReadRecoversFromATornTail(t *testing.T) {
	store, root := newRunStore(t)
	runID := seedRun(t, store)
	if _, err := store.Append(t.Context(), "project-1", runID,
		&ComponentReady{ComponentInstance: componentRef(ComponentPlan, 1)}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// A crash between a write and its fsync leaves a line with no terminating
	// newline. It is not a durable event and must not be replayed.
	events := filepath.Join(root, "project-1", "runs", runID, runEventsName)
	handle, err := os.OpenFile(events, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := handle.WriteString(`{"seq":3,"at":"2026-08-09T01:00:00Z","type":"component_started"`); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	state, err := store.Read(t.Context(), "project-1", runID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.LastSeq != 2 {
		t.Fatalf("LastSeq = %d, want the torn tail ignored", state.LastSeq)
	}

	// The next append repairs the tail rather than concatenating onto it.
	next, err := store.Append(t.Context(), "project-1", runID,
		&ComponentStarted{ComponentInstance: componentRef(ComponentPlan, 1)})
	if err != nil {
		t.Fatalf("Append after torn tail: %v", err)
	}
	if next.LastSeq != 3 {
		t.Fatalf("LastSeq = %d, want a gapless sequence", next.LastSeq)
	}
}

func TestRunAppendRejectsUndefinedTransitions(t *testing.T) {
	tests := []struct {
		name    string
		prepare []Payload
		invalid Payload
	}{
		{
			name:    "started before ready",
			invalid: &ComponentStarted{ComponentInstance: componentRef(ComponentPlan, 1)},
		},
		{
			name:    "succeeded before running",
			invalid: &ComponentSucceeded{ComponentInstance: componentRef(ComponentPlan, 1)},
		},
		{
			name:    "attempt exit without a live attempt",
			invalid: &AttemptExited{AttemptRef: attemptRef(ComponentBuild, 1, "ghost"), ExitCode: 0},
		},
		{
			name: "attempt exit for another attempt",
			prepare: []Payload{
				&AttemptLaunchRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "t"},
			},
			invalid: &AttemptExited{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-2"), ExitCode: 0},
		},
		{
			name:    "gate decided with no open gate",
			invalid: &GateDecided{ComponentInstance: componentRef(ComponentPublish, 1), Decision: GateApproved},
		},
		{
			name: "gate decided twice",
			prepare: []Payload{
				&GateOpened{ComponentInstance: componentRef(ComponentPublish, 1), Reason: "approval"},
				&GateDecided{ComponentInstance: componentRef(ComponentPublish, 1), Decision: GateApproved},
			},
			invalid: &GateDecided{ComponentInstance: componentRef(ComponentPublish, 1), Decision: GateRejected},
		},
		{
			name:    "publish completed without an attempt",
			invalid: &PublishCompleted{Publication: PublicationRecord{ChangeRevision: 1}},
		},
		{
			name: "change revision that does not increase",
			prepare: []Payload{
				&ChangeCaptured{ChangeRevision: 2, TreeOID: "tree", PatchSha256: "d", BaseSha: "b"},
			},
			invalid: &ChangeCaptured{ChangeRevision: 2, TreeOID: "tree", PatchSha256: "d", BaseSha: "b"},
		},
		{
			name: "second run root",
			prepare: []Payload{
				&RunRootCreated{RunRoot: "/runs/a", Branch: "dagama/a", BaseBranch: "main", BaseSha: "abc"},
			},
			invalid: &RunRootCreated{RunRoot: "/runs/b", Branch: "dagama/b", BaseBranch: "main", BaseSha: "abc"},
		},
		{
			name:    "second creation",
			invalid: createdRun(),
		},
		{
			name:    "events after the run finished",
			prepare: []Payload{&RunFinished{Status: RunCanceled, Reason: "user", Message: "stopped"}},
			invalid: &ComponentReady{ComponentInstance: componentRef(ComponentPlan, 1)},
		},
		{
			name: "artifact outside the blob store",
			invalid: &ArtifactPromoted{Artifact: ArtifactRecord{
				ArtifactID: "a1", Kind: "plan", Name: "PLAN.md",
				Path:   ".fleetlog/run/artifacts/blobs/../../../../atlas/secret.md",
				Sha256: strings.Repeat("a", 64), Bytes: 3,
				Producer: ArtifactProducer{ComponentID: ComponentBuild, Instance: 1},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newRunStore(t)
			runID := seedRun(t, store)
			for _, payload := range test.prepare {
				if _, err := store.Append(t.Context(), "project-1", runID, payload); err != nil {
					t.Fatalf("prepare %s: %v", payload.EventType(), err)
				}
			}
			before, err := store.Read(t.Context(), "project-1", runID)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}

			if _, err := store.Append(t.Context(), "project-1", runID, test.invalid); err == nil {
				t.Fatal("an undefined transition was accepted")
			}

			after, err := store.Read(t.Context(), "project-1", runID)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			// A refused append must never reach the log, so the previous state is
			// undamaged and the sequence does not advance.
			if mustJSON(t, before) != mustJSON(t, after) {
				t.Fatal("a refused append changed the stored state")
			}
		})
	}
}

func TestRunAppendAcceptsTheHappyPipeline(t *testing.T) {
	store, _ := newRunStore(t)
	runID := seedRun(t, store)

	sequence := []Payload{
		&RunRootCreated{RunRoot: "/runs/a", Branch: "dagama/a", BaseBranch: "main", BaseSha: "abc"},
		&ComponentReady{ComponentInstance: componentRef(ComponentBuild, 1)},
		&AttemptLaunchRequested{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "t"},
		&AttemptLaunched{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), TmuxName: "t"},
		&AttemptSessionBound{
			AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"),
			SessionID:  "11111111-1111-4111-8111-111111111111",
		},
		&AttemptExited{AttemptRef: attemptRef(ComponentBuild, 1, "attempt-1"), ExitCode: 0},
		&ComponentSucceeded{ComponentInstance: componentRef(ComponentBuild, 1), Outputs: []string{"IMPLEMENTATION.md"}},
		&ChangeCaptured{ChangeRevision: 1, TreeOID: "tree", PatchSha256: "digest", BaseSha: "abc"},
		&GateOpened{ComponentInstance: componentRef(ComponentPublish, 1), Reason: "approval", Message: "ready"},
		&GateDecided{ComponentInstance: componentRef(ComponentPublish, 1), Decision: GateApproved, Message: "go"},
		&PublishCompleted{Publication: PublicationRecord{ChangeRevision: 1, CommitSha: "c", Branch: "dagama/a"}},
		&RunFinished{Status: RunSucceeded},
	}
	for _, payload := range sequence {
		if _, err := store.Append(t.Context(), "project-1", runID, payload); err != nil {
			t.Fatalf("Append(%s): %v", payload.EventType(), err)
		}
	}

	state, err := store.Read(t.Context(), "project-1", runID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.Status != RunSucceeded || state.Publication == nil || state.Failure != nil {
		t.Fatalf("state = %+v", state)
	}
	if state.LastSeq != uint64(len(sequence)+1) {
		t.Fatalf("LastSeq = %d", state.LastSeq)
	}
}

func TestRunStoreRefusesUnsafeIdentities(t *testing.T) {
	store, _ := newRunStore(t)
	for _, identity := range []struct{ project, run string }{
		{"../escape", testRunID},
		{"project-1", "../escape"},
		{"project-1", "run-not-valid"},
	} {
		if _, err := store.Append(t.Context(), identity.project, identity.run, createdRun()); err == nil {
			t.Errorf("Append(%q,%q) succeeded, want a refusal", identity.project, identity.run)
		}
	}
}

func TestRunReadReportsAMissingRun(t *testing.T) {
	store, _ := newRunStore(t)
	if got := codeOf(t, mustRunReadError(t, store)); got != CodeNotFound {
		t.Fatalf("code = %q, want %q", got, CodeNotFound)
	}
}

func mustRunReadError(t *testing.T, store *RunStore) error {
	t.Helper()
	_, err := store.Read(t.Context(), "project-1", testRunID)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	return err
}

func TestConcurrentAppendsAreSerializedAndGapless(t *testing.T) {
	store, _ := newRunStore(t)
	runID := seedRun(t, store)

	// Artifact promotions carry no ordering constraint, so every writer's event
	// is legal whatever order they interleave in; what is under test is that the
	// store loses none of them and allocates a gapless sequence.
	const writers = 8
	var group sync.WaitGroup
	failures := make([]error, writers)
	for index := range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, failures[index] = store.Append(context.Background(), "project-1", runID,
				&ArtifactPromoted{Artifact: ArtifactRecord{
					ArtifactID: fmt.Sprintf("a%d", index), Kind: "plan",
					Name:   fmt.Sprintf("PLAN-%d.md", index),
					Path:   ArtifactBlobPrefix + strings.Repeat("a", 64) + ".md",
					Sha256: strings.Repeat("a", 64), Bytes: 4,
					Producer: ArtifactProducer{ComponentID: ComponentBuild, Instance: 1},
				}})
		}()
	}
	group.Wait()

	for index, err := range failures {
		if err != nil {
			t.Fatalf("writer %d: %v", index, err)
		}
	}
	state, err := store.Read(t.Context(), "project-1", runID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(state.Artifacts) != writers {
		t.Fatalf("artifacts = %d, want %d — an append was lost", len(state.Artifacts), writers)
	}
	if state.LastSeq != uint64(writers+1) {
		t.Fatalf("LastSeq = %d, want %d", state.LastSeq, writers+1)
	}

	replayed, err := store.Replay(t.Context(), "project-1", runID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if mustJSON(t, state) != mustJSON(t, replayed) {
		t.Fatal("the view and a replay disagree after concurrent writes")
	}
}

func TestConcurrentTransitionsElectOneWinner(t *testing.T) {
	store, _ := newRunStore(t)
	runID := seedRun(t, store)
	if _, err := store.Append(t.Context(), "project-1", runID,
		&ComponentReady{ComponentInstance: componentRef(ComponentPlan, 1)}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.Append(t.Context(), "project-1", runID,
		&ComponentStarted{ComponentInstance: componentRef(ComponentPlan, 1)}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Two writers race to finish the same component. Exactly one may win, or the
	// run would record two terminal outcomes for one instance.
	const writers = 6
	var group sync.WaitGroup
	results := make([]error, writers)
	for index := range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index] = func() error {
				_, err := store.Append(context.Background(), "project-1", runID,
					&ComponentSucceeded{ComponentInstance: componentRef(ComponentPlan, 1)})
				return err
			}()
		}()
	}
	group.Wait()

	accepted := 0
	for _, err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d writers succeeded, want exactly 1", accepted)
	}
}

func TestRunListIsNewestFirstAndSkipsDamagedRuns(t *testing.T) {
	store, root := newRunStore(t)
	for _, runID := range []string{
		"run-20260809t000001-aaaaaaaa",
		"run-20260809t000002-bbbbbbbb",
	} {
		if _, err := store.Append(t.Context(), "project-1", runID, createdRun()); err != nil {
			t.Fatalf("Append(%s): %v", runID, err)
		}
	}
	damaged := filepath.Join(root, "project-1", "runs", "run-20260809t000003-cccccccc")
	if err := os.MkdirAll(damaged, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(damaged, runEventsName), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	summaries, err := store.List(t.Context(), "project-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d, want the two readable runs", len(summaries))
	}
	// Run identifiers embed a sortable timestamp, so newest is lexically last.
	if summaries[0].RunID != "run-20260809t000002-bbbbbbbb" {
		t.Fatalf("summaries[0] = %q, want the newest run", summaries[0].RunID)
	}
}

func TestNewRunID(t *testing.T) {
	at := time.Date(2026, 8, 9, 0, 45, 12, 0, time.UTC)
	runID, err := NewRunID(at, "0a1b2c3d")
	if err != nil {
		t.Fatalf("NewRunID: %v", err)
	}
	if runID != "run-20260809t004512-0a1b2c3d" {
		t.Fatalf("runID = %q", runID)
	}
	if _, err := NewRunID(at, "NOTHEX"); err == nil {
		t.Fatal("an invalid suffix was accepted")
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(encoded)
}
