package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

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

func TestCacheV2StoreLoadRoundTrip(t *testing.T) {
	cache := NewCache(t.TempDir())
	family := validFamily(t, "root-1")
	snapshot := CachedSnapshotV2{
		Version: cacheV2Version, BaselineID: "req-1", CoverageSinceMs: 500,
		Families: []CachedFamilyV2{{
			Vendor: vendors.AgentClaude, FamilyID: "root-1", Facts: family, Fingerprint: "fp-1", LastSuccessAtMs: 1000,
		}},
		VendorComplete: map[string]bool{vendors.AgentClaude: true},
		Coverage:       []AgentCoverage{{Agent: vendors.AgentClaude, CandidateFiles: 1, SelectedFiles: 1}},
		FetchedAtMs:    1000, RoundTripMs: 42,
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
	info, err := os.Stat(filepath.Join(dir, "snapshot-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v, want 0600", info.Mode().Perm())
	}
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
	file, err := os.Create(path)
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
	ownership, ok, err := cache.LoadHelperOwnership(sourceID)
	if err != nil || !ok || ownership.Version != "v1.2.3" || ownership.Alias != "agent-box" {
		t.Fatalf("LoadHelperOwnership = %#v, %v, %v", ownership, ok, err)
	}
	path, err := cache.helperOwnershipPath(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("helper ownership mode = %v, err=%v", info.Mode(), err)
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
