package migration

import (
	"os"
	"path/filepath"
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
	moment := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	return func() time.Time { return moment }
}

func newJournal(t *testing.T) (*Journal, string) {
	t.Helper()
	scope, root := newScope(t)
	journal, err := OpenJournal(scope, fixedNow())
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	return journal, root
}

func mustRecord(t *testing.T, journal *Journal, entry Entry) Entry {
	t.Helper()
	recorded, err := journal.Record(t.Context(), entry)
	if err != nil {
		t.Fatalf("Record(%s/%s): %v", entry.Product, entry.SourceID, err)
	}
	return recorded
}

func TestTheJournalSurvivesReopening(t *testing.T) {
	// Resumability is the whole point: a migration killed after this write must
	// find the record on the next run, in a fresh process.
	scope, root := newScope(t)
	journal, err := OpenJournal(scope, fixedNow())
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	mustRecord(t, journal, Entry{
		Product: "dagama", Kind: KindBoard, SourceID: "board-1",
		DestinationID: "board-1", SourceSHA256: "abc", Outcome: Imported,
	})

	reopenedScope, err := runfs.OpenScope(root, runfs.ScopeOptions{})
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}
	defer reopenedScope.Close()
	reopened, err := OpenJournal(reopenedScope, fixedNow())
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	ledger, err := reopened.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ledger.Settled("dagama", KindBoard, "board-1", "abc") {
		t.Fatal("a recorded import was not settled after reopening")
	}
}

func TestAnUnchangedSourceIsSettledAndAChangedOneIsNot(t *testing.T) {
	journal, _ := newJournal(t)
	mustRecord(t, journal, Entry{
		Product: "atlas", Kind: KindBoard, SourceID: "board-1",
		DestinationID: "board-1", SourceSHA256: "sum-one", Outcome: Imported,
	})
	ledger, err := journal.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if !ledger.Settled("atlas", KindBoard, "board-1", "sum-one") {
		t.Fatal("an unchanged source was not settled")
	}
	// The operator edited it in the legacy app after the first pass. Reporting
	// it as done would silently drop that edit.
	if ledger.Settled("atlas", KindBoard, "board-1", "sum-two") {
		t.Fatal("a changed source was reported as settled")
	}
	if ledger.Settled("atlas", KindBoard, "board-2", "sum-one") {
		t.Fatal("an item that was never journaled was reported as settled")
	}
}

func TestAFailedItemIsRetriedAndItsRetryWins(t *testing.T) {
	journal, _ := newJournal(t)
	mustRecord(t, journal, Entry{
		Product: "dagama", Kind: KindRun, SourceID: "run-1",
		SourceSHA256: "sum", Outcome: Failed, Reason: "the destination store was unavailable",
	})
	ledger, err := journal.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Resuming means retrying what did not finish.
	if ledger.Settled("dagama", KindRun, "run-1", "sum") {
		t.Fatal("a failed item was treated as settled")
	}

	mustRecord(t, journal, Entry{
		Product: "dagama", Kind: KindRun, SourceID: "run-1",
		DestinationID: "run-1", SourceSHA256: "sum", Outcome: Imported,
	})
	ledger, err = journal.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ledger.Settled("dagama", KindRun, "run-1", "sum") {
		t.Fatal("the successful retry did not supersede the failure")
	}
	entry, _ := ledger.Lookup("dagama", KindRun, "run-1")
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q, want imported", entry.Outcome)
	}
	// Both attempts stay on the log; only the current answer changes.
	if len(ledger.Entries()) != 2 {
		t.Fatalf("entries = %d, want 2", len(ledger.Entries()))
	}
}

func TestASkipMustCarryAReason(t *testing.T) {
	// An item that is missing with no explanation is indistinguishable from
	// data loss, so the journal refuses to record one.
	journal, _ := newJournal(t)
	for _, outcome := range []Outcome{Skipped, Failed} {
		_, err := journal.Record(t.Context(), Entry{
			Product: "browser", Kind: KindPreference, SourceID: "k", Outcome: outcome,
		})
		if err == nil {
			t.Fatalf("a %s entry with no reason was accepted", outcome)
		}
	}
}

func TestAnIncompleteOrUnknownEntryIsRefused(t *testing.T) {
	journal, _ := newJournal(t)
	cases := []Entry{
		{Kind: KindBoard, SourceID: "b", Outcome: Imported},
		{Product: "atlas", SourceID: "b", Outcome: Imported},
		{Product: "atlas", Kind: KindBoard, Outcome: Imported},
		{Product: "atlas", Kind: KindBoard, SourceID: "b", Outcome: Outcome("moved")},
	}
	for index, entry := range cases {
		if _, err := journal.Record(t.Context(), entry); err == nil {
			t.Fatalf("case %d was accepted", index)
		}
	}
}

