package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/atlas"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/dagama"
)

// Importing legacy boards.
//
// A board is configuration, which makes it the easy half of this migration:
// there is no process to reconcile and no history to preserve. What matters is
// that a board arrives INTACT or not at all. A partially repaired pipeline that
// then runs is worse than one the operator has to re-enter, because the run it
// produces looks legitimate.
//
// So every board is decoded and policy-checked by the destination store before
// it counts as imported. Atlas gets its v1-to-v2 migration for free —
// atlas.DecodeBoard already owns that boundary and is tested there — and this
// package does not reimplement it.

// LegacyBoard is one board found in the legacy data.
type LegacyBoard struct {
	// ID is the legacy identifier. It is kept when it is usable.
	ID string
	// Name is the operator-visible title. A board with none gets its id.
	Name string
	// Raw is the stored document exactly as it was read, so the checksum in the
	// journal is over what was actually on disk.
	Raw []byte
}

// BoardImport imports boards for one product into one project.
type BoardImport struct {
	Journal *Journal
	// ProjectID is the coSlash project the boards land in.
	ProjectID string
	// ProjectPath is the project's absolute directory. DaGama stores it on the
	// board and refuses one without it; a legacy board carries the legacy
	// path, which may not exist on this machine.
	ProjectPath string
	// DaGama receives DaGama boards. Exactly one store is set.
	DaGama *dagama.BoardStore
	// Atlas receives Atlas boards.
	Atlas *atlas.BoardStore
}

// ImportBoards imports a set of legacy boards, skipping what is already done.
func ImportBoards(ctx context.Context, boards []LegacyBoard, importer BoardImport) ([]Entry, error) {
	if importer.Journal == nil {
		return nil, errors.New("migration: a board import needs a journal")
	}
	if (importer.DaGama == nil) == (importer.Atlas == nil) {
		return nil, errors.New("migration: a board import needs exactly one destination store")
	}
	if importer.ProjectID == "" {
		return nil, errors.New("migration: a board import needs a project")
	}
	if importer.DaGama != nil && importer.ProjectPath == "" {
		// A DaGama board names its project directory, and the legacy value may
		// point at a machine this collector is not running on.
		return nil, errors.New("migration: a DaGama board import needs the destination project path")
	}

	ledger, err := importer.Journal.Read(ctx)
	if err != nil {
		return nil, err
	}

	product := "atlas"
	if importer.DaGama != nil {
		product = "dagama"
	}

	entries := make([]Entry, 0, len(boards))
	for _, board := range boards {
		entry := Entry{
			Product:      product,
			Kind:         KindBoard,
			SourceID:     board.ID,
			SourceSHA256: Checksum(board.Raw),
			SourceBytes:  int64(len(board.Raw)),
		}
		if board.ID == "" {
			entry.SourceID = "(unidentified)"
			entry.Outcome = Skipped
			entry.Reason = "the legacy board has no identifier, so it cannot be placed or recognized on a rerun"
		} else if ledger.Settled(product, KindBoard, board.ID, entry.SourceSHA256) {
			previous, _ := ledger.Lookup(product, KindBoard, board.ID)
			entry.Outcome = AlreadyPresent
			entry.DestinationID = previous.DestinationID
		} else {
			entry = importer.importOne(ctx, board, entry)
		}

		recorded, err := importer.Journal.Record(ctx, entry)
		if err != nil {
			return nil, err
		}
		entries = append(entries, recorded)
	}
	return entries, nil
}

func (importer BoardImport) importOne(ctx context.Context, board LegacyBoard, entry Entry) Entry {
	name := strings.TrimSpace(board.Name)
	if name == "" {
		name = board.ID
	}
	if importer.DaGama != nil {
		return importer.importDaGama(ctx, board, name, entry)
	}
	return importer.importAtlas(ctx, board, name, entry)
}

