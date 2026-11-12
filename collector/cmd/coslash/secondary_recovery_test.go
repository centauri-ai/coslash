package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func TestSecondaryRecoveryKeepsBoundPendingWork(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises Linux secondary credential storage")
	}
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	var credential atomic.Value
	credential.Store("replacement")
	var requests atomic.Int32
	hub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/v1/device-authorizations" {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pair", "deviceCode": "device-code", "userCode": "ABCD", "verificationUri": "https://" + r.Host + "/approve", "expiresAt": time.Now().Add(time.Minute), "intervalSeconds": 1})
			return
		}
		if r.URL.Path == "/v1/device-authorizations/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"deviceId": "device", "credential": credential.Load().(string), "tokenType": "Device", "scope": "ingest"})
			return
		}
		http.NotFound(w, r)
	}))
	defer hub.Close()
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	if err = registry.addSecondary(hub.URL); err != nil {
		t.Fatal(err)
	}
	if err = registry.setEnabled(hub.URL, true); err != nil {
		t.Fatal(err)
	}
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
	defer workers.StopAll()
	original := workers.client
	workers.client = func(c hubConnection, primary bool) (*hubclient.Client, error) {
		client, err := original(c, primary)
		if err == nil {
			client.HTTP = hub.Client()
		}
		return client, err
	}
	client, err := original(hubConnection{Origin: hub.URL}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Credentials.Save(context.Background(), "original"); err != nil {
		t.Fatal(err)
	}
	binding, err := client.V4Binding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(hub.URL))))
	q, err := syncv4.Open(filepath.Join(root, "sync-v4"))
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Rebind(binding); err != nil {
		t.Fatal(err)
	}
	if err = q.Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "pending-bundle"}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err = q.SaveDiscoveryCursor(binding, []byte(`{"pending":true}`)); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(root, "sync-v4", "queue.json")
	before, err := os.ReadFile(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(root, "prepared", "pending-bundle", "fixture")
	if err = os.MkdirAll(filepath.Dir(spool), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(spool, []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := newSecondaryPairings(workers)
	ctx := context.Background()
	if _, _, err = manager.beginAttempt(ctx, hub.URL, false, true); err == nil || requests.Load() != 0 {
		t.Fatal("recovery without consent contacted Hub")
	}
	if _, _, err = manager.beginAttempt(ctx, hub.URL, true, true); err == nil || requests.Load() != 0 {
		t.Fatal("recovery overwrote a live credential")
	}
	if _, _, err = manager.beginAttempt(ctx, "https://primary.example", true, true); err == nil {
		t.Fatal("primary recovery accepted as secondary")
	}
	if _, _, err = manager.beginAttempt(ctx, "https://unknown.example", true, true); err == nil {
		t.Fatal("unknown recovery accepted")
	}
	if err = client.Credentials.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	id, _, err := manager.beginAttempt(ctx, hub.URL, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := manager.poll(ctx, id); err != nil || state != "binding_mismatch" {
		t.Fatalf("replacement outcome %q %v", state, err)
	}
	if _, err := client.Credentials.Load(ctx); !errors.Is(err, hubclient.ErrNotPaired) {
		t.Fatal("mismatched credential was installed")
	}
	data, err := os.ReadFile(queuePath)
	if err != nil || string(data) != string(before) {
		t.Fatal("mismatched recovery changed queue")
	}
	if b, err := os.ReadFile(spool); err != nil || string(b) != "pending" {
		t.Fatal("mismatched recovery changed spool")
	}
	if registry.snapshot().Connections[1].Enabled {
		t.Fatal("mismatched recovery enabled sync")
	}
	backupPath := queuePath + ".backup"
	if err := os.Rename(queuePath, backupPath); err != nil {
		t.Fatal(err)
	}
	id, _, err = manager.beginAttempt(ctx, hub.URL, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := manager.poll(ctx, id); err != nil || state != "binding_unknown" {
		t.Fatalf("missing queue outcome %q %v", state, err)
	}
	if _, err := client.Credentials.Load(ctx); !errors.Is(err, hubclient.ErrNotPaired) {
		t.Fatal("unknown binding accepted credential")
	}
	if err := os.Rename(backupPath, queuePath); err != nil {
		t.Fatal(err)
	}
	reopened, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	workers.registry = reopened
	registry = reopened
	manager = newSecondaryPairings(workers)
	credential.Store("original")
	id, _, err = manager.beginAttempt(ctx, hub.URL, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := manager.poll(ctx, id); err != nil || state != "recovered_disabled" {
		t.Fatalf("matching recovery %q %v", state, err)
	}
	got, err := client.Credentials.Load(ctx)
	if err != nil || got != "original" {
		t.Fatal("matching credential was not restored")
	}
	data, err = os.ReadFile(queuePath)
	if err != nil || string(data) != string(before) {
		t.Fatal("matching recovery rewrote queue")
	}
	if registry.snapshot().Connections[1].Enabled {
		t.Fatal("recovery silently enabled sync")
	}
	if b, err := os.ReadFile(spool); err != nil || string(b) != "pending" {
		t.Fatal("matching recovery changed spool")
	}
}
