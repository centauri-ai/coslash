package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/atlas"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/dagama"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Importing legacy runs.
//
// A run is an event log; the log IS the run, and the materialized view is
// derivable from it. So importing a run means importing its log verbatim rather
// than replaying a summary into a fresh one. A summary would keep the outcome
// and lose the evidence — which attempt failed, which gate was opened, who took
// control — and that evidence is the entire reason a finished run is worth
// keeping.
//
// Verbatim does not mean unchecked. The log is reduced BEFORE anything is
// written: a log that cannot produce a coherent state is refused whole, and the
// destination never sees it. Reduce is the same function the run store uses, so
// "the importer accepted it" and "the product can read it" are the same claim.
//
// A run that was still in flight is closed as interrupted_migration. The
// process it described ended when the legacy app stopped; it has no live
// terminal, no tmux session, and no attempt to reconcile. Leaving it open would
// invite every control to offer to advance something that cannot advance.

// LegacyRun is one run found in the legacy data.
type LegacyRun struct {
	// ID is the legacy run identifier.
	ID string
	// Events is the raw JSONL log, exactly as it was read.
	Events []byte
}

// RunImport imports runs for one product into one project.
type RunImport struct {
	Journal   *Journal
	ProjectID string
	// Scope is rooted at the destination runs root. The importer writes the
	// event log directly because the log is what it is copying; the store then
	// materializes the view from it.
	Scope *runfs.Scope
	// Exactly one of these is set; it verifies the log and reads it back.
	DaGama *dagama.RunStore
	Atlas  *atlas.RunStore
}

// runEventsName mirrors the durable layout both run stores use.
//
// This is a deliberate coupling: the importer writes the log the store will
// read, so it has to agree on where the log lives. TestAnImportedRunIsReadable
// asserts the agreement, so a layout change fails here rather than silently
// producing runs nothing can open.
const runEventsName = "events.jsonl"

func runEventsPath(projectID, runID string) string {
	return path.Join(projectID, "runs", runID, runEventsName)
}

// ImportRuns imports a set of legacy runs, skipping what is already done.
func ImportRuns(ctx context.Context, runs []LegacyRun, importer RunImport) ([]Entry, error) {
	if importer.Journal == nil || importer.Scope == nil {
		return nil, errors.New("migration: a run import needs a journal and a scope")
	}
	if (importer.DaGama == nil) == (importer.Atlas == nil) {
		return nil, errors.New("migration: a run import needs exactly one destination store")
	}
	if importer.ProjectID == "" {
		return nil, errors.New("migration: a run import needs a project")
	}

	ledger, err := importer.Journal.Read(ctx)
	if err != nil {
		return nil, err
	}

	product := "atlas"
	valid := atlas.ValidRunID
	if importer.DaGama != nil {
		product = "dagama"
		valid = dagama.ValidRunID
	}

	entries := make([]Entry, 0, len(runs))
	for _, run := range runs {
		entry := Entry{
			Product:      product,
			Kind:         KindRun,
			SourceID:     run.ID,
			SourceSHA256: Checksum(run.Events),
			SourceBytes:  int64(len(run.Events)),
		}
		switch {
		case run.ID == "":
			entry.SourceID = "(unidentified)"
			entry.Outcome = Skipped
			entry.Reason = "the legacy run has no identifier, so it cannot be placed or recognized on a rerun"
		case ledger.Settled(product, KindRun, run.ID, entry.SourceSHA256):
			previous, _ := ledger.Lookup(product, KindRun, run.ID)
			entry.Outcome = AlreadyPresent
			entry.DestinationID = previous.DestinationID
		default:
			entry = importer.importOne(ctx, run, valid, entry)
		}

		recorded, err := importer.Journal.Record(ctx, entry)
		if err != nil {
			return nil, err
		}
		entries = append(entries, recorded)
	}
	return entries, nil
}

