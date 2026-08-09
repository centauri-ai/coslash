package hardening

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/dagama"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/migration"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/persistence"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Migration acceptance rows.
//
// ACCEPTANCE.md states two:
//
//	"Legacy import is idempotent, reports conflicts, never overwrites
//	originals, and never restarts old live agents."
//	"Browser export excludes secrets and imports only allowlisted schemas."
//
// The migration package tests each mechanism. These assert the four promises
// end to end, against real stores, because "idempotent" and "never overwrites"
// are claims about a whole pass rather than about any one function.

func migrationFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "migration", "testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return raw
}

func migrationJournal(t *testing.T) (*migration.Journal, *runfs.Scope) {
	t.Helper()
	scope := atlasScope(t)
	journal, err := migration.OpenJournal(scope, atlasClock())
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	return journal, scope
}

// Row: "Legacy import is idempotent."
func TestLegacyImportIsIdempotentAcrossEveryKind(t *testing.T) {
	journal, _ := migrationJournal(t)
	boardScope := atlasScope(t)
	boards, err := dagama.NewBoardStore(boardScope, atlasClock())
	if err != nil {
		t.Fatalf("NewBoardStore: %v", err)
	}
	runScope := atlasScope(t)
	runs, err := dagama.NewRunStore(runScope, atlasClock())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	legacyBoards := []migration.LegacyBoard{{
		ID: "fixture-dagama-board", Name: "Fixture", Raw: migrationFixture(t, "dagama-board-v1.json"),
	}}
	legacyRuns := []migration.LegacyRun{{
		ID: "run-20260801t120000-abcdef01", Events: migrationFixture(t, "dagama-lifecycle-events.jsonl"),
	}}
	boardImport := migration.BoardImport{
		Journal: journal, ProjectID: "demo", ProjectPath: "/tmp/demo", DaGama: boards,
	}
	runImport := migration.RunImport{
		Journal: journal, ProjectID: "demo", Scope: runScope, DaGama: runs,
	}

	for pass := 1; pass <= 3; pass++ {
		boardEntries, err := migration.ImportBoards(context.Background(), legacyBoards, boardImport)
		if err != nil {
			t.Fatalf("pass %d boards: %v", pass, err)
		}
		runEntries, err := migration.ImportRuns(context.Background(), legacyRuns, runImport)
		if err != nil {
			t.Fatalf("pass %d runs: %v", pass, err)
		}
		if pass == 1 {
			for _, entry := range append(boardEntries, runEntries...) {
				if entry.Outcome != migration.Imported {
					t.Fatalf("first pass %s: %q (%s)", entry.SourceID, entry.Outcome, entry.Reason)
				}
			}
			continue
		}
		// Every later pass writes nothing. Three passes rather than two,
		// because an off-by-one in the ledger would still look right at two.
		for _, entry := range append(boardEntries, runEntries...) {
			if entry.Outcome != migration.AlreadyPresent {
				t.Fatalf("pass %d %s: %q (%s)", pass, entry.SourceID, entry.Outcome, entry.Reason)
			}
		}
	}

	board, err := boards.Load(context.Background(), "demo", "fixture-dagama-board")
	if err != nil {
		t.Fatalf("Load board: %v", err)
	}
	if board.Revision != 1 {
		t.Fatalf("board revision = %d after three passes, want 1", board.Revision)
	}
	summaries, err := runs.List(context.Background(), "demo")
	if err != nil {
		t.Fatalf("List runs: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("runs = %d after three passes, want 1", len(summaries))
	}
}

// Row: "... reports conflicts, never overwrites originals."
func TestLegacyImportNeverOverwritesWhatIsAlreadyThere(t *testing.T) {
	journal, _ := migrationJournal(t)

	workspaceRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	workspaces, err := persistence.Open(context.Background(),
		filepath.Join(workspaceRoot, "canvas"), persistence.Options{Now: atlasClock()})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	t.Cleanup(func() { _ = workspaces.Close() })

	session := contracts.SessionIdentity{Agent: "claude", ID: "aaaa1111-bbbb-2222-cccc-333344445555"}
	mine := json.RawMessage(`{"version":1,"authoredInCoslash":true}`)
	if _, err := workspaces.Save(context.Background(), session, contracts.WorkspaceWrite{
		SchemaVersion: persistence.SchemaVersion, ExpectedRevision: 0, State: mine,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bundle := browserBundle(t, bundleRecordFixture{
		Key:    "fleetlog.canvasWorkspace.v1:" + session.ID,
		Kind:   "workspace",
		Suffix: session.ID,
		Value:  `{"version":1,"fromLegacy":true}`,
	})
	report, err := migration.ImportBrowserState(context.Background(), bundle, migration.BrowserImport{
		Journal:    journal,
		Workspaces: workspaces,
		Resolve: func(context.Context, string) ([]contracts.SessionIdentity, error) {
			return []contracts.SessionIdentity{session}, nil
		},
	})
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	if len(report.Entries) != 1 || report.Entries[0].Outcome != migration.Skipped {
		t.Fatalf("entries = %+v", report.Entries)
	}
	// Reported, not silent: an operator has to be able to see the collision.
	if !strings.Contains(report.Entries[0].Reason, "will not overwrite") {
		t.Fatalf("the conflict was not reported: %s", report.Entries[0].Reason)
	}

	stored, err := workspaces.Load(context.Background(), session)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(string(stored.State), "authoredInCoslash") {
		t.Fatalf("the operator's workspace was replaced: %s", stored.State)
	}
	if stored.Revision != 1 {
		t.Fatalf("revision = %d; the import wrote", stored.Revision)
	}
}

// Row: "... never restarts old live agents."
func TestAnImportedLiveRunCanNeverBeRestarted(t *testing.T) {
	// The process a legacy run described ended when the legacy app stopped.
	// Importing it must not produce something any control will try to advance.
	journal, _ := migrationJournal(t)
	runScope := atlasScope(t)
	runs, err := dagama.NewRunStore(runScope, atlasClock())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	full := string(migrationFixture(t, "dagama-lifecycle-events.jsonl"))
	lines := strings.Split(strings.TrimRight(full, "\n"), "\n")
	// Cut before the terminal event: a run that was still in flight.
	live := strings.Join(lines[:len(lines)-1], "\n") + "\n"

	entries, err := migration.ImportRuns(context.Background(),
		[]migration.LegacyRun{{ID: "run-20260801t120000-abcdef01", Events: []byte(live)}},
		migration.RunImport{Journal: journal, ProjectID: "demo", Scope: runScope, DaGama: runs})
	if err != nil {
		t.Fatalf("ImportRuns: %v", err)
	}
	if entries[0].Outcome != migration.Imported {
		t.Fatalf("entry = %+v", entries[0])
	}

	state, err := runs.Read(context.Background(), "demo", "run-20260801t120000-abcdef01")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.Status != dagama.RunInterruptedImport {
		t.Fatalf("status = %q, want interrupted_migration", state.Status)
	}
	// Every control gates on the store refusing further events. Asserting the
	// refusal directly is what makes "never restarts" a property rather than a
	// hope about each caller.
	refusals := []dagama.Payload{
		&dagama.ComponentReady{},
		&dagama.CancelRequested{},
		&dagama.RunFinished{Status: dagama.RunSucceeded},
	}
	for _, payload := range refusals {
		if _, err := runs.Append(context.Background(), "demo", "run-20260801t120000-abcdef01", payload); err == nil {
			t.Fatalf("an imported run accepted %s", payload.EventType())
		}
	}
}

// Row: "Browser export excludes secrets and imports only allowlisted schemas."
func TestBrowserImportRefusesAnythingItDoesNotRecognize(t *testing.T) {
	journal, _ := migrationJournal(t)
	workspaceRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	workspaces, err := persistence.Open(context.Background(),
		filepath.Join(workspaceRoot, "canvas"), persistence.Options{Now: atlasClock()})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	t.Cleanup(func() { _ = workspaces.Close() })

	importer := migration.BrowserImport{
		Journal:    journal,
		Workspaces: workspaces,
		Resolve: func(context.Context, string) ([]contracts.SessionIdentity, error) {
			return nil, nil
		},
	}

	// A bundle whose envelope this build does not understand is refused whole,
	// because its records may mean something else.
	future := strings.Replace(string(browserBundle(t)), `"schemaVersion":1`, `"schemaVersion":99`, 1)
	if _, err := migration.ImportBrowserState(context.Background(), []byte(future), importer); err == nil {
		t.Fatal("a bundle from a newer schema was imported")
	}
	foreign := strings.Replace(string(browserBundle(t)), `"fleetlog-canvas"`, `"somewhere-else"`, 1)
	if _, err := migration.ImportBrowserState(context.Background(), []byte(foreign), importer); err == nil {
		t.Fatal("a bundle from another source was imported")
	}

	// And a record inside a valid bundle cannot invent a kind: the bundle
	// arrives from a page, so asserting a kind must not confer a destination.
	report, err := migration.ImportBrowserState(context.Background(), browserBundle(t, bundleRecordFixture{
		Key: "fleetlog.credentials", Kind: "credentials", Value: `{"apiKey":"sk-live-SECRET"}`,
	}), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	if report.Entries[0].Outcome != migration.Skipped {
		t.Fatalf("entry = %+v", report.Entries[0])
	}
	// The refusal is recorded without the value: a journal that quoted the
	// payload would put the secret on disk in the one file kept forever.
	raw, err := json.Marshal(report.Entries[0])
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	if strings.Contains(string(raw), "sk-live-SECRET") {
		t.Fatalf("the journal entry carries the secret: %s", raw)
	}
}

// bundleRecordFixture mirrors one exported record.
type bundleRecordFixture struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Suffix string `json:"suffix"`
	Value  string `json:"value"`
}

// browserBundle builds an export bundle in the shape export.ts produces.
func browserBundle(t *testing.T, records ...bundleRecordFixture) []byte {
	t.Helper()
	payload := map[string]any{
		"schemaVersion": 1,
		"source":        "fleetlog-canvas",
		"exportedAt":    "2026-08-09T18:30:00Z",
		"records":       records,
		"refused":       []any{},
		"unrecognized":  []string{},
		"truncated":     false,
	}
	if records == nil {
		payload["records"] = []any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return raw
}
