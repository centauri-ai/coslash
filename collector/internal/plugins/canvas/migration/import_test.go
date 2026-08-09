package migration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/atlas"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/dagama"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// These exercise the frozen Task 00 fixtures, which characterize the legacy
// baseline at c13a3ef01438193dcdcd2e387300e69ae3c27437. Using them rather than
// hand-written shapes is what makes "the importer reads legacy data" a claim
// about the real product instead of about this test file.

const testProject = "fixture-project"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func newDaGamaBoards(t *testing.T) *dagama.BoardStore {
	t.Helper()
	scope, _ := newScope(t)
	store, err := dagama.NewBoardStore(scope, fixedNow())
	if err != nil {
		t.Fatalf("NewBoardStore: %v", err)
	}
	return store
}

func newAtlasBoards(t *testing.T) *atlas.BoardStore {
	t.Helper()
	scope, _ := newScope(t)
	store, err := atlas.NewBoardStore(scope, testProject, fixedNow())
	if err != nil {
		t.Fatalf("NewBoardStore: %v", err)
	}
	return store
}

func newDaGamaRuns(t *testing.T) (*dagama.RunStore, *runfs.Scope) {
	t.Helper()
	scope, _ := newScope(t)
	store, err := dagama.NewRunStore(scope, fixedNow())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	return store, scope
}

func newAtlasRuns(t *testing.T) (*atlas.RunStore, *runfs.Scope) {
	t.Helper()
	scope, _ := newScope(t)
	store, err := atlas.NewRunStore(scope, fixedNow())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	return store, scope
}

// importResult lets a test read a one-item import in a single expression:
// `result(ImportBoards(...)).only(t)`.
type importResult struct {
	entries []Entry
	err     error
}

func result(entries []Entry, err error) importResult { return importResult{entries, err} }

func (r importResult) only(t *testing.T) Entry {
	t.Helper()
	if r.err != nil {
		t.Fatalf("import: %v", r.err)
	}
	if len(r.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(r.entries))
	}
	return r.entries[0]
}

// ---------------------------------------------------------------------------
// Boards
// ---------------------------------------------------------------------------

func TestADaGamaBoardArrivesIntact(t *testing.T) {
	journal, _ := newJournal(t)
	boards := newDaGamaBoards(t)
	raw := fixture(t, "dagama-board-v1.json")

	entry := result(ImportBoards(t.Context(),
		[]LegacyBoard{{ID: "fixture-dagama-board", Name: "Fixture DaGama", Raw: raw}},
		BoardImport{Journal: journal, ProjectID: testProject, ProjectPath: "/tmp/fixture-project", DaGama: boards})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}

	// The destination store validated it on the way in; loading proves the
	// product can actually open what was written.
	loaded, err := boards.Load(t.Context(), testProject, "fixture-dagama-board")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "Fixture DaGama" {
		t.Fatalf("name = %q", loaded.Name)
	}
	if !strings.Contains(loaded.Instructions, "synthetic fixture project") {
		t.Fatalf("instructions did not survive: %q", loaded.Instructions)
	}
	if loaded.Components.Plan.Prompt == "" {
		t.Fatal("the plan seat prompt did not survive")
	}
}

func TestAnAtlasV1BoardIsMigratedOnTheWayIn(t *testing.T) {
	// The v1-to-v2 boundary belongs to atlas.DecodeBoard, which is tested
	// there. What this asserts is that the importer routes through it and says
	// so, rather than storing a v1 document nothing reads.
	journal, _ := newJournal(t)
	boards := newAtlasBoards(t)

	entry := result(ImportBoards(t.Context(),
		[]LegacyBoard{{ID: "legacy-atlas", Name: "Legacy Atlas", Raw: fixture(t, "atlas-board-v1.json")}},
		BoardImport{Journal: journal, ProjectID: testProject, Atlas: boards})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}
	if len(entry.Warnings) == 0 || !strings.Contains(strings.Join(entry.Warnings, " "), "upgraded") {
		t.Fatalf("the migration was not reported: %v", entry.Warnings)
	}

	loaded, err := boards.Load(t.Context(), "legacy-atlas")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Board.SchemaVersion != atlas.BoardSchemaVersion {
		t.Fatalf("schema = %d, want %d", loaded.Board.SchemaVersion, atlas.BoardSchemaVersion)
	}
	if len(loaded.Board.Components) == 0 {
		t.Fatal("the migrated graph has no components")
	}
}

func TestAnAtlasV2BoardIsNotReportedAsMigrated(t *testing.T) {
	journal, _ := newJournal(t)
	boards := newAtlasBoards(t)

	entry := result(ImportBoards(t.Context(),
		[]LegacyBoard{{ID: "atlas-v2", Name: "Atlas v2", Raw: fixture(t, "atlas-board-v2.json")}},
		BoardImport{Journal: journal, ProjectID: testProject, Atlas: boards})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}
	if strings.Contains(strings.Join(entry.Warnings, " "), "upgraded") {
		t.Fatalf("a v2 board was reported as migrated: %v", entry.Warnings)
	}
}

