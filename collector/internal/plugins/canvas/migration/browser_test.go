package migration

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/persistence"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// The reference fixtures deliberately give a Claude session and a Codex session
// the same bare id, because legacy Canvas keyed by that id alone.
const sharedBareID = "0f9a4d1e-2b3c-4d5e-8f60-112233445566"

var (
	claudeSession = contracts.SessionIdentity{Agent: "claude", ID: sharedBareID}
	codexSession  = contracts.SessionIdentity{Agent: "codex", ID: sharedBareID}
	loneSession   = contracts.SessionIdentity{Agent: "claude", ID: "aaaa1111-bbbb-2222-cccc-333344445555"}
)

func newWorkspaces(t *testing.T) *persistence.Store {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	store, err := persistence.Open(t.Context(), filepath.Join(root, "canvas"), persistence.Options{
		Now: fixedNow(),
	})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// resolveFixed answers with whatever the test declares, so ambiguity is
// reproducible rather than dependent on a live session index.
func resolveFixed(byBareID map[string][]contracts.SessionIdentity) SessionResolver {
	return func(_ context.Context, bareID string) ([]contracts.SessionIdentity, error) {
		return byBareID[bareID], nil
	}
}

func workspaceBundle(records ...bundleRecord) []byte {
	raw, err := json.Marshal(bundle{
		SchemaVersion: BundleSchemaVersion,
		Source:        SourceName,
		ExportedAt:    "2026-08-09T18:30:00Z",
		Records:       records,
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func workspaceRecord(bareID, value string) bundleRecord {
	key := "fleetlog.canvasWorkspace.v1:" + bareID
	return bundleRecord{
		Key: key, Kind: "workspace", Suffix: bareID,
		Purpose: "Session Canvas layout", Value: value, Bytes: int64(len(value)),
	}
}

const validWorkspace = `{"version":1,"layout":{},"checkpoints":[],"pinIds":["a"]}`

func newBrowserImport(t *testing.T, resolve SessionResolver) (BrowserImport, *persistence.Store, string) {
	t.Helper()
	journal, root := newJournal(t)
	workspaces := newWorkspaces(t)
	return BrowserImport{Journal: journal, Workspaces: workspaces, Resolve: resolve}, workspaces, root
}

func onlyEntry(t *testing.T, result *BrowserResult) Entry {
	t.Helper()
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(result.Entries))
	}
	return result.Entries[0]
}

func TestAWorkspaceLandsOnItsCompositeIdentity(t *testing.T) {
	importer, workspaces, _ := newBrowserImport(t, resolveFixed(map[string][]contracts.SessionIdentity{
		loneSession.ID: {loneSession},
	}))

	result, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord(loneSession.ID, validWorkspace)), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Imported {
		t.Fatalf("outcome = %q (%s)", entry.Outcome, entry.Reason)
	}
	if entry.DestinationID != "claude/"+loneSession.ID {
		t.Fatalf("destination = %q", entry.DestinationID)
	}

	stored, err := workspaces.Load(t.Context(), loneSession)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Revision != 1 {
		t.Fatalf("revision = %d, want 1", stored.Revision)
	}
	var state map[string]any
	if err := json.Unmarshal(stored.State, &state); err != nil {
		t.Fatalf("stored state is not readable: %v", err)
	}
	if state["version"] != float64(1) {
		t.Fatalf("state = %v", state)
	}
}

func TestAnAmbiguousLegacyIdIsRefusedRatherThanGuessed(t *testing.T) {
	// Legacy keyed by a bare id; coSlash keys by {agent, id}. Picking a
	// candidate would attach one agent's layout to another agent's session.
	importer, workspaces, _ := newBrowserImport(t, resolveFixed(map[string][]contracts.SessionIdentity{
		sharedBareID: {claudeSession, codexSession},
	}))

	result, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord(sharedBareID, validWorkspace)), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Skipped {
		t.Fatalf("outcome = %q, want skipped", entry.Outcome)
	}
	// The reason has to name both candidates, or the operator cannot act on it.
	if !strings.Contains(entry.Reason, "claude") || !strings.Contains(entry.Reason, "codex") {
		t.Fatalf("reason does not name the candidates: %s", entry.Reason)
	}

	for _, session := range []contracts.SessionIdentity{claudeSession, codexSession} {
		stored, err := workspaces.Load(t.Context(), session)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if stored.Revision != 0 {
			t.Fatalf("%s was written despite the ambiguity", session.Agent)
		}
	}
}

