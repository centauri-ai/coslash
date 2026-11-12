package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
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
	if os.Getenv("COSLASH_TEST_ROLLBACK_CHILD") == "1" {
		os.Args = []string{os.Args[0], "--no-open", "--port", "0"}
		main()
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
	if err != nil {
		t.Fatal(err)
	}
	store := hubclient.FileCredentialStore{Path: filepath.Join(home, "hub-credentials", origin.Host)}
	if err := store.Save(context.Background(), "original"); err != nil {
		t.Fatal(err)
	}
	if err := writeStoredHubURL(hub.URL); err != nil {
		t.Fatal(err)
	}
	binding, err := (&hubclient.Client{BaseURL: origin, Credentials: store}).V4Binding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Rebind(binding); err != nil {
		t.Fatal(err)
	}
	if err := queue.Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "pending-bundle"}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := queue.SaveDiscoveryCursor(binding, []byte(`{"plan":1}`)); err != nil {
		t.Fatal(err)
	}
	installID := queue.InstallID()
	spool := filepath.Join(home, "session-backups", "prepared", "pending-bundle", "fixture")
	secondary := "https://secondary.example"
	secondaryFile := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(secondary))), "prepared", "fixture")
	for _, path := range []string{spool, secondaryFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("synthetic pending"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := openHubRegistry(home, hub.URL, func(string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.addSecondary(secondary); err != nil {
		t.Fatal(err)
	}
	secondaryQueueRoot := filepath.Join(filepath.Dir(filepath.Dir(secondaryFile)), "sync-v4")
	secondaryQueue, err := syncv4.Open(secondaryQueueRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondaryQueue.Rebind(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := secondaryQueue.Merge([]syncv4.Entry{{Key: "secondary-pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "secondary-bundle"}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	secondaryQueueBefore, err := os.ReadFile(filepath.Join(secondaryQueueRoot, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	registryBefore, err := os.ReadFile(filepath.Join(home, "hub-connections", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	run := func(binary string, args ...string) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "COSLASH_TEST_ROLLBACK_CHILD=1")
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		var desc runtimeDescriptor
		var token []byte
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			data, e := os.ReadFile(filepath.Join(home, "runtime.json"))
			token, err = os.ReadFile(filepath.Join(home, "token"))
			if e == nil && err == nil && json.Unmarshal(data, &desc) == nil && desc.BaseURL != "" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if desc.BaseURL == "" {
			t.Fatal("Local did not start")
		}
		req, err := http.NewRequest(http.MethodPost, desc.BaseURL+"/api/shutdown", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Coslash-Token", strings.TrimSpace(string(token)))
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("shutdown status %d", resp.StatusCode)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Local exit: %v: %s", err, output.String())
			}
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Fatal("Local did not stop")
		}
	}
	run(os.Args[0], "-test.run=^TestUnchangedPrimaryDowngradeAndReupgrade$")
	run(oldBinary, "--no-open", "--port", "0")
	data, err := os.ReadFile(filepath.Join(home, "sync-v4", "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oldQueue map[string]json.RawMessage
	if json.Unmarshal(data, &oldQueue) != nil {
		t.Fatal("invalid old queue")
	}
	if _, ok := oldQueue["discoveryCursor"]; ok {
		t.Fatal("old binary unexpectedly retained new cursor")
	}
	run(os.Args[0], "-test.run=^TestUnchangedPrimaryDowngradeAndReupgrade$")
	reopened, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	if err != nil {
		t.Fatal(err)
	}
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
