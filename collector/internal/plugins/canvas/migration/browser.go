package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/persistence"
)

// Importing legacy browser state.
//
// The bundle is produced by frontend/src/plugins/canvas/migration/export.ts,
// which is the only code that can read `localStorage`. This file consumes it.
//
// The bundle is treated as a DESCRIPTION of what was found, never as an
// instruction. It arrives from a page, so every field is re-checked here: an
// unknown schema is refused whole, a key outside this build's own allowlist is
// refused individually, and nothing in the bundle can name a destination.
//
// The hard part is not copying — it is identity. Legacy Canvas keyed a
// workspace by a BARE session id, and coSlash keys it by {agent, id}, because a
// bare id is not unique across agents. That is not a theoretical concern: Task
// 00's reference fixtures deliberately give a Claude session and a Codex
// session the same bare id. When a legacy id resolves to more than one coSlash
// session there is no correct answer, so the item is skipped by name rather
// than assigned to whichever candidate sorted first.

// SourceName is the only bundle source this build accepts.
const SourceName = "fleetlog-canvas"

// BundleSchemaVersion is the export envelope version this build can read.
const BundleSchemaVersion uint64 = 1

// bundle mirrors LegacyExportBundle in export.ts. Field names must match.
type bundle struct {
	SchemaVersion uint64         `json:"schemaVersion"`
	Source        string         `json:"source"`
	ExportedAt    string         `json:"exportedAt"`
	Records       []bundleRecord `json:"records"`
	Refused       []skippedKey   `json:"refused"`
	Unrecognized  []string       `json:"unrecognized"`
	Truncated     bool           `json:"truncated"`
}

type bundleRecord struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Suffix  string `json:"suffix"`
	Purpose string `json:"purpose"`
	Value   string `json:"value"`
	Bytes   int64  `json:"bytes"`
}

type skippedKey struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// SessionResolver maps a legacy bare session id to the coSlash sessions that
// could be it. Returning more than one is not an error — it is the ambiguity
// this migration exists to refuse to resolve silently.
type SessionResolver func(ctx context.Context, bareID string) ([]contracts.SessionIdentity, error)

// BrowserImport is one import pass over a bundle.
type BrowserImport struct {
	// Journal records every decision. Required: an import that cannot be
	// traced is not one this package will perform.
	Journal *Journal
	// Workspaces receives Session Canvas state. Required.
	Workspaces *persistence.Store
	// Resolve turns a legacy bare session id into coSlash identities.
	// Required — without it every workspace would be unidentifiable.
	Resolve SessionResolver
}

// BrowserSeed is a legacy preference with no server-backed destination.
//
// coSlash keeps "last project opened" and its siblings in the browser, exactly
// as the legacy app did, so there is nothing on the server to write them to.
// They are returned rather than dropped: the frontend re-applies them, and the
// journal records that this is what happened to them.
type BrowserSeed struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// BrowserResult reports one pass.
type BrowserResult struct {
	Entries []Entry       `json:"entries"`
	Seeds   []BrowserSeed `json:"seeds"`
	// RefusedAtSource are the keys the exporter itself declined, carried
	// through so one report covers both halves of the migration.
	RefusedAtSource []skippedKey `json:"refusedAtSource"`
	Unrecognized    []string     `json:"unrecognized"`
	Truncated       bool         `json:"truncated"`
}

// Counts summarizes the pass by outcome.
func (r *BrowserResult) Counts() map[Outcome]int {
	counts := make(map[Outcome]int, 4)
	for _, entry := range r.Entries {
		counts[entry.Outcome]++
	}
	return counts
}

const browserProduct = "browser"

// ImportBrowserState imports one export bundle.
//
// Rerunning is safe and is the expected case: an item already recorded in the
// journal with an unchanged checksum is reported as already present and not
// written again.
func ImportBrowserState(ctx context.Context, raw []byte, importer BrowserImport) (*BrowserResult, error) {
	if importer.Journal == nil || importer.Workspaces == nil || importer.Resolve == nil {
		return nil, errors.New("migration: a browser import needs a journal, a workspace store, and a resolver")
	}

	var decoded bundle
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("migration: the export bundle is not readable: %w", err)
	}
	// Refused whole rather than per record: a bundle whose envelope this build
	// does not understand may describe records whose meaning has changed.
	if decoded.Source != SourceName {
		return nil, fmt.Errorf("migration: the bundle names source %q, not %q", decoded.Source, SourceName)
	}
	if decoded.SchemaVersion != BundleSchemaVersion {
		return nil, fmt.Errorf(
			"migration: the bundle declares schema %d, and this build reads %d",
			decoded.SchemaVersion, BundleSchemaVersion)
	}

	ledger, err := importer.Journal.Read(ctx)
	if err != nil {
		return nil, err
	}

	result := &BrowserResult{
		RefusedAtSource: decoded.Refused,
		Unrecognized:    decoded.Unrecognized,
		Truncated:       decoded.Truncated,
	}
	// A truncated export is a partial view of the operator's data. It is
	// imported as far as it goes, and the fact is carried into the report so
	// nobody reads the result as complete.
	for _, record := range decoded.Records {
		entry, seed := importer.importRecord(ctx, record, ledger)
		recorded, err := importer.Journal.Record(ctx, entry)
		if err != nil {
			return nil, err
		}
		result.Entries = append(result.Entries, recorded)
		if seed != nil {
			result.Seeds = append(result.Seeds, *seed)
		}
	}
	return result, nil
}