func (importer BoardImport) importDaGama(
	ctx context.Context,
	board LegacyBoard,
	name string,
	entry Entry,
) Entry {
	destination, reason, err := ResolveID("board", board.ID, dagama.ValidBoardID, boardID, func(candidate string) (bool, error) {
		_, loadErr := importer.DaGama.Load(ctx, importer.ProjectID, candidate)
		return presence(loadErr)
	})
	if err != nil {
		entry.Outcome = Failed
		entry.Reason = err.Error()
		return entry
	}
	if reason != "" {
		entry.Warnings = append(entry.Warnings, reason)
	}
	entry.DestinationID = destination

	var decoded dagama.Board
	if err := json.Unmarshal(board.Raw, &decoded); err != nil {
		// A board that will not decode is left where it is. The legacy copy is
		// still readable by the legacy app, and a repaired guess is not.
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf("the legacy board could not be decoded: %v", err)
		return entry
	}
	decoded.ID = destination
	decoded.Name = name
	decoded.ProjectID = importer.ProjectID
	decoded.ProjectPath = importer.ProjectPath
	// The destination allocates its own revision; carrying the legacy one would
	// make the first coSlash edit look like a conflict.
	decoded.Revision = 0

	if _, err := importer.DaGama.Save(ctx, &decoded, 0); err != nil {
		return failOrSkip(entry, err, "board")
	}
	entry.Outcome = Imported
	return entry
}

func (importer BoardImport) importAtlas(
	ctx context.Context,
	board LegacyBoard,
	name string,
	entry Entry,
) Entry {
	destination, reason, err := ResolveID("board", board.ID, atlas.ValidBoardID, boardID, func(candidate string) (bool, error) {
		_, loadErr := importer.Atlas.Load(ctx, candidate)
		return presence(loadErr)
	})
	if err != nil {
		entry.Outcome = Failed
		entry.Reason = err.Error()
		return entry
	}
	if reason != "" {
		entry.Warnings = append(entry.Warnings, reason)
	}
	entry.DestinationID = destination

	// DecodeBoard owns the v1-to-v2 boundary and is tested in the atlas
	// package. Reimplementing the migration here would give the suite two
	// answers to the same question.
	graph, err := atlas.DecodeBoard(board.Raw)
	if err != nil {
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf("the legacy board could not be decoded: %v", err)
		return entry
	}
	if wasLegacySchema(board.Raw) {
		entry.Warnings = append(entry.Warnings,
			"the board was upgraded from the record-shaped v1 schema to the v2 graph")
	}

	if _, err := importer.Atlas.Save(ctx, &atlas.BoardDocument{
		ID:        destination,
		Name:      name,
		ProjectID: importer.ProjectID,
		Board:     graph,
	}, 0); err != nil {
		return failOrSkip(entry, err, "board")
	}
	entry.Outcome = Imported
	return entry
}

// boardID derives a replacement board identifier.
func boardID(legacyID string) string { return DerivedID("board", legacyID) }

// wasLegacySchema reports whether the raw document declared the v1 shape, read
// before decoding because decoding is what erases the distinction.
func wasLegacySchema(raw []byte) bool {
	var probe struct {
		SchemaVersion uint64 `json:"schemaVersion"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.SchemaVersion == atlas.LegacyBoardSchemaVersion
}

// presence turns a store load into "does this exist", keeping a real storage
// failure distinguishable from an absence.
func presence(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if codeOf(err) == "NOT_FOUND" {
		return false, nil
	}
	return false, err
}

// failOrSkip separates "this data cannot be imported" from "the import did not
// work this time". The first is a decision; the second is retried on the next
// pass, so recording them the same way would either lose data or loop.
func failOrSkip(entry Entry, err error, what string) Entry {
	switch codeOf(err) {
	case "POLICY_VIOLATION", "UNSUPPORTED_SCHEMA_VERSION", "CORRUPT_DOCUMENT",
		"INVALID_BOARD_ID", "INVALID_RUN_ID", "INVALID_PROJECT_ID", "INVALID_STATE":
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf("the destination refused the %s: %v", what, err)
	default:
		entry.Outcome = Failed
		entry.Reason = fmt.Sprintf("the %s could not be written: %v", what, err)
	}
	return entry
}

// codeOf reads the stable machine code from a DaGama or Atlas error.
func codeOf(err error) string {
	var dagamaErr *dagama.Error
	if errors.As(err, &dagamaErr) {
		return dagamaErr.Code
	}
	var atlasErr *atlas.Error
	if errors.As(err, &atlasErr) {
		return atlasErr.Code
	}
	return ""
}
