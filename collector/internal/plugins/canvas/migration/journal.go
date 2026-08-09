package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// The migration journal.
//
// A migration is the one operation in Canvas that runs over data the operator
// already has and cannot be asked to recreate. That makes two properties
// non-negotiable, and both come from the same place — an append-only log
// written before the effect it authorizes:
//
//	Resumable. An import killed halfway has its completed items on disk. The
//	next run reads them and does not repeat them.
//
//	Traceable. Every item is either copied or skipped, and both outcomes are
//	recorded with a reason. "It didn't appear" is never an acceptable answer
//	about someone's data.
//
// The journal is deliberately the same machinery a run log uses: append,
// fsync, replay. A migration that invented its own durability story would be
// the least-tested durable writer in the codebase.

const (
	// JournalSchemaVersion is the journal envelope version this build writes.
	JournalSchemaVersion uint64 = 1

	journalName = "journal.jsonl"

	maxJournalEntryBytes int64 = 64 << 10
	maxJournalEntries          = 1 << 16
)

// Outcome is what happened to one source item.
type Outcome string

const (
	// Imported: the item was written to its destination by this migration.
	Imported Outcome = "imported"
	// AlreadyPresent: the destination already held it, so nothing was written.
	// This is the outcome that makes a rerun a no-op rather than a duplicate.
	AlreadyPresent Outcome = "already_present"
	// Skipped: deliberately not imported. `Reason` says why, always.
	Skipped Outcome = "skipped"
	// Failed: the import was attempted and did not complete. A later run may
	// retry it; the destination is untouched.
	Failed Outcome = "failed"
)

// Kind is the sort of thing an entry describes.
type Kind string

const (
	KindWorkspace  Kind = "workspace"
	KindPreference Kind = "preference"
	KindDraft      Kind = "draft"
	KindBoard      Kind = "board"
	KindRun        Kind = "run"
	KindArtifact   Kind = "artifact"
)

// Entry is one journaled decision.
//
// SourceID and DestinationID are both recorded even when they are equal,
// because the pair IS the trace: an operator asking "where did my board go"
// must be able to answer it from this file alone.
type Entry struct {
	SchemaVersion uint64 `json:"schemaVersion"`
	// Product is "session", "dagama", "atlas", or "browser".
	Product string `json:"product"`
	Kind    Kind   `json:"kind"`
	// SourceID identifies the item in the legacy data: a key, a board id, a
	// run id.
	SourceID string `json:"sourceId"`
	// DestinationID identifies it in coSlash. Empty when nothing was written.
	DestinationID string `json:"destinationId,omitempty"`
	// SourceSHA256 is over the source bytes exactly as they were read. It is
	// what makes a rerun able to tell "already imported" from "changed since".
	SourceSHA256 string  `json:"sourceSha256,omitempty"`
	SourceBytes  int64   `json:"sourceBytes,omitempty"`
	Outcome      Outcome `json:"outcome"`
	// Reason is required for Skipped and Failed. A skip without a reason is
	// indistinguishable from data loss.
	Reason string `json:"reason,omitempty"`
	// Warnings are non-fatal observations worth surfacing: a remapped id, a
	// repaired schema, a field that did not survive.
	Warnings []string `json:"warnings,omitempty"`
}

// Journal is the append-only record of one migration, across all its runs.
type Journal struct {
	log *runfs.EventLog
	now func() time.Time
}

// OpenJournal binds a journal to a scope. The scope is the migration's own
// directory, never a product's data root: a journal that could write into a
// board store would be one bug away from damaging what it is copying.
func OpenJournal(scope *runfs.Scope, now func() time.Time) (*Journal, error) {
	if scope == nil {
		return nil, errors.New("migration: a journal requires a scope")
	}
	if now == nil {
		now = time.Now
	}
	log, err := runfs.NewEventLog(scope, journalName, runfs.EventLogOptions{
		MaxEventBytes: maxJournalEntryBytes,
		MaxEvents:     maxJournalEntries,
		Now:           now,
	})
	if err != nil {
		return nil, fmt.Errorf("migration: the journal could not be opened: %w", err)
	}
	return &Journal{log: log, now: now}, nil
}