func TestAWorkspaceWithNoMatchingSessionIsSkipped(t *testing.T) {
	importer, _, _ := newBrowserImport(t, resolveFixed(nil))
	result, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord("gone-forever", validWorkspace)), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "no coSlash session") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestRerunningImportsNothingTwice(t *testing.T) {
	// The exit gate: running the migration twice produces no duplicate records.
	importer, workspaces, _ := newBrowserImport(t, resolveFixed(map[string][]contracts.SessionIdentity{
		loneSession.ID: {loneSession},
	}))
	raw := workspaceBundle(workspaceRecord(loneSession.ID, validWorkspace))

	if _, err := ImportBrowserState(t.Context(), raw, importer); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, err := ImportBrowserState(t.Context(), raw, importer)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	entry := onlyEntry(t, second)
	if entry.Outcome != AlreadyPresent {
		t.Fatalf("outcome = %q, want already_present", entry.Outcome)
	}
	if entry.DestinationID != "claude/"+loneSession.ID {
		t.Fatalf("the rerun lost the destination: %q", entry.DestinationID)
	}

	stored, err := workspaces.Load(t.Context(), loneSession)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Revision 2 would mean the rerun wrote again.
	if stored.Revision != 1 {
		t.Fatalf("revision = %d, want 1", stored.Revision)
	}
}

func TestAnExistingDestinationIsNeverOverwritten(t *testing.T) {
	// Whatever the operator has done in coSlash since is theirs. A migration
	// that overwrites it is doing the one damage it must not do.
	importer, workspaces, _ := newBrowserImport(t, resolveFixed(map[string][]contracts.SessionIdentity{
		loneSession.ID: {loneSession},
	}))
	if _, err := workspaces.Save(t.Context(), loneSession, contracts.WorkspaceWrite{
		SchemaVersion:    persistence.SchemaVersion,
		ExpectedRevision: 0,
		State:            json.RawMessage(`{"version":1,"mine":true}`),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	result, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord(loneSession.ID, validWorkspace)), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "will not overwrite") {
		t.Fatalf("entry = %+v", entry)
	}

	stored, err := workspaces.Load(t.Context(), loneSession)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(string(stored.State), "mine") {
		t.Fatalf("the operator's workspace was replaced: %s", stored.State)
	}
}

func TestAMalformedWorkspaceIsReportedRatherThanWrapped(t *testing.T) {
	// Wrapping it would produce a workspace the product cannot read, and the
	// legacy copy is still the operator's only good source.
	importer, workspaces, _ := newBrowserImport(t, resolveFixed(map[string][]contracts.SessionIdentity{
		loneSession.ID: {loneSession},
	}))
	result, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord(loneSession.ID, `{"version":1,"layout":`)), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "not valid JSON") {
		t.Fatalf("entry = %+v", entry)
	}
	stored, _ := workspaces.Load(t.Context(), loneSession)
	if stored.Revision != 0 {
		t.Fatal("a malformed workspace was written")
	}
}

func TestAPreferenceComesBackAsASeed(t *testing.T) {
	// coSlash keeps these in the browser, as the legacy app did. Returning them
	// is the difference between "moved" and "lost".
	importer, _, _ := newBrowserImport(t, resolveFixed(nil))
	result, err := ImportBrowserState(t.Context(), workspaceBundle(bundleRecord{
		Key: "fleetlog.dagamaProject.v1", Kind: "preference",
		Value: "/Users/example/code/demo", Bytes: 24,
	}), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Skipped {
		t.Fatalf("outcome = %q", entry.Outcome)
	}
	if len(result.Seeds) != 1 || result.Seeds[0].Value != "/Users/example/code/demo" {
		t.Fatalf("seeds = %+v", result.Seeds)
	}
}

func TestASeedIsStillReturnedOnARerun(t *testing.T) {
	// The seed is how the preference reaches the browser at all. A rerun that
	// dropped it would leave the operator worse off than the first pass.
	importer, _, _ := newBrowserImport(t, resolveFixed(nil))
	raw := workspaceBundle(bundleRecord{
		Key: "fleetlog.atlasProject.v1", Kind: "preference", Value: "/demo", Bytes: 5,
	})
	if _, err := ImportBrowserState(t.Context(), raw, importer); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, err := ImportBrowserState(t.Context(), raw, importer)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if onlyEntry(t, second).Outcome != AlreadyPresent {
		t.Fatalf("outcome = %q", second.Entries[0].Outcome)
	}
	if len(second.Seeds) != 1 {
		t.Fatalf("the rerun dropped the seed: %+v", second.Seeds)
	}
}

func TestAChangedSourceIsImportedAgain(t *testing.T) {
	// The operator kept using the legacy app between passes. Reporting the item
	// as done would silently drop that work.
	importer, _, _ := newBrowserImport(t, resolveFixed(map[string][]contracts.SessionIdentity{
		loneSession.ID: {loneSession},
	}))
	if _, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord(loneSession.ID, `{"version":1,"pinIds":[]}`)), importer); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	second, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord(loneSession.ID, `{"version":1,"pinIds":["new"]}`)), importer)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	entry := onlyEntry(t, second)
	// It is attempted again. The destination now exists, so it is refused for
	// that reason rather than reported as already done.
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "will not overwrite") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestAnUnknownBundleEnvelopeIsRefusedWhole(t *testing.T) {
	// A bundle whose envelope this build does not understand may describe
	// records whose meaning has changed, so no part of it is imported.
	importer, _, _ := newBrowserImport(t, resolveFixed(nil))

	future, _ := json.Marshal(bundle{SchemaVersion: 99, Source: SourceName})
	if _, err := ImportBrowserState(t.Context(), future, importer); err == nil {
		t.Fatal("a future bundle schema was accepted")
	}
	foreign, _ := json.Marshal(bundle{SchemaVersion: BundleSchemaVersion, Source: "somewhere-else"})
	if _, err := ImportBrowserState(t.Context(), foreign, importer); err == nil {
		t.Fatal("a bundle from another source was accepted")
	}
	if _, err := ImportBrowserState(t.Context(), []byte("not json"), importer); err == nil {
		t.Fatal("an unreadable bundle was accepted")
	}
}

