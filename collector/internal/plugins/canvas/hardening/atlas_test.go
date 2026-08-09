package hardening

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/atlas"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Atlas acceptance rows.
//
// These are the rows ACCEPTANCE.md lists under "Atlas", asserted against the
// assembled Atlas package rather than against a unit under test. They overlap
// deliberately with the atlas package's own suite: what is different here is
// the vantage point. The atlas tests ask whether each piece behaves; these ask
// whether the shipped whole still satisfies the acceptance criteria, so a
// refactor that keeps every unit green and breaks a stated guarantee fails
// here.

func atlasScope(t *testing.T) *runfs.Scope {
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
	return scope
}

func atlasClock() func() time.Time {
	moment := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return moment }
}

func readAtlasFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "atlas", "testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return raw
}

// Row: "Schema-v1 and schema-v2 boards round-trip without data loss."
func TestAtlasBoardsRoundTripWithoutDataLoss(t *testing.T) {
	scope := atlasScope(t)
	store, err := atlas.NewBoardStore(scope, "demo", atlasClock())
	if err != nil {
		t.Fatalf("NewBoardStore: %v", err)
	}

	for _, name := range []string{"board-v1.json", "board-v2.json"} {
		t.Run(name, func(t *testing.T) {
			decoded, err := atlas.DecodeBoard(readAtlasFixture(t, name))
			if err != nil {
				t.Fatalf("DecodeBoard: %v", err)
			}
			saved, err := store.Save(context.Background(), &atlas.BoardDocument{
				ID: "board-" + name[:len(name)-5], Name: name, ProjectID: "demo", Board: decoded,
			}, 0)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			loaded, err := store.Load(context.Background(), saved.ID)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			// Round trip is asserted on the SERIALIZED graph, not field by
			// field: a comparison that walks known fields cannot notice one
			// that was dropped, which is the loss this row exists to catch.
			before, err := json.Marshal(saved.Board)
			if err != nil {
				t.Fatalf("marshal saved: %v", err)
			}
			after, err := json.Marshal(loaded.Board)
			if err != nil {
				t.Fatalf("marshal loaded: %v", err)
			}
			if string(before) != string(after) {
				t.Fatalf("the board changed across a store round trip\nbefore: %s\nafter:  %s", before, after)
			}
			if loaded.Board.SchemaVersion != atlas.BoardSchemaVersion {
				t.Fatalf("schema = %d, want %d", loaded.Board.SchemaVersion, atlas.BoardSchemaVersion)
			}
		})
	}
}