func TestReimportingABoardWritesNothingNew(t *testing.T) {
	// The exit gate: running the migration twice produces no duplicate boards.
	journal, _ := newJournal(t)
	boards := newAtlasBoards(t)
	legacy := []LegacyBoard{{ID: "atlas-v2", Name: "Atlas v2", Raw: fixture(t, "atlas-board-v2.json")}}
	importer := BoardImport{Journal: journal, ProjectID: testProject, Atlas: boards}

	result(ImportBoards(t.Context(), legacy, importer)).only(t)
	entry := result(ImportBoards(t.Context(), legacy, importer)).only(t)
	if entry.Outcome != AlreadyPresent {
		t.Fatalf("outcome = %q", entry.Outcome)
	}

	loaded, err := boards.Load(t.Context(), "atlas-v2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A second write would have advanced the revision.
	if loaded.Revision != 1 {
		t.Fatalf("revision = %d, want 1", loaded.Revision)
	}
	summaries, _, err := boards.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("boards = %d, want 1", len(summaries))
	}
}

func TestAColidingBoardIdIsRemappedDeterministically(t *testing.T) {
	// The destination is occupied by something this migration did not import.
	// Overwriting it is not an option and neither is dropping the legacy board.
	journal, _ := newJournal(t)
	boards := newAtlasBoards(t)
	graph, err := atlas.DecodeBoard(fixture(t, "atlas-board-v2.json"))
	if err != nil {
		t.Fatalf("DecodeBoard: %v", err)
	}
	if _, err := boards.Save(t.Context(), &atlas.BoardDocument{
		ID: "atlas-v2", Name: "Already mine", ProjectID: testProject, Board: graph,
	}, 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	entry := result(ImportBoards(t.Context(),
		[]LegacyBoard{{ID: "atlas-v2", Name: "Legacy", Raw: fixture(t, "atlas-board-v2.json")}},
		BoardImport{Journal: journal, ProjectID: testProject, Atlas: boards})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}
	if entry.DestinationID == "atlas-v2" {
		t.Fatal("the legacy board took the occupied id")
	}
	if entry.DestinationID != DerivedID("board", "atlas-v2") {
		t.Fatalf("destination %q is not the derived id", entry.DestinationID)
	}
	// A board that changed id is the most confusing thing a migration can do
	// silently, so the remap has to be journaled.
	if !strings.Contains(strings.Join(entry.Warnings, " "), "already occupies") {
		t.Fatalf("the remap was not reported: %v", entry.Warnings)
	}

	existing, err := boards.Load(t.Context(), "atlas-v2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if existing.Name != "Already mine" {
		t.Fatalf("the operator's board was replaced: %q", existing.Name)
	}
}

func TestAnUndecodableBoardIsLeftWhereItIs(t *testing.T) {
	journal, _ := newJournal(t)
	boards := newAtlasBoards(t)

	entry := result(ImportBoards(t.Context(),
		[]LegacyBoard{{ID: "broken", Name: "Broken", Raw: []byte(`{"kind":"atlas","schemaVersion":`)}},
		BoardImport{Journal: journal, ProjectID: testProject, Atlas: boards})).only(t)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "could not be decoded") {
		t.Fatalf("entry = %+v", entry)
	}
	if _, err := boards.Load(t.Context(), "broken"); err == nil {
		t.Fatal("an undecodable board was written")
	}
}

func TestABoardImportNeedsExactlyOneDestination(t *testing.T) {
	journal, _ := newJournal(t)
	cases := []BoardImport{
		{Journal: journal, ProjectID: testProject},
		{Journal: journal, ProjectID: testProject, DaGama: newDaGamaBoards(t), Atlas: newAtlasBoards(t)},
		{Journal: journal, Atlas: newAtlasBoards(t)},
		{ProjectID: testProject, Atlas: newAtlasBoards(t)},
	}
	for index, importer := range cases {
		if _, err := ImportBoards(t.Context(), nil, importer); err == nil {
			t.Fatalf("case %d was accepted", index)
		}
	}
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

func TestAnImportedRunIsReadable(t *testing.T) {
	// The importer writes the log the store reads, so the two have to agree on
	// both the envelope shape and where the log lives. This is the test that
	// fails if either changes.
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)

	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef01", Events: fixture(t, "dagama-lifecycle-events.jsonl")}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}

	state, err := runs.Read(t.Context(), testProject, "run-20260801t120000-abcdef01")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.Status != dagama.RunSucceeded {
		t.Fatalf("status = %q, want succeeded", state.Status)
	}
	// The evidence is the point: a summary would have kept the outcome and lost
	// which stages ran.
	if state.Components[dagama.ComponentReview].Status != dagama.ComponentSucceededStatus {
		t.Fatalf("review = %q", state.Components[dagama.ComponentReview].Status)
	}
	if state.Gate == nil || state.Gate.Decision != dagama.GateApproved {
		t.Fatalf("the publish gate did not survive: %+v", state.Gate)
	}
	if state.LastSeq != 25 {
		t.Fatalf("lastSeq = %d, want 25", state.LastSeq)
	}
}

