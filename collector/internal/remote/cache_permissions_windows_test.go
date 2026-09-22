package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/centauri-ai/coslash/collector/internal/windowstest"
)

func assertPrivateCachePath(t *testing.T, path string, directory bool) {
	t.Helper()
	windowstest.AssertPrivateACL(t, path, directory)
}

func TestCacheLoadRepairsLegacyBroadACLs(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	snapshot := CachedSnapshotV2{Version: cacheV2Version, BaselineID: "req-1"}
	if err := cache.StoreV2(sourceID, snapshot); err != nil {
		t.Fatal(err)
	}
	remotes := filepath.Join(root, "remotes")
	directory := filepath.Join(remotes, sourceID)
	path := filepath.Join(directory, "snapshot-v2.json")
	windowstest.SetDACL(t, remotes, "D:(A;OICI;FA;;;WD)", true)
	windowstest.SetDACL(t, directory, "D:(A;OICI;FA;;;WD)", true)
	windowstest.SetDACL(t, path, "D:(A;;FA;;;WD)", false)

	if _, ok, err := cache.LoadV2(sourceID); err != nil || !ok {
		t.Fatalf("LoadV2 after ACL migration: ok=%v err=%v", ok, err)
	}
	assertPrivateCachePath(t, remotes, true)
	assertPrivateCachePath(t, directory, true)
	assertPrivateCachePath(t, path, false)
}

func TestCacheLoadRejectsHardLinkedFile(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	if err := cache.StoreV2(sourceID, CachedSnapshotV2{BaselineID: "req-1"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "remotes", sourceID, "snapshot-v2.json")
	if err := os.Link(path, filepath.Join(filepath.Dir(path), "snapshot-copy.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.LoadV2(sourceID); err == nil || !strings.Contains(err.Error(), "must not be hard linked") {
		t.Fatalf("hard-linked cache error = %v", err)
	}
}

func TestCacheSupportsLongUnicodeRoot(t *testing.T) {
	root := t.TempDir()
	for index := 0; len(utf16.Encode([]rune(root))) <= 300; index++ {
		root = filepath.Join(root, fmt.Sprintf("缓存-%02d-%s", index, strings.Repeat("长", 24)))
	}
	cache := NewCache(root)
	const sourceID = "r_0123456789abcdef"
	if err := cache.StoreV2(sourceID, CachedSnapshotV2{BaselineID: "req-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.LoadV2(sourceID); err != nil || !ok {
		t.Fatalf("long-path LoadV2: ok=%v err=%v", ok, err)
	}
	remotes := filepath.Join(root, "remotes")
	directory := filepath.Join(remotes, sourceID)
	assertPrivateCachePath(t, remotes, true)
	assertPrivateCachePath(t, directory, true)
	assertPrivateCachePath(t, filepath.Join(directory, "snapshot-v2.json"), false)
}
