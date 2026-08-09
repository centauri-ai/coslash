package atlas

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

func testScope(t *testing.T) (string, *runfs.Scope) {
	t.Helper()
	root := t.TempDir()
	scope, err := runfs.OpenScope(root, runfs.ScopeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scope.Close() })
	return root, scope
}

func incrementingClock() func() time.Time {
	base := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	var ticks atomic.Int64
	return func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Second) }
}

func requireAtlasCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error = %v, want Atlas code %s", err, code)
	}
	return typed
}

func TestBoardStoreRevisionConflictAndConcurrentWriters(t *testing.T) {
	_, scope := testScope(t)
	store, err := NewBoardStore(scope, "project", incrementingClock())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Save(context.Background(), &BoardDocument{ID: "board", Name: "Board", Board: DefaultBoard()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("revision = %d, want 1", created.Revision)
	}

	const writers = 12
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			writer, newErr := NewBoardStore(scope, "project", incrementingClock())
			if newErr != nil {
				t.Errorf("writer %d: %v", index, newErr)
				return
			}
			_, saveErr := writer.Save(context.Background(), &BoardDocument{ID: "board", Name: "winner", Board: DefaultBoard()}, 1)
			if saveErr == nil {
				successes.Add(1)
				return
			}
			var typed *Error
			if errors.As(saveErr, &typed) && typed.Code == CodeRevisionConflict {
				conflicts.Add(1)
				return
			}
			t.Errorf("writer %d: %v", index, saveErr)
		}(index)
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || conflicts.Load() != writers-1 {
		t.Fatalf("successes=%d conflicts=%d", successes.Load(), conflicts.Load())
	}
	loaded, err := store.Load(context.Background(), "board")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 2 || loaded.Name != "winner" {
		t.Fatalf("stored board = rev %d name %q", loaded.Revision, loaded.Name)
	}
	if atlasWriteLocks.size() != 0 {
		t.Fatalf("board lock registry retained %d entries", atlasWriteLocks.size())
	}
}

