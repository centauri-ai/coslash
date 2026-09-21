package remote

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func rebindSnapshotSource(t *testing.T, snapshot CachedSnapshotV2, sourceID string) CachedSnapshotV2 {
	t.Helper()
	snapshot.SourceID = sourceID
	for index := range snapshot.FullRecords {
		record := snapshot.FullRecords[index].Record
		record.SourceID = sourceID
		frozen, err := fullsessionv1.Freeze(record)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.FullRecords[index].Record = frozen
	}
	return snapshot
}

func validFamily(t *testing.T, familyID string) remotefacts.Family {
	t.Helper()
	family := remotefacts.Family{
		SchemaVersion: remotefacts.SchemaVersion, ParserVersion: "test/1",
		Vendor: vendors.AgentClaude, FamilyID: familyID, State: remotefacts.StateComplete,
		Sessions: []remotefacts.Session{{
			ID: familyID, StartedAtMs: 1000, LastActivityAtMs: 2000,
			Counts: remotefacts.Counts{}, Usage: []remotefacts.ModelUsage{}, Spawns: []remotefacts.Spawn{}, CommandLabels: []string{},
		}},
		Fingerprints: []remotefacts.Fingerprint{{Key: "file-1", Size: 10, ModifiedAtMs: 1000}},
	}
	if err := remotefacts.Validate(family); err != nil {
		t.Fatalf("validFamily: %v", err)
	}
	return family
}

func completeCodexSnapshot(t *testing.T, baselineID, body string) CachedSnapshotV2 {
	t.Helper()
	edits := session.NewFileEditSet()
	edits.Add("main.go", 1, 0, true)
	edits.Write("main.go", body)
	parsed := session.Session{
		Agent: vendors.AgentCodex, ID: "root-1", WorkingDirectory: "/workspace",
		EditedFileCount: 1, StartedAt: 1000, LastActivityTime: 2000, Tokens: map[string]session.ModelTokens{},
		Subagents: []session.Subagent{}, SessionDetails: session.SessionDetails{
			Commands: []string{}, Commits: []string{}, CommitSHAs: []string{}, Todos: []session.Todo{},
			Digest: []session.DigestEntry{}, FileEdits: edits.Edits,
		},
	}
	record, err := fullsessionrecord.FromSession("r_0123456789abcdef", parsed)
	if err != nil {
		t.Fatal(err)
	}
	family := validFamily(t, "root-1")
	family.Vendor = vendors.AgentCodex
	if err := remotefacts.Validate(family); err != nil {
		t.Fatal(err)
	}
	return CachedSnapshotV2{
		Version: cacheV2Version, SourceID: "r_0123456789abcdef", BaselineID: baselineID,
		Families:    []CachedFamilyV2{{Vendor: vendors.AgentCodex, FamilyID: "root-1", Facts: family, Fingerprint: "fp-1", LastSuccessAtMs: 1000}},
		FullRecords: []remoteprotocol.FullRecord{{FamilyID: "root-1", Record: record}},
		FetchedAtMs: 1000,
	}
}