func (importer BrowserImport) importRecord(
	ctx context.Context,
	record bundleRecord,
	ledger *Ledger,
) (Entry, *BrowserSeed) {
	checksum := Checksum([]byte(record.Value))
	entry := Entry{
		Product:      browserProduct,
		SourceID:     record.Key,
		SourceSHA256: checksum,
		SourceBytes:  record.Bytes,
	}

	switch record.Kind {
	case "workspace":
		entry.Kind = KindWorkspace
	case "preference":
		entry.Kind = KindPreference
	case "draft":
		entry.Kind = KindDraft
	default:
		// The bundle does not get to name a kind this build has never heard of;
		// that is how an unrecognized record would acquire a destination.
		entry.Kind = KindPreference
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf("the bundle declares kind %q, which this build does not import", record.Kind)
		return entry, nil
	}

	if ledger.Settled(browserProduct, entry.Kind, record.Key, checksum) {
		previous, _ := ledger.Lookup(browserProduct, entry.Kind, record.Key)
		entry.Outcome = AlreadyPresent
		entry.DestinationID = previous.DestinationID
		return entry, seedFor(entry.Kind, record)
	}

	switch entry.Kind {
	case KindWorkspace:
		return importer.importWorkspace(ctx, record, entry), nil
	case KindPreference:
		// coSlash keeps these in the browser, exactly as the legacy app did, so
		// there is no server document to write. Returned as a seed instead.
		entry.Outcome = Skipped
		entry.Reason = "coSlash keeps this preference in the browser, so it is returned as a seed rather than stored on the server"
		return entry, seedFor(entry.Kind, record)
	default:
		// An unsaved workflow's destination is a saved board, which is the board
		// pass. Saying so is better than reporting a skip with no route.
		entry.Outcome = Skipped
		entry.Reason = "an unsaved workflow is imported by the board pass, which this build does not yet perform"
		return entry, nil
	}
}

func seedFor(kind Kind, record bundleRecord) *BrowserSeed {
	if kind != KindPreference {
		return nil
	}
	return &BrowserSeed{Key: record.Key, Value: record.Value}
}

func (importer BrowserImport) importWorkspace(
	ctx context.Context,
	record bundleRecord,
	entry Entry,
) Entry {
	bareID := strings.TrimSpace(record.Suffix)
	if bareID == "" {
		entry.Outcome = Skipped
		entry.Reason = "the legacy key carries no session identifier"
		return entry
	}

	candidates, err := importer.Resolve(ctx, bareID)
	if err != nil {
		entry.Outcome = Failed
		entry.Reason = fmt.Sprintf("the session could not be resolved: %v", err)
		return entry
	}
	switch len(candidates) {
	case 0:
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf(
			"no coSlash session matches legacy id %s, so this workspace has nothing to attach to", bareID)
		return entry
	case 1:
	default:
		// Legacy keyed by a bare id; coSlash keys by {agent, id}. There is no
		// correct answer here, and picking one would silently attach one
		// agent's layout to another agent's session.
		agents := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			agents = append(agents, candidate.Agent)
		}
		sort.Strings(agents)
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf(
			"legacy id %s matches %d coSlash sessions (%s); a bare id is not unique across agents, so this workspace is left for the operator to place",
			bareID, len(candidates), strings.Join(agents, ", "))
		return entry
	}

	session := candidates[0]
	entry.DestinationID = session.Agent + "/" + session.ID

	// Legacy stored this as a string. It must be JSON the destination can hold;
	// a value that does not parse is reported rather than wrapped, because
	// wrapping would produce a workspace the product cannot read.
	if !json.Valid([]byte(record.Value)) {
		entry.Outcome = Skipped
		entry.Reason = "the stored workspace is not valid JSON, so it was left in the browser rather than written in a shape nothing can read"
		return entry
	}

	current, err := importer.Workspaces.Load(ctx, session)
	if err != nil {
		entry.Outcome = Failed
		entry.Reason = fmt.Sprintf("the destination workspace could not be read: %v", err)
		return entry
	}
	// The destination already holds work this migration did not put there.
	// Overwriting it would destroy whatever the operator has done in coSlash
	// since, which is exactly the damage a migration must not do.
	if current.Revision != 0 {
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf(
			"session %s/%s already has a coSlash workspace at revision %d, which this migration will not overwrite",
			session.Agent, session.ID, current.Revision)
		return entry
	}

	if _, err := importer.Workspaces.Save(ctx, session, contracts.WorkspaceWrite{
		SchemaVersion:    persistence.SchemaVersion,
		ExpectedRevision: current.Revision,
		State:            json.RawMessage(record.Value),
	}); err != nil {
		entry.Outcome = Failed
		entry.Reason = fmt.Sprintf("the workspace could not be written: %v", err)
		return entry
	}

	entry.Outcome = Imported
	return entry
}