func TestBoardStoreRejectsCorruptionAndSymlinkedStorage(t *testing.T) {
	root, scope := testScope(t)
	store, err := NewBoardStore(scope, "project", incrementingClock())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(context.Background(), &BoardDocument{ID: "board", Name: "Board", Board: DefaultBoard()}, 0); err != nil {
		t.Fatal(err)
	}
	location := filepath.Join(root, filepath.FromSlash(BoardsDirectory), "board.json")
	legacyBytes, err := os.ReadFile(location)
	if err != nil {
		t.Fatal(err)
	}
	var legacyEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(legacyBytes, &legacyEnvelope); err != nil {
		t.Fatal(err)
	}
	delete(legacyEnvelope, "projectId")
	legacyBytes, err = json.MarshalIndent(legacyEnvelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(location, append(legacyBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.Load(context.Background(), "board")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ProjectID != "project" {
		t.Fatalf("legacy board project = %q, want project", legacy.ProjectID)
	}
	if err := os.WriteFile(location, []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = store.Load(context.Background(), "board")
	requireAtlasCode(t, err, CodeCorruptDocument)

	target := t.TempDir()
	symlinkRoot := t.TempDir()
	if err := os.Symlink(target, filepath.Join(symlinkRoot, ".coslash")); err != nil {
		t.Fatal(err)
	}
	symlinkScope, err := runfs.OpenScope(symlinkRoot, runfs.ScopeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer symlinkScope.Close()
	symlinkStore, err := NewBoardStore(symlinkScope, "project", incrementingClock())
	if err != nil {
		t.Fatal(err)
	}
	_, err = symlinkStore.Save(context.Background(), &BoardDocument{ID: "board", Name: "Board", Board: DefaultBoard()}, 0)
	requireAtlasCode(t, err, CodeUnsafePath)
}

func TestRunStoreRepairsStaleViewAndSerializesTransitions(t *testing.T) {
	root, scope := testScope(t)
	clock := incrementingClock()
	store, err := NewRunStore(scope, clock)
	if err != nil {
		t.Fatal(err)
	}
	const projectID = "project"
	const runID = "run-20260809t000000-abcdef12"
	board := &BoardDocument{SchemaVersion: DocumentSchemaVersion, ID: "board", Name: "Board", ProjectID: projectID, Revision: 1, Board: DefaultBoard()}
	if err := store.Allocate(context.Background(), projectID, runID, board, map[string]string{"problem": "fixture"}); err != nil {
		t.Fatal(err)
	}
	created := &RunCreated{ProjectID: projectID, BoardID: board.ID, BoardRevision: board.Revision, Title: "Run"}
	if _, err := store.Append(context.Background(), projectID, runID, created); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash after the authoritative log append but before run.json.
	log, err := store.eventLog(projectID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(context.Background(), EventComponentReady, &ComponentReadyEvent{ComponentID: ComponentPlan, Instance: 1}); err != nil {
		t.Fatal(err)
	}
	read, err := store.Read(context.Background(), projectID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if read.LastSeq != 2 || read.Component(ComponentPlan).Status != ComponentReady {
		t.Fatalf("stale view was returned: seq=%d plan=%s", read.LastSeq, read.Component(ComponentPlan).Status)
	}

	// A view with the current sequence is still only a cache. If it is edited
	// on disk, the event replay must win and repair it.
	viewPath := filepath.Join(root, projectID, RunsDirectory, runID, runViewName)
	forgedBytes, err := os.ReadFile(viewPath)
	if err != nil {
		t.Fatal(err)
	}
	var forged RunState
	if err := json.Unmarshal(forgedBytes, &forged); err != nil {
		t.Fatal(err)
	}
	forged.Status = RunSucceeded
	forgedBytes, err = json.MarshalIndent(&forged, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(viewPath, append(forgedBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	trusted, err := store.Read(context.Background(), projectID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if trusted.Status == RunSucceeded {
		t.Fatal("a forged materialized status overrode the event log")
	}
	repairedBytes, err := os.ReadFile(viewPath)
	if err != nil {
		t.Fatal(err)
	}
	var repaired RunState
	if err := json.Unmarshal(repairedBytes, &repaired); err != nil {
		t.Fatal(err)
	}
	if repaired.Status != trusted.Status {
		t.Fatalf("repaired view status = %q, want %q", repaired.Status, trusted.Status)
	}

	const writers = 10
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			writer, newErr := NewRunStore(scope, incrementingClock())
			if newErr != nil {
				t.Errorf("writer %d: %v", index, newErr)
				return
			}
			_, appendErr := writer.Append(context.Background(), projectID, runID, &AttemptLaunchRequested{
				ComponentID: ComponentPlan, Instance: 1, SeatID: "worker", Attempt: 1, AttemptID: "same-attempt",
			})
			if appendErr == nil {
				successes.Add(1)
				return
			}
			var typed *Error
			if errors.As(appendErr, &typed) && typed.Code == CodeInvalidState {
				conflicts.Add(1)
				return
			}
			t.Errorf("writer %d: %v", index, appendErr)
		}(index)
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || conflicts.Load() != writers-1 {
		t.Fatalf("successes=%d conflicts=%d", successes.Load(), conflicts.Load())
	}
	if atlasWriteLocks.size() != 0 {
		t.Fatalf("run lock registry retained %d entries", atlasWriteLocks.size())
	}
	replayed, err := store.Rebuild(context.Background(), projectID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.LastSeq != read.LastSeq+1 {
		t.Fatalf("replay seq=%d, want %d", replayed.LastSeq, read.LastSeq+1)
	}

	// An unterminated final line is a crash tail, not a durable event. The next
	// append must remove it rather than concatenate valid JSON onto it.
	eventsPath := filepath.Join(root, projectID, RunsDirectory, runID, runEventsName)
	file, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"seq":999`); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := store.Append(context.Background(), projectID, runID, &AttemptLaunched{
		ComponentID: ComponentPlan, Instance: 1, SeatID: "worker", Attempt: 1, AttemptID: "same-attempt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.LastSeq != replayed.LastSeq+1 {
		t.Fatalf("tail recovery seq=%d, want %d", next.LastSeq, replayed.LastSeq+1)
	}
}

func TestRunStoreReportsDurableLogCorruption(t *testing.T) {
	root, scope := testScope(t)
	store, err := NewRunStore(scope, incrementingClock())
	if err != nil {
		t.Fatal(err)
	}
	const projectID = "project"
	const runID = "run-20260809t000000-abcdef12"
	if _, err := store.Append(context.Background(), projectID, runID, &RunCreated{
		ProjectID: projectID, BoardID: "board", BoardRevision: 1, Title: "Run",
	}); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(root, projectID, RunsDirectory, runID, runEventsName)
	file, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{broken}\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(context.Background(), projectID, runID)
	requireAtlasCode(t, err, CodeCorruptDocument)
}

func TestRunStoreRejectsMutableAndCrossProjectSnapshotsAndEvents(t *testing.T) {
	root, scope := testScope(t)
	store, err := NewRunStore(scope, incrementingClock())
	if err != nil {
		t.Fatal(err)
	}
	const runID = "run-20260809t000000-abcdef12"
	foreign := &BoardDocument{ID: "board", Name: "Board", ProjectID: "other", Revision: 1, Board: DefaultBoard()}
	if err := store.Allocate(context.Background(), "project", runID, foreign, nil); err == nil {
		t.Fatal("cross-project board snapshot was accepted")
	}
	local := &BoardDocument{SchemaVersion: DocumentSchemaVersion, ID: "board", Name: "Board", ProjectID: "project", Revision: 1, Board: DefaultBoard()}
	if err := store.Allocate(context.Background(), "project", runID, local, map[string]string{"problem": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Allocate(context.Background(), "project", runID, local, map[string]string{"problem": "second"}); err == nil {
		t.Fatal("an allocation replaced immutable input snapshots")
	}

	snapshotPath := filepath.Join(root, "project", RunsDirectory, runID, boardSnapshotName)
	snapshotBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot BoardDocument
	if err := json.Unmarshal(snapshotBytes, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.ProjectID = "other"
	snapshotBytes, err = json.MarshalIndent(&snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotPath, append(snapshotBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BoardSnapshot(context.Background(), "project", runID); err == nil {
		t.Fatal("a cross-project board snapshot was returned")
	}
	_, err = store.Append(context.Background(), "project", runID, &RunCreated{ProjectID: "other", BoardID: "board", BoardRevision: 1, Title: "Run"})
	requireAtlasCode(t, err, CodeInvalidState)
}
