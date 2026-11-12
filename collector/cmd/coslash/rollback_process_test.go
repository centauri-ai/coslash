package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

// Supply an independently built 4baa60ae binary to exercise its real queue decoder.
func TestUnchangedPrimaryDowngradeAndReupgrade(t *testing.T) {
	if runTestLocalChild() {
		return
	}
	oldBinary := os.Getenv("COSLASH_TEST_OLD_BINARY")
	if oldBinary == "" || runtime.GOOS != "linux" {
		t.Skip("requires an explicitly supplied Linux binary built from 4baa60ae")
	}
	home := t.TempDir()
	for k, v := range map[string]string{"COSLASH_HOME": home, "HOME": home, "USERPROFILE": home, "PATH": home, "XDG_CONFIG_HOME": home, "XDG_DATA_HOME": home, "COSLASH_HUB_URL": "", "COSLASH_V4_SYNC": "1", "COSLASH_SYNC_PAUSED": "1", "PI_CODING_AGENT_DIR": filepath.Join(home, "empty"), "OPENCODE_DB": filepath.Join(home, "missing.db")} {
		t.Setenv(k, v)
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer hub.Close()
	origin, err := url.Parse(hub.URL)
	requireHubTestOK(t, err)
	store := hubclient.FileCredentialStore{Path: filepath.Join(home, "hub-credentials", origin.Host)}
	requireHubTestOK(t, store.Save(context.Background(), "original"))
	requireHubTestOK(t, writeStoredHubURL(hub.URL))
	binding, err := (&hubclient.Client{BaseURL: origin, Credentials: store}).V4Binding(context.Background())
	requireHubTestOK(t, err)
	queue, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	requireHubTestOK(t, queue.Rebind(binding))
	requireHubTestOK(t, queue.Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "pending-bundle"}}, time.Unix(1, 0)))
	requireHubTestOK(t, queue.SaveDiscoveryCursor(binding, []byte(`{"plan":1}`)))
	installID := queue.InstallID()
	spool := filepath.Join(home, "session-backups", "prepared", "pending-bundle", "fixture")
	secondary := "https://secondary.example"
	secondaryFile := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(secondary))), "prepared", "fixture")
	for _, path := range []string{spool, secondaryFile} {
		requireHubTestOK(t, os.MkdirAll(filepath.Dir(path), 0700))
		requireHubTestOK(t, os.WriteFile(path, []byte("synthetic pending"), 0600))
	}
	registry, err := openHubRegistry(home, hub.URL, func(string) (bool, error) { return true, nil })
	requireHubTestOK(t, err)
	requireHubTestOK(t, registry.addSecondary(secondary))
	secondaryQueueRoot := filepath.Join(filepath.Dir(filepath.Dir(secondaryFile)), "sync-v4")
	secondaryQueue, err := syncv4.Open(secondaryQueueRoot)
	requireHubTestOK(t, err)
	requireHubTestOK(t, secondaryQueue.Rebind(strings.Repeat("a", 64)))
	requireHubTestOK(t, secondaryQueue.Merge([]syncv4.Entry{{Key: "secondary-pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "secondary-bundle"}}, time.Unix(1, 0)))
	secondaryQueueBefore, err := os.ReadFile(filepath.Join(secondaryQueueRoot, "queue.json"))
	requireHubTestOK(t, err)
	registryBefore, err := os.ReadFile(filepath.Join(home, "hub-connections", "registry.json"))
	requireHubTestOK(t, err)

	run := func(binary string) { startTestLocal(t, home, binary).stop() }
	run("")
	run(oldBinary)
	data, err := os.ReadFile(filepath.Join(home, "sync-v4", "queue.json"))
	requireHubTestOK(t, err)
	var oldQueue map[string]json.RawMessage
	if json.Unmarshal(data, &oldQueue) != nil {
		t.Fatal("invalid old queue")
	}
	if _, ok := oldQueue["discoveryCursor"]; ok {
		t.Fatal("old binary unexpectedly retained new cursor")
	}
	run("")
	reopened, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	if got, _ := reopened.DiscoveryCursor(); got != binding || reopened.InstallID() != installID || len(reopened.Entries()) != 1 || reopened.Entries()[0].BundleID != "pending-bundle" {
		t.Fatal("primary pending queue changed")
	}
	for _, path := range []string{spool, secondaryFile} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic pending" {
			t.Fatalf("pending data %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(home, "hub-connections", "registry.json")); err != nil || !bytes.Equal(data, registryBefore) {
		t.Fatal("registry changed during rollback")
	}
	if data, err := os.ReadFile(filepath.Join(secondaryQueueRoot, "queue.json")); err != nil || !bytes.Equal(data, secondaryQueueBefore) {
		t.Fatal("secondary queue changed during rollback")
	}
	if got, err := readStoredHubURL(); err != nil || got != hub.URL {
		t.Fatal("primary selector changed")
	}
	if got, err := store.Load(context.Background()); err != nil || got != "original" {
		t.Fatal("primary credential changed")
	}
}