func TestAnAtlasCommitteeRunKeepsItsAttributedAttempts(t *testing.T) {
	journal, _ := newJournal(t)
	runs, scope := newAtlasRuns(t)

	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260802t120000-abcdef02", Events: fixture(t, "atlas-committee-events.jsonl")}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, Atlas: runs})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}

	state, err := runs.Read(t.Context(), testProject, "run-20260802t120000-abcdef02")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	plan := state.Components[atlas.ComponentPlan]
	if plan == nil {
		t.Fatal("the plan stage did not survive")
	}
	// A committee is several attributed attempts. Losing the attribution would
	// make the fan-out unreadable after import.
	if len(plan.Attempts) < 2 {
		t.Fatalf("attempts = %d, want the committee's siblings", len(plan.Attempts))
	}
	seats := map[string]bool{}
	for _, attempt := range plan.Attempts {
		seats[attempt.SeatID] = true
	}
	if len(seats) < 2 {
		t.Fatalf("attempts collapsed onto one seat: %v", seats)
	}
}

func TestARunStillInFlightIsClosedAsInterrupted(t *testing.T) {
	// The process it described ended when the legacy app stopped. Leaving it
	// open would invite every control to offer to advance what cannot advance.
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)

	// The lifecycle fixture without its terminal event is a run left mid-flight.
	full := string(fixture(t, "dagama-lifecycle-events.jsonl"))
	lines := strings.Split(strings.TrimRight(full, "\n"), "\n")
	partial := strings.Join(lines[:len(lines)-1], "\n") + "\n"

	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef03", Events: []byte(partial)}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}
	if !strings.Contains(strings.Join(entry.Warnings, " "), "interrupted_migration") {
		t.Fatalf("the conversion was not reported: %v", entry.Warnings)
	}

	state, err := runs.Read(t.Context(), testProject, "run-20260801t120000-abcdef03")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.Status != dagama.RunInterruptedImport {
		t.Fatalf("status = %q, want interrupted_migration", state.Status)
	}
	// And it never resumes: the store refuses every further event.
	if _, err := runs.Append(t.Context(), testProject, "run-20260801t120000-abcdef03",
		&dagama.ComponentReady{}); err == nil {
		t.Fatal("an imported run accepted further work")
	}
}

func TestReimportingARunWritesNothingNew(t *testing.T) {
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)
	legacy := []LegacyRun{{ID: "run-20260801t120000-abcdef04", Events: fixture(t, "dagama-lifecycle-events.jsonl")}}
	importer := RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs}

	result(ImportRuns(t.Context(), legacy, importer)).only(t)
	before, err := runs.Read(t.Context(), testProject, "run-20260801t120000-abcdef04")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	entry := result(ImportRuns(t.Context(), legacy, importer)).only(t)
	if entry.Outcome != AlreadyPresent {
		t.Fatalf("outcome = %q", entry.Outcome)
	}
	after, err := runs.Read(t.Context(), testProject, "run-20260801t120000-abcdef04")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if after.LastSeq != before.LastSeq {
		t.Fatalf("the log grew on rerun: %d -> %d", before.LastSeq, after.LastSeq)
	}
}

func TestLosingTheJournalStillProducesNoDuplicateRun(t *testing.T) {
	// A run's identity IS its history, so a rerun with no journal recognizes
	// the log it already wrote instead of placing a second copy beside it.
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)
	legacy := []LegacyRun{{ID: "run-20260801t120000-abcdef05", Events: fixture(t, "dagama-lifecycle-events.jsonl")}}
	importer := RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs}
	result(ImportRuns(t.Context(), legacy, importer)).only(t)

	fresh, _ := newJournal(t)
	importer.Journal = fresh
	entry := result(ImportRuns(t.Context(), legacy, importer)).only(t)
	if entry.Outcome != AlreadyPresent {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.DestinationID != "run-20260801t120000-abcdef05" {
		t.Fatalf("the rerun moved the run to %q", entry.DestinationID)
	}

	summaries, err := runs.List(t.Context(), testProject)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("runs = %d, want 1", len(summaries))
	}
}