func TestCacheV2SeparatesBodiesAndFallsBackToPreviousCompleteGeneration(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	first := completeCodexSnapshot(t, "generation-1", "first body\n")
	if err := cache.StoreV2(sourceID, first); err != nil {
		t.Fatal(err)
	}
	second := completeCodexSnapshot(t, "generation-2", "second body\n")
	if err := cache.StoreV2(sourceID, second); err != nil {
		t.Fatal(err)
	}

	path, _ := cache.snapshotV2Path(sourceID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted CachedSnapshotV2
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	change := persisted.FullRecords[0].Record.Session.FileEdits[0].Changes[0]
	if change.Text != "" || len(persisted.ChangeBodies) != 1 || persisted.ChangeBodies[0].Text != "second body\n" {
		t.Fatalf("persisted row/body split = change:%#v bodies:%#v", change, persisted.ChangeBodies)
	}

	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := cache.LoadV2(sourceID)
	if err != nil || !ok {
		t.Fatalf("LoadV2 fallback: ok=%v err=%v", ok, err)
	}
	if loaded.BaselineID != "generation-1" || loaded.FullRecords[0].Record.Session.FileEdits[0].Changes[0].Text != "first body\n" {
		t.Fatalf("fallback generation = %#v", loaded)
	}
}

func TestCacheV2RejectsCollectionCardinalityBeforeDecode(t *testing.T) {
	data := append([]byte(`{"families":[`), []byte(strings.Repeat(`{},`, remoteprotocol.MaxRecords))...)
	data = append(data, []byte(`{}]}`)...)
	if _, ok, err := decodeCachedSnapshot(data); err != nil || ok {
		t.Fatalf("oversized cache collection: ok=%v err=%v", ok, err)
	}
}

func TestCacheV2RejectsMismatchedCurrentSourceAndFallsBackToPrevious(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceA = "r_aaaaaaaaaaaaaaaa"
	const sourceB = "r_bbbbbbbbbbbbbbbb"

	previousB := rebindSnapshotSource(t, completeCodexSnapshot(t, "b-previous", "previous B\n"), sourceB)
	if err := cache.StoreV2(sourceB, previousB); err != nil {
		t.Fatal(err)
	}
	currentB := rebindSnapshotSource(t, completeCodexSnapshot(t, "b-current", "current B\n"), sourceB)
	if err := cache.StoreV2(sourceB, currentB); err != nil {
		t.Fatal(err)
	}

	currentA := rebindSnapshotSource(t, completeCodexSnapshot(t, "a-current", "source A\n"), sourceA)
	if err := cache.StoreV2(sourceA, currentA); err != nil {
		t.Fatal(err)
	}
	pathA, _ := cache.snapshotV2Path(sourceA)
	pathB, _ := cache.snapshotV2Path(sourceB)
	dataA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, dataA, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := cache.LoadV2(sourceB)
	if err != nil || !ok {
		t.Fatalf("LoadV2 fallback: ok=%v err=%v", ok, err)
	}
	if loaded.SourceID != sourceB || loaded.BaselineID != "b-previous" ||
		loaded.FullRecords[0].Record.SourceID != sourceB {
		t.Fatalf("loaded mismatched source generation: %#v", loaded)
	}

	replacementB := rebindSnapshotSource(t, completeCodexSnapshot(t, "b-replacement", "replacement B\n"), sourceB)
	if err := cache.StoreV2(sourceB, replacementB); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err = cache.LoadV2(sourceB)
	if err != nil || !ok || loaded.BaselineID != "b-previous" {
		t.Fatalf("wrong-source current displaced last-good previous: cached=%#v ok=%v err=%v", loaded, ok, err)
	}
}

func TestCacheV2FailedReplacementAndRestartKeepExactRecordReadable(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	snapshot := completeCodexSnapshot(t, "generation-1", "durable body\n")
	if err := cache.StoreV2(sourceID, snapshot); err != nil {
		t.Fatal(err)
	}
	broken := completeCodexSnapshot(t, "generation-2", "replacement\n")
	broken.FullRecords[0].Record.SourceID = "r_ffffffffffffffff"
	if err := cache.StoreV2(sourceID, broken); err == nil {
		t.Fatal("source-mismatched replacement was stored")
	}

	manager := NewManager(Options{Cache: cache})
	t.Cleanup(manager.Shutdown)
	config := &settings.RemoteSettings{ID: sourceID, SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	revision := snapshot.FullRecords[0].Record.RevisionID
	record, err := manager.ReadFullSession(sourceID, vendors.AgentCodex, "root-1", revision)
	if err != nil || record == nil {
		t.Fatalf("ReadFullSession: record=%#v err=%v", record, err)
	}
	changeID := record.Session.FileEdits[0].Changes[0].ID
	change, err := manager.ReadChange(sourceID, vendors.AgentCodex, "root-1", revision, changeID)
	if err != nil || change == nil || change.Text != "durable body\n" {
		t.Fatalf("ReadChange: change=%#v err=%v", change, err)
	}
	if _, err := manager.ReadFullSession(sourceID, vendors.AgentCodex, "root-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, ErrRemoteRevisionNotFound) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestRemoveSourceDeletesCurrentPreviousAndCompleteBodies(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	if err := cache.StoreV2(sourceID, completeCodexSnapshot(t, "generation-1", "first\n")); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreV2(sourceID, completeCodexSnapshot(t, "generation-2", "second\n")); err != nil {
		t.Fatal(err)
	}
	if err := cache.RemoveSource(sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "remotes", sourceID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source cache still exists: %v", err)
	}
}

func TestCacheV2StoreLoadRoundTrip(t *testing.T) {
	cache := NewCache(t.TempDir())
	family := validFamily(t, "root-1")
	snapshot := CachedSnapshotV2{
		Version: cacheV2Version, BaselineID: "req-1", CoverageSinceMs: 500,
		Families: []CachedFamilyV2{{
			Vendor: vendors.AgentClaude, FamilyID: "root-1", Facts: family, Fingerprint: "fp-1", LastSuccessAtMs: 1000,
		}},
		Coverage:    []AgentCoverage{{Agent: vendors.AgentClaude, CandidateFiles: 1, SelectedFiles: 1}},
		FetchedAtMs: 1000, RoundTripMs: 42,
		CodexHeaders: []CachedCodexHeader{{Key: "k1", Size: 5, ModifiedAtMs: 100, ParserVersion: codexParserVersion, SessionID: "s1"}},
	}
	if err := cache.StoreV2("r_0123456789abcdef", snapshot); err != nil {
		t.Fatalf("StoreV2: %v", err)
	}
	loaded, ok, err := cache.LoadV2("r_0123456789abcdef")
	if err != nil || !ok {
		t.Fatalf("LoadV2: ok=%v err=%v", ok, err)
	}
	if loaded.BaselineID != "req-1" || len(loaded.Families) != 1 || loaded.Families[0].FamilyID != "root-1" {
		t.Fatalf("round-tripped snapshot mismatch: %+v", loaded)
	}
	if len(loaded.CodexHeaders) != 1 || loaded.CodexHeaders[0].SessionID != "s1" {
		t.Fatalf("codex headers did not round-trip: %+v", loaded.CodexHeaders)
	}
}

func TestKnownFamiliesIncludeCodexHeaderMappings(t *testing.T) {
	family := validFamily(t, "root-1")
	family.Vendor = vendors.AgentCodex
	family.HeaderMappings = []remotefacts.HeaderMapping{{Key: "file-1", SessionID: "root-1"}}
	if err := remotefacts.Validate(family); err != nil {
		t.Fatal(err)
	}
	snapshot := CachedSnapshotV2{Families: []CachedFamilyV2{{
		Vendor: vendors.AgentCodex, FamilyID: "root-1", Fingerprint: "family-1", Facts: family,
	}}}
	known := knownFamiliesFor(snapshot)
	if len(known) != 1 || len(known[0].Headers) != 1 {
		t.Fatalf("known families = %#v", known)
	}
	if got, want := known[0].Headers[0], (remoteprotocol.KnownHeader{Key: "file-1", Size: 10, ModifiedAtMs: 1000, SessionID: "root-1"}); got != want {
		t.Fatalf("known header = %#v, want %#v", got, want)
	}
}

func TestSnapshotOrEmptyRequiresEveryCodexFamilyRecord(t *testing.T) {
	snapshot := completeCodexSnapshot(t, "generation-1", "body\n")
	snapshot.Families[0].Facts.Sessions = append(snapshot.Families[0].Facts.Sessions, remotefacts.Session{
		ID: "child-1", ParentID: "root-1", StartedAtMs: 1000, LastActivityAtMs: 2000,
		Counts: remotefacts.Counts{}, Usage: []remotefacts.ModelUsage{}, Spawns: []remotefacts.Spawn{}, CommandLabels: []string{},
	})
	if err := remotefacts.Validate(snapshot.Families[0].Facts); err != nil {
		t.Fatal(err)
	}

	incomplete := snapshotOrEmpty(&snapshot)
	if !strings.HasPrefix(incomplete.Families[0].Fingerprint, "complete-record-required-") {
		t.Fatalf("root-only family fingerprint was not invalidated: %q", incomplete.Families[0].Fingerprint)
	}

	child := snapshot.FullRecords[0]
	child.Record.SessionID = "child-1"
	child.Record.ParentSessionID = "root-1"
	frozen, err := fullsessionv1.Freeze(child.Record)
	if err != nil {
		t.Fatal(err)
	}
	child.Record = frozen
	snapshot.FullRecords = append(snapshot.FullRecords, child)
	complete := snapshotOrEmpty(&snapshot)
	if complete.Families[0].Fingerprint != snapshot.Families[0].Fingerprint {
		t.Fatalf("complete family fingerprint changed: got %q want %q", complete.Families[0].Fingerprint, snapshot.Families[0].Fingerprint)
	}
}

func TestCacheV2StoreIsAtomicAndPermissioned(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	snapshot := CachedSnapshotV2{Version: cacheV2Version, BaselineID: "req-1"}
	if err := cache.StoreV2("r_0123456789abcdef", snapshot); err != nil {
		t.Fatalf("StoreV2: %v", err)
	}
	dir := filepath.Join(root, "remotes", "r_0123456789abcdef")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
	assertPrivateCachePath(t, filepath.Join(root, "remotes"), true)
	assertPrivateCachePath(t, dir, true)
	assertPrivateCachePath(t, filepath.Join(dir, "snapshot-v2.json"), false)
}

func TestCacheV2LoadRejectsCorruptFile(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	dir := filepath.Join(root, "remotes", "r_0123456789abcdef")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot-v2.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := cache.LoadV2("r_0123456789abcdef")
	if err != nil {
		t.Fatalf("LoadV2 on corrupt file should degrade, not error: %v", err)
	}
	if ok {
		t.Fatal("corrupt cache file should not be treated as valid")
	}
}

func TestCacheV2LoadRejectsOversizedFile(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	dir := filepath.Join(root, "remotes", "r_0123456789abcdef")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "snapshot-v2.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxCacheV2Bytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.LoadV2("r_0123456789abcdef"); err != nil || ok {
		t.Fatalf("oversized cache should degrade safely: ok=%v err=%v", ok, err)
	}
}

func TestCacheV2StoreDoesNotReadOrRotateOversizedCurrent(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	if err := cache.StoreV2(sourceID, CachedSnapshotV2{BaselineID: "first"}); err != nil {
		t.Fatal(err)
	}
	path, _ := cache.snapshotV2Path(sourceID)
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxCacheV2Bytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreV2(sourceID, CachedSnapshotV2{BaselineID: "replacement"}); err != nil {
		t.Fatal(err)
	}
	previous, _ := cache.snapshotV2PreviousPath(sourceID)
	if _, err := os.Stat(previous); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized current was rotated to previous: %v", err)
	}
}