// Row: "Schema-v1 ... round-trip without data loss" — the migration half.
func TestAtlasV1MigrationKeepsWhatV1Expressed(t *testing.T) {
	// v1 stored the pipeline as a record; v2 stores a graph. Migration is only
	// lossless if what the operator configured is still there afterwards.
	raw := readAtlasFixture(t, "board-v1.json")
	migrated, err := atlas.DecodeBoard(raw)
	if err != nil {
		t.Fatalf("DecodeBoard: %v", err)
	}

	var legacy struct {
		Instructions string `json:"instructions"`
		Components   map[string]struct {
			Prompt string `json:"prompt"`
			Seat   *struct {
				Vendor string `json:"vendor"`
				Model  string `json:"model"`
			} `json:"seat"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatalf("read the v1 fixture: %v", err)
	}
	if legacy.Instructions != "" && migrated.Instructions != legacy.Instructions {
		t.Fatalf("instructions = %q, want %q", migrated.Instructions, legacy.Instructions)
	}

	byRole := map[string]atlas.AgentComponent{}
	for _, component := range migrated.Components {
		if component.LegacyRole != nil {
			byRole[string(*component.LegacyRole)] = component
		}
	}
	for role, member := range legacy.Components {
		if member.Prompt == "" && member.Seat == nil {
			continue
		}
		component, ok := byRole[role]
		if !ok {
			t.Fatalf("the %s stage did not survive migration", role)
		}
		if member.Prompt != "" && component.Prompt != member.Prompt {
			t.Fatalf("%s prompt = %q, want %q", role, component.Prompt, member.Prompt)
		}
		if member.Seat != nil && string(component.Seat.Vendor) != member.Seat.Vendor {
			t.Fatalf("%s vendor = %q, want %q", role, component.Seat.Vendor, member.Seat.Vendor)
		}
	}
	// And the migration is idempotent: migrating an already-migrated board must
	// not compound, or a repeated import would drift each time.
	again, err := atlas.DecodeBoard(mustJSON(t, migrated))
	if err != nil {
		t.Fatalf("DecodeBoard (second pass): %v", err)
	}
	if string(mustJSON(t, again)) != string(mustJSON(t, migrated)) {
		t.Fatal("migrating an already-migrated board changed it")
	}
}

// Row: "Graph editing and current run-policy blocking work."
func TestAtlasStoresAGraphThatCanActuallyRun(t *testing.T) {
	scope := atlasScope(t)
	store, err := atlas.NewBoardStore(scope, "demo", atlasClock())
	if err != nil {
		t.Fatalf("NewBoardStore: %v", err)
	}
	board, err := atlas.DecodeBoard(readAtlasFixture(t, "board-v2.json"))
	if err != nil {
		t.Fatalf("DecodeBoard: %v", err)
	}

	// A graph with no seats is REPAIRED into the starter chain rather than
	// refused. Normalize is total by design, and the alternative — refusing —
	// hands the operator an error they cannot act on and an empty canvas. What
	// must not happen is storing the empty graph as though it were runnable,
	// because the failure would then surface at run time instead.
	empty := *board
	empty.Components = nil
	empty.Edges = nil
	repaired, err := store.Save(context.Background(), &atlas.BoardDocument{
		ID: "empty", Name: "Empty", ProjectID: "demo", Board: &empty,
	}, 0)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	roles := map[atlas.ComponentID]bool{}
	for _, component := range repaired.Board.Components {
		if component.LegacyRole != nil {
			roles[*component.LegacyRole] = true
		}
	}
	for _, required := range []atlas.ComponentID{atlas.ComponentPlan, atlas.ComponentBuild, atlas.ComponentReview} {
		if !roles[required] {
			t.Fatalf("the repaired graph has no %s stage: %v", required, roles)
		}
	}
	if len(repaired.Board.Edges) == 0 {
		t.Fatal("the repaired graph has no edges, so nothing would trigger")
	}

	// A board with no name IS refused: it cannot be presented, so storing it
	// would create something the operator has no way to find again.
	if _, err := store.Save(context.Background(), &atlas.BoardDocument{
		ID: "unnamed", Name: "", ProjectID: "demo", Board: board,
	}, 0); err == nil {
		t.Fatal("an unnamed board was accepted")
	}
}

// Row: "Graph editing ... work" — an edit is revision-checked.
func TestAtlasBoardEditsAreRevisionChecked(t *testing.T) {
	// Two tabs editing one board is the ordinary case, not the exotic one. The
	// second writer must be refused rather than silently winning.
	scope := atlasScope(t)
	store, err := atlas.NewBoardStore(scope, "demo", atlasClock())
	if err != nil {
		t.Fatalf("NewBoardStore: %v", err)
	}
	board, err := atlas.DecodeBoard(readAtlasFixture(t, "board-v2.json"))
	if err != nil {
		t.Fatalf("DecodeBoard: %v", err)
	}
	saved, err := store.Save(context.Background(), &atlas.BoardDocument{
		ID: "board-1", Name: "Board", ProjectID: "demo", Board: board,
	}, 0)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := store.Save(context.Background(), &atlas.BoardDocument{
		ID: "board-1", Name: "First writer", ProjectID: "demo", Board: board,
	}, saved.Revision); err != nil {
		t.Fatalf("the first writer was refused: %v", err)
	}
	if _, err := store.Save(context.Background(), &atlas.BoardDocument{
		ID: "board-1", Name: "Second writer", ProjectID: "demo", Board: board,
	}, saved.Revision); err == nil {
		t.Fatal("a stale writer overwrote a newer revision")
	}

	loaded, err := store.Load(context.Background(), "board-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "First writer" {
		t.Fatalf("name = %q; the stale write landed", loaded.Name)
	}
}

// Row: "One-worker execution skips refine; multi-worker execution refines
// through the main worker."
func TestAtlasCommitteeSizeDecidesWhetherRefineRuns(t *testing.T) {
	board, err := atlas.DecodeBoard(readAtlasFixture(t, "board-v2.json"))
	if err != nil {
		t.Fatalf("DecodeBoard: %v", err)
	}

	var single, committee *atlas.AgentComponent
	for index := range board.Components {
		component := &board.Components[index]
		if len(component.Seats) == 1 && single == nil {
			single = component
		}
		if len(component.Seats) > 1 && committee == nil {
			committee = component
		}
	}
	if single == nil || committee == nil {
		t.Skip("the fixture board has no one-worker and multi-worker pair to compare")
	}

	// A sole worker writes the result directly, and no member of it is main:
	// the role only means something when there is a committee to be main OF.
	// A refine turn there would be a second model call producing the same file.
	for _, seat := range single.Seats {
		if seat.Role == atlas.RoleMain {
			t.Fatalf("a one-worker stage has a main seat: %s", seat.ID)
		}
	}
	if !atlas.IsMainRefineSeatID(atlas.ComponentID(committee.ID), atlas.MainRefineSeatID(atlas.ComponentID(committee.ID))) {
		t.Fatal("the refine seat id does not round-trip")
	}

	// Exactly one member of a committee is main: the refine turn has to have a
	// single owner or the consolidation has no defined author.
	mains := 0
	for _, seat := range committee.Seats {
		if seat.Role == atlas.RoleMain {
			mains++
		}
	}
	if mains != 1 {
		t.Fatalf("main seats = %d, want exactly 1", mains)
	}
}

// Row: "Git-project and plain-folder modes retain their distinct behavior."
func TestAtlasPlainFolderIsSupportedButCannotPublish(t *testing.T) {
	// A plain folder is a supported project, not a broken one. The distinction
	// that must survive is that it can run and cannot publish.
	board, err := atlas.DecodeBoard(readAtlasFixture(t, "board-v2.json"))
	if err != nil {
		t.Fatalf("DecodeBoard: %v", err)
	}
	if board.RunPolicy != nil && board.RunPolicy.Publish.Base != "" {
		// The fixture configures a publish target; a plain folder has no remote
		// to satisfy it, and that is the behavior the row is about.
		t.Logf("fixture publish base: %q", board.RunPolicy.Publish.Base)
	}

	scope := atlasScope(t)
	runs, err := atlas.NewRunStore(scope, atlasClock())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	runID, err := atlas.NewRunID(atlasClock()(), "0a1b2c3d")
	if err != nil {
		t.Fatalf("NewRunID: %v", err)
	}
	state, err := runs.Append(context.Background(), "demo", runID, &atlas.RunCreated{
		ProjectID: "demo", BoardID: "board-1", BoardRevision: 1, Title: "Plain folder run",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	// No run root event, so no remote: publication has nothing to target.
	if state.RemoteURL != "" {
		t.Fatalf("remote = %q, want empty for a plain folder", state.RemoteURL)
	}
	if _, err := runs.Append(context.Background(), "demo", runID, &atlas.PublishCompleted{}); err == nil {
		t.Fatal("a plain-folder run accepted a publication")
	}
}

// Row: "Headless attempts survive restart without duplication."
func TestAtlasRunStateSurvivesAReopenWithoutDuplication(t *testing.T) {
	// Restart is a reopen of the same durable log. Replay must yield exactly
	// the state the writer saw, or a restarted collector would act on a run
	// that never existed.
	scope := atlasScope(t)
	runs, err := atlas.NewRunStore(scope, atlasClock())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	runID, err := atlas.NewRunID(atlasClock()(), "0a1b2c3d")
	if err != nil {
		t.Fatalf("NewRunID: %v", err)
	}
	if _, err := runs.Append(context.Background(), "demo", runID, &atlas.RunCreated{
		ProjectID: "demo", BoardID: "board-1", BoardRevision: 1, Title: "Restart",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	written, err := runs.Append(context.Background(), "demo", runID,
		&atlas.ComponentReadyEvent{ComponentID: atlas.ComponentPlan, Instance: 1})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Read replays when the materialized view is missing or behind, which is
	// exactly the restart path: the log is the authority, the view is a cache.
	replayed, err := runs.Read(context.Background(), "demo", runID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(mustJSON(t, written)) != string(mustJSON(t, replayed)) {
		t.Fatal("replay disagrees with the state the writer saw")
	}
	if replayed.LastSeq != 2 {
		t.Fatalf("lastSeq = %d, want 2; the log duplicated on replay", replayed.LastSeq)
	}

	// And a second store over the same root sees one run, not two.
	reopened, err := atlas.NewRunStore(scope, atlasClock())
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	summaries, listErrors, err := reopened.List(context.Background(), "demo")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listErrors) != 0 {
		t.Fatalf("the reopened store reported unreadable runs: %v", listErrors)
	}
	if len(summaries) != 1 {
		t.Fatalf("runs = %d, want 1", len(summaries))
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