func TestARunWithADifferentHistoryIsRemappedNotOverwritten(t *testing.T) {
	// Same id, different run. Replacing the stored log would destroy history
	// that is not ours; dropping the legacy one would lose history that is.
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)
	importer := RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs}

	full := fixture(t, "dagama-lifecycle-events.jsonl")
	lines := strings.Split(strings.TrimRight(string(full), "\n"), "\n")
	shorter := []byte(strings.Join(lines[:6], "\n") + "\n")

	result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef10", Events: shorter}}, importer)).only(t)

	fresh, _ := newJournal(t)
	importer.Journal = fresh
	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef10", Events: full}}, importer)).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.DestinationID != DerivedRunID("run-20260801t120000-abcdef10") {
		t.Fatalf("destination = %q", entry.DestinationID)
	}
	if !strings.Contains(strings.Join(entry.Warnings, " "), "already occupies") {
		t.Fatalf("the remap was not reported: %v", entry.Warnings)
	}

	// The run that was already there is untouched: still the six imported
	// events plus the closing one its own import appended, and still terminal.
	original, err := runs.Read(t.Context(), testProject, "run-20260801t120000-abcdef10")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if original.LastSeq != 7 {
		t.Fatalf("the stored run was rewritten: lastSeq = %d", original.LastSeq)
	}
	if original.Status != dagama.RunInterruptedImport {
		t.Fatalf("the stored run changed status: %q", original.Status)
	}
}

func TestACorruptRunLogIsRefusedWhole(t *testing.T) {
	// Importing a corrupt log would attest to a history that never happened.
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)

	corrupt := "{\"type\":\"run_created\",\"seq\":1,\"at\":\"2026-08-01T12:00:00Z\"}\nnot json\n"
	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef06", Events: []byte(corrupt)}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs})).only(t)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "unreadable") {
		t.Fatalf("entry = %+v", entry)
	}
	if _, err := runs.Read(t.Context(), testProject, "run-20260801t120000-abcdef06"); err == nil {
		t.Fatal("a corrupt log reached the destination")
	}
}

func TestAGapInTheSequenceIsRefused(t *testing.T) {
	// Gapless sequence is what makes the log an authority rather than a pile of
	// records; a gap means something was removed.
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)

	gapped := "{\"type\":\"run_created\",\"seq\":1,\"at\":\"2026-08-01T12:00:00Z\"}\n" +
		"{\"type\":\"component_ready\",\"componentId\":\"plan\",\"instance\":1,\"seq\":5,\"at\":\"2026-08-01T12:00:01Z\"}\n"
	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef07", Events: []byte(gapped)}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs})).only(t)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "seq") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestATornFinalRunEventIsDropped(t *testing.T) {
	// The legacy app may have been killed mid-append; that record was never
	// durable, and the rest of the run is still good history.
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)

	torn := string(fixture(t, "dagama-lifecycle-events.jsonl")) + `{"type":"component_re`
	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef08", Events: []byte(torn)}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}
	state, err := runs.Read(t.Context(), testProject, "run-20260801t120000-abcdef08")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.LastSeq != 25 {
		t.Fatalf("lastSeq = %d, want 25", state.LastSeq)
	}
}

func TestAnEmptyRunLogIsSkipped(t *testing.T) {
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)
	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "run-20260801t120000-abcdef09", Events: nil}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs})).only(t)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "empty") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestAnUnusableRunIdIsRemapped(t *testing.T) {
	journal, _ := newJournal(t)
	runs, scope := newDaGamaRuns(t)
	entry := result(ImportRuns(t.Context(),
		[]LegacyRun{{ID: "legacy-dagama-active", Events: fixture(t, "dagama-lifecycle-events.jsonl")}},
		RunImport{Journal: journal, ProjectID: testProject, Scope: scope, DaGama: runs})).only(t)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}
	if entry.DestinationID != DerivedRunID("legacy-dagama-active") {
		t.Fatalf("destination = %q", entry.DestinationID)
	}
	if !strings.Contains(strings.Join(entry.Warnings, " "), "not a valid coSlash") {
		t.Fatalf("the remap was not reported: %v", entry.Warnings)
	}
	if _, err := runs.Read(t.Context(), testProject, entry.DestinationID); err != nil {
		t.Fatalf("the remapped run is unreadable: %v", err)
	}
}

func TestADerivedIdIsStableAcrossPasses(t *testing.T) {
	// A random id would leave a duplicate behind every interruption.
	if DerivedID("run", "legacy-1") != DerivedID("run", "legacy-1") {
		t.Fatal("the derived id is not stable")
	}
	// Namespaced, so the same legacy id used for a board and a run does not
	// collapse to one destination.
	if DerivedID("run", "legacy-1") == DerivedID("board", "legacy-1") {
		t.Fatal("namespaces collide")
	}
}