func TestTheBundleCannotNameAKindThisBuildDoesNotImport(t *testing.T) {
	// The bundle arrives from a page. An unrecognized kind must not acquire a
	// destination by being asserted.
	importer, _, _ := newBrowserImport(t, resolveFixed(nil))
	result, err := ImportBrowserState(t.Context(), workspaceBundle(bundleRecord{
		Key: "fleetlog.somethingElse", Kind: "credentials", Value: "x", Bytes: 1,
	}), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "does not import") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestAResolverFailureIsRetryableRatherThanASkip(t *testing.T) {
	// A transient failure must not be recorded as a decision about the data.
	journal, _ := newJournal(t)
	importer := BrowserImport{
		Journal:    journal,
		Workspaces: newWorkspaces(t),
		Resolve: func(context.Context, string) ([]contracts.SessionIdentity, error) {
			return nil, errors.New("the session index is unavailable")
		},
	}
	result, err := ImportBrowserState(t.Context(),
		workspaceBundle(workspaceRecord(loneSession.ID, validWorkspace)), importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	if onlyEntry(t, result).Outcome != Failed {
		t.Fatalf("outcome = %q, want failed", result.Entries[0].Outcome)
	}

	ledger, err := journal.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if ledger.Settled(browserProduct, KindWorkspace,
		"fleetlog.canvasWorkspace.v1:"+loneSession.ID, Checksum([]byte(validWorkspace))) {
		t.Fatal("a failed item was settled, so the next pass would skip it")
	}
}

func TestTheReportCarriesWhatWasLeftBehind(t *testing.T) {
	// One report has to cover both halves, or the operator has to reconcile the
	// exporter's refusals against the importer's by hand.
	importer, _, _ := newBrowserImport(t, resolveFixed(nil))
	raw, _ := json.Marshal(bundle{
		SchemaVersion: BundleSchemaVersion,
		Source:        SourceName,
		Refused:       []skippedKey{{Key: "fleetlog.llmConfig", Reason: "credentials are never migrated"}},
		Unrecognized:  []string{"fleetlog.somethingAddedLater"},
		Truncated:     true,
	})
	result, err := ImportBrowserState(t.Context(), raw, importer)
	if err != nil {
		t.Fatalf("ImportBrowserState: %v", err)
	}
	if len(result.RefusedAtSource) != 1 || result.RefusedAtSource[0].Key != "fleetlog.llmConfig" {
		t.Fatalf("refused = %+v", result.RefusedAtSource)
	}
	if len(result.Unrecognized) != 1 || !result.Truncated {
		t.Fatalf("result = %+v", result)
	}
}

func TestAnImportNeedsItsCollaborators(t *testing.T) {
	// An import that cannot be traced is not one this package performs.
	scope, _ := newScope(t)
	journal, err := OpenJournal(scope, fixedNow())
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	cases := []BrowserImport{
		{Workspaces: newWorkspaces(t), Resolve: resolveFixed(nil)},
		{Journal: journal, Resolve: resolveFixed(nil)},
		{Journal: journal, Workspaces: newWorkspaces(t)},
	}
	for index, importer := range cases {
		if _, err := ImportBrowserState(t.Context(), workspaceBundle(), importer); err == nil {
			t.Fatalf("case %d was accepted", index)
		}
	}
}

func TestTheJournalIsTheOnlyPlaceRerunStateLives(t *testing.T) {
	// Deleting the journal makes the next pass re-examine everything, and the
	// destination guard is what still keeps it from doing damage.
	importer, workspaces, root := newBrowserImport(t, resolveFixed(map[string][]contracts.SessionIdentity{
		loneSession.ID: {loneSession},
	}))
	raw := workspaceBundle(workspaceRecord(loneSession.ID, validWorkspace))
	if _, err := ImportBrowserState(t.Context(), raw, importer); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	scope, err := runfs.OpenScope(root, runfs.ScopeOptions{})
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}
	defer scope.Close()
	if err := scope.AtomicWrite(t.Context(), journalName, nil); err != nil {
		t.Fatalf("truncate journal: %v", err)
	}
	fresh, err := OpenJournal(scope, fixedNow())
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	importer.Journal = fresh

	result, err := ImportBrowserState(t.Context(), raw, importer)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	entry := onlyEntry(t, result)
	if entry.Outcome != Skipped || !strings.Contains(entry.Reason, "will not overwrite") {
		t.Fatalf("entry = %+v", entry)
	}
	stored, _ := workspaces.Load(t.Context(), loneSession)
	if stored.Revision != 1 {
		t.Fatalf("revision = %d; the destination was written twice", stored.Revision)
	}
}