func TestATornFinalEntryIsRecoveredRatherThanFatal(t *testing.T) {
	// A migration killed mid-write must be resumable. The entry being written
	// did not complete, so dropping it is exactly right.
	journal, root := newJournal(t)
	mustRecord(t, journal, Entry{
		Product: "atlas", Kind: KindBoard, SourceID: "board-1",
		DestinationID: "board-1", SourceSHA256: "sum", Outcome: Imported,
	})

	path := filepath.Join(root, journalName)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(path, append(contents, []byte(`{"seq":2,"type":"board","data":{"prod`)...), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ledger, err := journal.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(ledger.Entries()) != 1 {
		t.Fatalf("entries = %d, want 1", len(ledger.Entries()))
	}
	if !ledger.Settled("atlas", KindBoard, "board-1", "sum") {
		t.Fatal("the complete entry was lost with the torn one")
	}
}

func TestCorruptionBeforeTheEndIsFatal(t *testing.T) {
	// A journal that cannot be trusted cannot answer the only question it
	// exists for, so a rerun must stop rather than re-import everything.
	journal, root := newJournal(t)
	mustRecord(t, journal, Entry{
		Product: "atlas", Kind: KindBoard, SourceID: "board-1", Outcome: Imported,
	})
	mustRecord(t, journal, Entry{
		Product: "atlas", Kind: KindBoard, SourceID: "board-2", Outcome: Imported,
	})

	path := filepath.Join(root, journalName)
	if err := os.WriteFile(path, []byte("not json\n{\"seq\":2}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := journal.Read(t.Context()); err == nil {
		t.Fatal("a corrupt journal was read as valid")
	}
}

func TestAJournalFromANewerBuildIsRefused(t *testing.T) {
	// Resuming against outcomes this build cannot interpret would re-import
	// items the newer build already placed.
	journal, root := newJournal(t)
	mustRecord(t, journal, Entry{
		Product: "atlas", Kind: KindBoard, SourceID: "board-1", Outcome: Imported,
	})
	path := filepath.Join(root, journalName)
	line := `{"seq":2,"at":"2026-08-09T18:00:00Z","type":"board","data":{"schemaVersion":99,"product":"atlas","kind":"board","sourceId":"board-2","outcome":"imported"}}` + "\n"
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(path, append(contents, []byte(line)...), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := journal.Read(t.Context()); err == nil {
		t.Fatal("a journal from a newer build was accepted")
	}
}

func TestAnItemSkippedForANonContentReasonStaysSettled(t *testing.T) {
	// A DaGama run withheld because of a model gap has no meaningful checksum
	// relationship; re-examining it would produce the same skip every pass.
	journal, _ := newJournal(t)
	mustRecord(t, journal, Entry{
		Product: "browser", Kind: KindPreference, SourceID: "fleetlog.llmConfig",
		Outcome: Skipped, Reason: "credentials are never migrated",
	})
	ledger, err := journal.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ledger.Settled("browser", KindPreference, "fleetlog.llmConfig", "any-sum") {
		t.Fatal("a reason-based skip was not settled")
	}
}

func TestCountsSummarizeTheCurrentAnswerPerItem(t *testing.T) {
	journal, _ := newJournal(t)
	mustRecord(t, journal, Entry{Product: "atlas", Kind: KindBoard, SourceID: "b1", Outcome: Imported})
	mustRecord(t, journal, Entry{
		Product: "atlas", Kind: KindRun, SourceID: "r1",
		Outcome: Failed, Reason: "storage was unavailable",
	})
	mustRecord(t, journal, Entry{Product: "atlas", Kind: KindRun, SourceID: "r1", Outcome: Imported})
	mustRecord(t, journal, Entry{
		Product: "dagama", Kind: KindRun, SourceID: "r2",
		Outcome: Skipped, Reason: "nonterminal legacy run",
	})

	ledger, err := journal.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	counts := ledger.Counts()
	// r1 is counted once, as imported: the retry replaced the failure.
	if counts[Imported] != 2 || counts[Skipped] != 1 || counts[Failed] != 0 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestChecksumIsOverTheBytesAsRead(t *testing.T) {
	// Taken before decoding, so a value that fails to parse still has a stable
	// identity across runs.
	broken := []byte(`{"version":1,"layout":`)
	if Checksum(broken) != Checksum(broken) {
		t.Fatal("the checksum is not stable")
	}
	if Checksum(broken) == Checksum([]byte(`{"version":1,"layout":{}}`)) {
		t.Fatal("different bytes produced the same checksum")
	}
	if len(Checksum(nil)) != 64 {
		t.Fatal("the checksum is not a sha256 hex digest")
	}
}