func TestCacheV2DoesNotReapUnownedTempFiles(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	if err := cache.StoreV2(sourceID, CachedSnapshotV2{BaselineID: "first"}); err != nil {
		t.Fatal(err)
	}
	dir, _ := cache.sourceDir(sourceID)
	temp := filepath.Join(dir, ".snapshot-v2-live-writer.tmp")
	if err := os.WriteFile(temp, []byte("in progress"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.LoadV2(sourceID); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreV2(sourceID, CachedSnapshotV2{BaselineID: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(temp); err != nil {
		t.Fatalf("another writer's temp file was removed: %v", err)
	}
}

func TestCloneFullRecordsCopiesOnlyMutableChangeContainers(t *testing.T) {
	records := completeCodexSnapshot(t, "generation", "body\n").FullRecords
	cloned := cloneFullRecords(records)
	cloned[0].Record.Session.FileEdits[0].Changes[0].Text = "changed"
	if got := records[0].Record.Session.FileEdits[0].Changes[0].Text; got != "body\n" {
		t.Fatalf("structural clone mutated input body: %q", got)
	}
}

func TestRetainFullRecordFamiliesKeepsNewestWholeFamilies(t *testing.T) {
	combined := CachedSnapshotV2{SourceID: "r_0123456789abcdef"}
	sizes := map[string]int{}
	for index, id := range []string{"old", "middle", "new"} {
		snapshot := completeCodexSnapshot(t, "generation-"+id, id+" body\n")
		family := snapshot.Families[0]
		family.FamilyID = id
		family.Facts.FamilyID = id
		family.Facts.Sessions[0].ID = id
		family.Facts.Sessions[0].LastActivityAtMs = int64(2_000 + index)
		family.LastSuccessAtMs = int64(3_000 + index)

		full := snapshot.FullRecords[0]
		full.FamilyID = id
		full.Record.SessionID = id
		full.Record.Session.LastActivityAtMs = int64(2_000 + index)
		frozen, err := fullsessionv1.Freeze(full.Record)
		if err != nil {
			t.Fatal(err)
		}
		full.Record = frozen
		size, err := persistedFullRecordBytes([]remoteprotocol.FullRecord{full})
		if err != nil {
			t.Fatal(err)
		}
		sizes[id] = size
		combined.Families = append(combined.Families, family)
		combined.FullRecords = append(combined.FullRecords, full)
	}

	retained, pruned, err := retainFullRecordFamilies(combined, sizes["middle"]+sizes["new"])
	if err != nil || !pruned {
		t.Fatalf("retainFullRecordFamilies: pruned=%v err=%v", pruned, err)
	}
	if len(retained.Families) != 2 || retained.Families[0].FamilyID != "middle" || retained.Families[1].FamilyID != "new" {
		t.Fatalf("retained families = %#v", retained.Families)
	}
	if len(retained.FullRecords) != 2 || retained.FullRecords[0].FamilyID != "middle" || retained.FullRecords[1].FamilyID != "new" {
		t.Fatalf("retained full records = %#v", retained.FullRecords)
	}
	if len(combined.Families) != 3 || len(combined.FullRecords) != 3 {
		t.Fatal("retention mutated its input generation")
	}
}

func TestRetainFullRecordFamiliesChargesPersistedSidecars(t *testing.T) {
	snapshot := completeCodexSnapshot(t, "generation", "body\n")
	canonical, err := fullsessionv1.Marshal(snapshot.FullRecords[0].Record)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := persistedFullRecordBytes(snapshot.FullRecords)
	if err != nil {
		t.Fatal(err)
	}
	if persisted <= len(canonical) {
		t.Fatalf("persisted bytes = %d, canonical bytes = %d", persisted, len(canonical))
	}
	retained, pruned, err := retainFullRecordFamilies(snapshot, len(canonical))
	if err != nil || !pruned || len(retained.FullRecords) != 0 || len(retained.Families) != 0 {
		t.Fatalf("sidecar-aware retention: records=%d families=%d pruned=%v err=%v", len(retained.FullRecords), len(retained.Families), pruned, err)
	}
}

func TestCacheV2LoadRejectsInvalidCoverageBounds(t *testing.T) {
	cache := NewCache(t.TempDir())
	snapshot := CachedSnapshotV2{
		Version:  cacheV2Version,
		Coverage: []AgentCoverage{{Agent: vendors.AgentClaude, CandidateFiles: 1, SelectedFiles: 2}},
	}
	if err := cache.StoreV2("r_0123456789abcdef", snapshot); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.LoadV2("r_0123456789abcdef"); err != nil || ok {
		t.Fatalf("invalid coverage should not load: ok=%v err=%v", ok, err)
	}
}

func TestCacheV2LoadRejectsInvalidFamilyFacts(t *testing.T) {
	cache := NewCache(t.TempDir())
	family := validFamily(t, "root-1")
	family.Sessions = nil // now fails remotefacts.Validate
	snapshot := CachedSnapshotV2{
		Version: cacheV2Version, BaselineID: "req-1",
		Families: []CachedFamilyV2{{Vendor: vendors.AgentClaude, FamilyID: "root-1", Facts: family, Fingerprint: "fp-1", LastSuccessAtMs: 1000}},
	}
	if err := cache.StoreV2("r_0123456789abcdef", snapshot); err != nil {
		t.Fatalf("StoreV2: %v", err)
	}
	_, ok, err := cache.LoadV2("r_0123456789abcdef")
	if err != nil {
		t.Fatalf("LoadV2 should degrade rather than error: %v", err)
	}
	if ok {
		t.Fatal("a snapshot containing an invalid family must not load as valid")
	}
}

func TestCacheV2LoadRejectsUnstructuredStaleReason(t *testing.T) {
	cache := NewCache(t.TempDir())
	snapshot := CachedSnapshotV2{
		Version: cacheV2Version, BaselineID: "req-1",
		Families: []CachedFamilyV2{{
			Vendor: vendors.AgentClaude, FamilyID: "root-1", Facts: validFamily(t, "root-1"),
			Fingerprint: "fp-1", StaleReason: "prompt text from hostile helper", LastSuccessAtMs: 1000,
		}},
	}
	if err := cache.StoreV2("r_0123456789abcdef", snapshot); err != nil {
		t.Fatalf("StoreV2: %v", err)
	}
	if _, ok, err := cache.LoadV2("r_0123456789abcdef"); err != nil || ok {
		t.Fatalf("unstructured stale reason should degrade safely: ok=%v err=%v", ok, err)
	}
}

func TestCacheV2LoadRejectsFactStaleReasonOutsideStaleState(t *testing.T) {
	cache := NewCache(t.TempDir())
	facts := validFamily(t, "root-1")
	facts.StaleReason = "hostile transcript text"
	snapshot := CachedSnapshotV2{
		Version: cacheV2Version, BaselineID: "req-1",
		Families: []CachedFamilyV2{{
			Vendor: vendors.AgentClaude, FamilyID: "root-1", Facts: facts,
			Fingerprint: "fp-1", LastSuccessAtMs: 1000,
		}},
	}
	if err := cache.StoreV2("r_0123456789abcdef", snapshot); err != nil {
		t.Fatalf("StoreV2: %v", err)
	}
	if _, ok, err := cache.LoadV2("r_0123456789abcdef"); err != nil || ok {
		t.Fatalf("fact stale reason should degrade safely: ok=%v err=%v", ok, err)
	}
}

func TestCacheV1RemainsLoadableAlongsideV2(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	legacy := CachedSnapshot{
		Version: cacheVersion, Sessions: []CachedSession{{Agent: vendors.AgentClaude, ID: "s1", StartedAt: 1, LastActivityTime: 2}},
		FetchedAtMs: 1000,
	}
	if err := cache.Store("r_0123456789abcdef", legacy); err != nil {
		t.Fatalf("Store (v1): %v", err)
	}
	_, ok, err := cache.LoadV2("r_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("no v2 snapshot has been written yet; LoadV2 must not fabricate one")
	}
	v1, ok, err := cache.Load("r_0123456789abcdef")
	if err != nil || !ok {
		t.Fatalf("v1 snapshot should still load: ok=%v err=%v", ok, err)
	}
	if len(v1.Sessions) != 1 {
		t.Fatalf("v1 sessions = %d, want 1", len(v1.Sessions))
	}
}

func TestCacheHelperOwnershipPersistsOnlyAValidatedVersion(t *testing.T) {
	cache := NewCache(t.TempDir())
	const sourceID = "r_0123456789abcdef"
	if err := cache.StoreHelperVersion(sourceID, "v1.2.3", "agent-box"); err != nil {
		t.Fatalf("StoreHelperVersion: %v", err)
	}
	path, err := cache.helperOwnershipPath(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateCachePath(t, filepath.Join(cache.Root, "remotes"), true)
	assertPrivateCachePath(t, filepath.Dir(path), true)
	assertPrivateCachePath(t, path, false)
	ownership, ok, err := cache.LoadHelperOwnership(sourceID)
	if err != nil || !ok || ownership.Version != "v1.2.3" || ownership.Alias != "agent-box" {
		t.Fatalf("LoadHelperOwnership = %#v, %v, %v", ownership, ok, err)
	}
	if err := cache.RemoveHelperVersion(sourceID); err != nil {
		t.Fatalf("RemoveHelperVersion: %v", err)
	}
	if _, ok, err := cache.LoadHelperOwnership(sourceID); err != nil || ok {
		t.Fatalf("removed ownership should be absent: ok=%v err=%v", ok, err)
	}
}

func TestCacheHelperOwnershipCorruptionFailsClosedAndLegacyMigratesWithConfiguredAlias(t *testing.T) {
	cache := NewCache(t.TempDir())
	const sourceID = "r_0123456789abcdef"
	path, err := cache.helperOwnershipPath(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.LoadHelperOwnership(sourceID); ok || !errors.Is(err, ErrHelperOwnershipCorrupt) {
		t.Fatalf("corrupt ownership = ok:%v err:%v", ok, err)
	}
	if err := os.WriteFile(path, []byte(`{"version":"v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ownership, ok, err := cache.LoadHelperOwnership(sourceID)
	if !ok || !errors.Is(err, ErrHelperOwnershipLegacy) || ownership.Version != "v1" {
		t.Fatalf("legacy ownership = %#v, %v, %v", ownership, ok, err)
	}
	manager := NewManager(Options{Cache: cache})
	config := &settings.RemoteSettings{ID: sourceID, SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatalf("migrate legacy ownership: %v", err)
	}
	ownership, ok, err = cache.LoadHelperOwnership(sourceID)
	if err != nil || !ok || ownership.Alias != config.SSHAlias {
		t.Fatalf("migrated ownership = %#v, %v, %v", ownership, ok, err)
	}
}