func (importer RunImport) importOne(
	ctx context.Context,
	run LegacyRun,
	valid func(string) bool,
	entry Entry,
) Entry {
	events, err := parseEventLog(run.Events)
	if err != nil {
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf("the legacy run log is unreadable: %v", err)
		return entry
	}
	if len(events) == 0 {
		entry.Outcome = Skipped
		entry.Reason = "the legacy run log is empty, so there is no run to import"
		return entry
	}
	converted, err := EncodeEventLog(events)
	if err != nil {
		entry.Outcome = Failed
		entry.Reason = fmt.Sprintf("the converted run log could not be encoded: %v", err)
		return entry
	}

	destination, placement, err := importer.place(ctx, run.ID, valid, converted)
	if err != nil {
		entry.Outcome = Failed
		entry.Reason = err.Error()
		return entry
	}
	entry.DestinationID = destination
	switch placement.outcome {
	case AlreadyPresent:
		// This run is already here with the same history, which is what a rerun
		// looks like when the journal was lost.
		entry.Outcome = AlreadyPresent
		return entry
	case Skipped:
		entry.Outcome = Skipped
		entry.Reason = placement.reason
		return entry
	}
	if placement.reason != "" {
		entry.Warnings = append(entry.Warnings, placement.reason)
	}

	// Reduced before anything is written: "the importer accepted it" and "the
	// product can read it" have to be the same claim.
	live, reduceErr := importer.reduce(destination, events)
	if reduceErr != nil {
		entry.Outcome = Skipped
		entry.Reason = fmt.Sprintf("the legacy run log does not reduce to a coherent state: %v", reduceErr)
		return entry
	}

	if err := importer.Scope.AtomicWrite(ctx, runEventsPath(importer.ProjectID, destination), converted); err != nil {
		entry.Outcome = Failed
		entry.Reason = fmt.Sprintf("the run log could not be written: %v", err)
		return entry
	}

	if live {
		closed, err := importer.close(ctx, destination)
		if err != nil {
			// The log is on disk and readable; only the closing event is
			// missing. Failed rather than skipped, so the next pass finishes it.
			entry.Outcome = Failed
			entry.Reason = fmt.Sprintf("the imported run could not be closed as interrupted: %v", err)
			return entry
		}
		entry.Warnings = append(entry.Warnings, fmt.Sprintf(
			"the legacy run was still %s, so it was closed as interrupted_migration and can never resume", closed))
	}

	entry.Outcome = Imported
	return entry
}

// placement is where a run will be written and why.
type placement struct {
	// outcome is empty when the run should be written, AlreadyPresent when this
	// exact history is already there, and Skipped when it cannot be placed.
	outcome Outcome
	reason  string
}

// place decides which identifier a run is stored under.
//
// A run differs from a board here, and the difference matters. A run's identity
// IS its history: an immutable log. So when the destination id is occupied,
// there are two possibilities, and conflating them is how a migration produces
// duplicates. Either the run already there has this same history — our own
// earlier import, which a lost journal would otherwise repeat under a new id —
// or it is a genuinely different run that happens to share an id, which is the
// case that earns a remap.
//
// The stored log is compared by PREFIX rather than by equality, because a run
// imported while still in flight had a closing event appended after the copy.
func (importer RunImport) place(
	ctx context.Context,
	legacyID string,
	valid func(string) bool,
	converted []byte,
) (string, placement, error) {
	candidates := []string{legacyID}
	remapReason := ""
	if !valid(legacyID) {
		derived := DerivedRunID(legacyID)
		if !valid(derived) {
			return "", placement{}, fmt.Errorf(
				"migration: no valid run identifier could be derived from %q", legacyID)
		}
		candidates = []string{derived}
		remapReason = fmt.Sprintf(
			"legacy id %q is not a valid coSlash run identifier, so it was stored as %q", legacyID, derived)
	} else {
		candidates = append(candidates, DerivedRunID(legacyID))
	}

	for index, candidate := range candidates {
		occupied, err := importer.exists(ctx, candidate)
		if err != nil {
			return "", placement{}, fmt.Errorf("migration: the destination run could not be checked: %w", err)
		}
		if !occupied {
			reason := remapReason
			if index > 0 {
				reason = fmt.Sprintf(
					"a different run already occupies id %q, so the legacy one was stored as %q",
					legacyID, candidate)
			}
			return candidate, placement{reason: reason}, nil
		}

		stored, err := importer.Scope.ReadFile(ctx, runEventsPath(importer.ProjectID, candidate))
		if err != nil {
			// The run exists but its log is unreadable from here. Treating that
			// as "different" would create a duplicate, so it is refused.
			return candidate, placement{
				outcome: Skipped,
				reason: fmt.Sprintf(
					"run %q already exists and its log could not be compared: %v", candidate, err),
			}, nil
		}
		if bytes.HasPrefix(stored, converted) {
			return candidate, placement{outcome: AlreadyPresent}, nil
		}
	}

	last := candidates[len(candidates)-1]
	return last, placement{
		outcome: Skipped,
		reason: fmt.Sprintf(
			"run %q and its derived id %q are both taken by different runs, so this one has nowhere to go",
			legacyID, last),
	}, nil
}