// Record appends one decision and returns it as written.
//
// A skip or a failure without a reason is refused here rather than accepted and
// discovered later, when the only remaining evidence is an item that is missing
// with no explanation.
func (j *Journal) Record(ctx context.Context, entry Entry) (Entry, error) {
	if entry.Product == "" || entry.Kind == "" || entry.SourceID == "" {
		return Entry{}, errors.New("migration: a journal entry needs a product, a kind, and a source id")
	}
	switch entry.Outcome {
	case Imported, AlreadyPresent:
	case Skipped, Failed:
		if entry.Reason == "" {
			return Entry{}, fmt.Errorf("migration: a %s entry requires a reason", entry.Outcome)
		}
	default:
		return Entry{}, fmt.Errorf("migration: unknown outcome %q", entry.Outcome)
	}
	entry.SchemaVersion = JournalSchemaVersion

	if _, err := j.log.Append(ctx, string(entry.Kind), entry); err != nil {
		return Entry{}, fmt.Errorf("migration: the journal entry could not be appended: %w", err)
	}
	return entry, nil
}

// Ledger is the journal replayed into the question the importer actually asks:
// "have I already done this one, and did the source change since?"
type Ledger struct {
	entries map[string]Entry
	all     []Entry
}

// Read replays the journal.
//
// A torn final line is recovered rather than fatal: a migration killed mid-write
// must be resumable, and the entry that was being written did not complete, so
// dropping it is exactly right. Corruption anywhere earlier is fatal, because a
// journal that cannot be trusted cannot answer the only question it exists for.
func (j *Journal) Read(ctx context.Context) (*Ledger, error) {
	result, err := j.log.Recover(ctx)
	if err != nil {
		return nil, fmt.Errorf("migration: the journal is unreadable: %w", err)
	}
	ledger := &Ledger{entries: make(map[string]Entry, len(result.Events))}
	for _, event := range result.Events {
		var entry Entry
		if err := json.Unmarshal(event.Data, &entry); err != nil {
			return nil, fmt.Errorf("migration: journal entry %d is corrupt: %w", event.Seq, err)
		}
		if entry.SchemaVersion > JournalSchemaVersion {
			// A journal written by a newer build may describe outcomes this one
			// cannot interpret. Resuming against it would re-import items that
			// build already placed.
			return nil, fmt.Errorf(
				"migration: journal entry %d declares schema %d, which this build cannot read",
				event.Seq, entry.SchemaVersion)
		}
		ledger.all = append(ledger.all, entry)
		// A later entry supersedes an earlier one for the same item, so a retry
		// after a failure reports the retry's outcome.
		ledger.entries[ledgerKey(entry.Product, entry.Kind, entry.SourceID)] = entry
	}
	return ledger, nil
}

// Entries returns every journaled decision in order, for reporting.
func (l *Ledger) Entries() []Entry { return l.all }

// Lookup returns the last recorded outcome for one source item.
func (l *Ledger) Lookup(product string, kind Kind, sourceID string) (Entry, bool) {
	entry, ok := l.entries[ledgerKey(product, kind, sourceID)]
	return entry, ok
}

// Settled reports whether an item needs no further work.
//
// An item whose source bytes changed since it was imported is NOT settled: the
// operator edited it in the legacy app after the first pass, and reporting it as
// done would silently drop that edit. An item with no recorded checksum — one
// skipped for a reason that has nothing to do with its contents — stays settled,
// because re-examining it would produce the same skip.
func (l *Ledger) Settled(product string, kind Kind, sourceID, sourceSHA256 string) bool {
	entry, ok := l.Lookup(product, kind, sourceID)
	if !ok {
		return false
	}
	switch entry.Outcome {
	case Imported, AlreadyPresent, Skipped:
	default:
		// A failure is retried on the next pass; that is the point of resuming.
		return false
	}
	if entry.SourceSHA256 == "" || sourceSHA256 == "" {
		return true
	}
	return entry.SourceSHA256 == sourceSHA256
}

// Counts summarizes a ledger by outcome, for the operator-facing report.
func (l *Ledger) Counts() map[Outcome]int {
	counts := make(map[Outcome]int, 4)
	for _, entry := range l.entries {
		counts[entry.Outcome]++
	}
	return counts
}

func ledgerKey(product string, kind Kind, sourceID string) string {
	return product + "\x00" + string(kind) + "\x00" + sourceID
}

// Checksum is the source fingerprint recorded in the journal. It is taken over
// the bytes as read, before any decoding, so a value that fails to parse still
// has a stable identity across runs.
func Checksum(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}
