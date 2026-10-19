package syncv4

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestFingerprintsHitMissAndInvalidate(t *testing.T) {
	root := t.TempDir()
	store, err := OpenFingerprints(root)
	if err != nil {
		t.Fatal(err)
	}
	key := vendors.CacheKey{Agent: "claude", Identity: "/home/x/a.jsonl", Version: "claude-1", Fingerprint: "10:20"}
	if _, ok := store.Lookup(key); ok {
		t.Fatal("empty store hit")
	}
	if err := store.Store(key, json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	payload, ok := store.Lookup(key)
	if !ok || string(payload) != `{"a":1}` {
		t.Fatalf("lookup = %s, %v", payload, ok)
	}
	changed := key
	changed.Fingerprint = "11:20"
	if _, ok := store.Lookup(changed); ok {
		t.Fatal("changed fingerprint hit")
	}
	bumped := key
	bumped.Version = "claude-2"
	if _, ok := store.Lookup(bumped); ok {
		t.Fatal("bumped parser version hit")
	}
	other := vendors.CacheKey{Agent: "codex", Identity: "/home/x/b.jsonl", Version: "codex-1", Fingerprint: "1:2"}
	if err := store.Store(other, json.RawMessage(`{"b":2}`)); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenFingerprints(root)
	if err != nil {
		t.Fatal(err)
	}
	if payload, ok := reopened.Lookup(key); !ok || string(payload) != `{"a":1}` {
		t.Fatalf("reopened lookup = %s, %v", payload, ok)
	}
	stats := reopened.Stats()
	if stats.Entries != 2 || stats.Hits != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(reopened.path(key))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("entry mode = %v, %v", info.Mode(), err)
		}
		dir, err := os.Stat(filepath.Dir(reopened.path(key)))
		if err != nil || dir.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v, %v", dir.Mode(), err)
		}
	}
}

func TestFingerprintsCorruptEntriesAreMissesAndRebuilt(t *testing.T) {
	root := t.TempDir()
	store, err := OpenFingerprints(root)
	if err != nil {
		t.Fatal(err)
	}
	key := vendors.CacheKey{Agent: "claude", Identity: "/home/x/a.jsonl", Version: "claude-1", Fingerprint: "10:20"}
	if err := store.Store(key, json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	path := store.path(key)
	if err := os.WriteFile(path, []byte("{\"agent\":\"claude\",\"identity\":\"/home/x/a.jsonl\",\"version\":\"claude-1\",\"fingerprint\":\"10:20\"}\n{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Lookup(key); ok {
		t.Fatal("corrupt payload hit")
	}
	if err := store.Store(key, json.RawMessage(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if payload, ok := store.Lookup(key); !ok || string(payload) != `{"a":2}` {
		t.Fatalf("rebuilt lookup = %s, %v", payload, ok)
	}
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenFingerprints(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt file kept: %v", err)
	}
	if _, ok := reopened.Lookup(key); ok {
		t.Fatal("corrupt entry hit after reopen")
	}
}

func TestFingerprintsPruneAndCursor(t *testing.T) {
	store, err := OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keep := vendors.CacheKey{Agent: "claude", Identity: "/a", Version: "v", Fingerprint: "1"}
	drop := vendors.CacheKey{Agent: "claude", Identity: "/b", Version: "v", Fingerprint: "1"}
	for _, key := range []vendors.CacheKey{keep, drop} {
		if err := store.Store(key, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.Prune(func(agent, identity string) bool { return identity == "/a" })
	if err != nil || removed != 1 {
		t.Fatalf("prune = %d, %v", removed, err)
	}
	if _, ok := store.Lookup(drop); ok {
		t.Fatal("pruned entry hit")
	}
	if _, ok := store.Lookup(keep); !ok {
		t.Fatal("kept entry missing")
	}
	if _, ok := store.LoadDiscoveryCursor(); ok {
		t.Fatal("cursor present before save")
	}
	if err := store.SaveDiscoveryCursor(json.RawMessage(`{"startedAtMs":5}`)); err != nil {
		t.Fatal(err)
	}
	if cursor, ok := store.LoadDiscoveryCursor(); !ok || string(cursor) != `{"startedAtMs":5}` {
		t.Fatalf("cursor = %s, %v", cursor, ok)
	}
	if err := store.SaveDiscoveryCursor(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.LoadDiscoveryCursor(); ok {
		t.Fatal("cursor present after clear")
	}
	if err := store.Store(vendors.CacheKey{Agent: "claude"}, json.RawMessage(`{}`)); err == nil {
		t.Fatal("incomplete key stored")
	}
}