// exists reports whether a run is already present at the destination.
func (importer RunImport) exists(ctx context.Context, runID string) (bool, error) {
	var err error
	if importer.DaGama != nil {
		_, err = importer.DaGama.Read(ctx, importer.ProjectID, runID)
	} else {
		_, err = importer.Atlas.Read(ctx, importer.ProjectID, runID)
	}
	return presence(err)
}

// reduce verifies the log and reports whether it left the run still live.
func (importer RunImport) reduce(runID string, events []runfs.Event) (bool, error) {
	if importer.DaGama != nil {
		state, err := dagama.Reduce(runID, events)
		if err != nil {
			return false, err
		}
		switch state.Status {
		case dagama.RunSucceeded, dagama.RunFailed, dagama.RunCanceled, dagama.RunInterruptedImport:
			return false, nil
		}
		return true, nil
	}
	state, err := atlas.Reduce(runID, events)
	if err != nil {
		return false, err
	}
	return !state.IsTerminal() && state.Status != atlas.RunFailed, nil
}

// close records the terminal event for a run that was still in flight.
func (importer RunImport) close(ctx context.Context, runID string) (string, error) {
	const message = "the legacy run was still in flight when it was imported; it is history and never resumes"
	if importer.DaGama != nil {
		state, err := importer.DaGama.Append(ctx, importer.ProjectID, runID, &dagama.RunFinished{
			Status:  dagama.RunInterruptedImport,
			Reason:  "imported_nonterminal",
			Message: message,
		})
		if err != nil {
			return "", err
		}
		return string(state.Status), nil
	}
	state, err := importer.Atlas.Append(ctx, importer.ProjectID, runID, &atlas.RunFinished{
		Status:  atlas.RunInterruptedImport,
		Reason:  "imported_nonterminal",
		Message: message,
	})
	if err != nil {
		return "", err
	}
	return string(state.Status), nil
}

// envelopeFields are the legacy log's own fields. Everything else on a line is
// payload.
var envelopeFields = map[string]bool{"seq": true, "at": true, "type": true}

// parseEventLog converts a legacy JSONL run log into coSlash events.
//
// The two formats agree on every event type and field name but differ in
// shape: legacy wrote the payload FLAT alongside `seq`, `at`, and `type`, while
// coSlash nests it under `data`. So this is a real conversion, not a copy, and
// it is the one place the difference is handled.
//
// A torn final line is dropped, matching how the run store recovers one: the
// legacy app may have been killed mid-append, and that record was never
// durable. Anything malformed before the end is corruption, and importing a
// corrupt log would attest to a history that never happened.
func parseEventLog(raw []byte) ([]runfs.Event, error) {
	trimmed := bytes.TrimRight(raw, "\n")
	if len(trimmed) == 0 {
		return nil, nil
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	complete := len(lines)
	if !bytes.HasSuffix(raw, []byte("\n")) {
		complete--
	}

	events := make([]runfs.Event, 0, complete)
	for index := 0; index < complete; index++ {
		line := bytes.TrimSpace(lines[index])
		if len(line) == 0 {
			return nil, fmt.Errorf("line %d is empty", index+1)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil {
			return nil, fmt.Errorf("line %d is not a readable event: %w", index+1, err)
		}

		var event runfs.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("line %d has an unreadable envelope: %w", index+1, err)
		}
		if event.Type == "" {
			return nil, fmt.Errorf("line %d has no event type", index+1)
		}
		if event.Seq != uint64(index+1) {
			// Gapless sequence is what makes the log an authority rather than a
			// pile of records. A gap means something was removed.
			return nil, fmt.Errorf("line %d declares seq %d", index+1, event.Seq)
		}

		// A legacy line already carrying `data` is one this collector wrote, so
		// it is taken as-is rather than re-wrapped.
		if nested, ok := fields["data"]; ok && len(fields) <= 4 {
			event.Data = nested
		} else {
			payload := make(map[string]json.RawMessage, len(fields))
			for name, value := range fields {
				if envelopeFields[name] {
					continue
				}
				payload[name] = value
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("line %d could not be converted: %w", index+1, err)
			}
			event.Data = encoded
		}
		events = append(events, event)
	}
	return events, nil
}

// EncodeEventLog renders converted events back as coSlash JSONL, which is what
// the destination store reads.
func EncodeEventLog(events []runfs.Event) ([]byte, error) {
	var buffer bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
	return buffer.Bytes(), nil
}
