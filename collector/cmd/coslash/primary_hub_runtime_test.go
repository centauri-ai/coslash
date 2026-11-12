package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func primaryRuntimeFixture(t *testing.T, credential string) (*hubclient.Client, *syncTestCredentials) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	t.Setenv("COSLASH_HUB_URL", "")
	store := &syncTestCredentials{value: credential}
	previous := hubCredentialStore
	hubCredentialStore = func(string) hubclient.CredentialStore { return store }
	t.Cleanup(func() { hubCredentialStore = previous })
	const origin = "https://hub.coslash.io"
	requireHubTestOK(t, writeStoredHubURL(origin))
	client, err := hubClientFromEnvironment("1.2.3")
	requireHubTestOK(t, err)
	return client, store
}

func primaryRuntimeController(t *testing.T, started, stopped chan struct{}) *syncController {
	t.Helper()
	c, err := newSyncController(func(ctx context.Context, _ *syncv4.Queue, _ *hubclient.Client, _, _ chan struct{}) {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
	}, func() bool { return false })
	requireHubTestOK(t, err)
	t.Cleanup(c.Stop)
	return c
}

func TestPrimaryHubRuntimeMigratesAndRestartsWithPendingWork(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "legacy-credential")
	started, stopped := make(chan struct{}, 2), make(chan struct{}, 2)
	first := primaryRuntimeController(t, started, stopped)
	binding, err := client.V4Binding(context.Background())
	requireHubTestOK(t, err)
	requireHubTestOK(t, first.Queue().Rebind(binding))
	requireHubTestOK(t, first.Queue().Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "pending-bundle"}}, time.Unix(1, 0)))
	requireHubTestOK(t, first.Queue().SaveDiscoveryCursor(binding, []byte(`{"legacy":true}`)))
	spool := filepath.Join(os.Getenv("COSLASH_HOME"), "session-backups", "prepared", "pending-bundle")
	requireHubTestOK(t, os.MkdirAll(spool, 0o700))
	requireHubTestOK(t, os.WriteFile(filepath.Join(spool, "fixture"), []byte("pending"), 0o600))
	installID := first.Queue().InstallID()
	runtime, selected, err := newPrimaryHubRuntime(client, first, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	if selected != client {
		t.Fatal("primary client changed")
	}
	requireHubTestOK(t, runtime.Ensure(selected))
	waitSignal(t, started)
	slot := runtime.workers.slot(runtime.registry.snapshot().Primary)
	if slot.controller != first || slot.controller.Queue() != first.Queue() {
		t.Fatal("supervisor opened a second primary queue")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
	second := primaryRuntimeController(t, started, stopped)
	restarted, selected, err := newPrimaryHubRuntime(client, second, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	requireHubTestOK(t, restarted.Ensure(selected))
	waitSignal(t, started)
	if second.Queue().InstallID() != installID || len(second.Queue().Entries()) != 1 || second.Queue().Entries()[0].BundleID != "pending-bundle" {
		t.Fatal("primary pending queue changed on restart")
	}
	if got, cursor := second.Queue().DiscoveryCursor(); got != binding || string(cursor) != `{"legacy":true}` {
		t.Fatalf("binding/cursor = %q %s", got, cursor)
	}
	if data, err := os.ReadFile(filepath.Join(spool, "fixture")); err != nil || string(data) != "pending" {
		t.Fatalf("primary spool = %q, %v", data, err)
	}
	restarted.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeRejectsMismatchedLegacyBinding(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "old")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	binding, err := client.V4Binding(context.Background())
	requireHubTestOK(t, err)
	requireHubTestOK(t, c.Queue().Rebind(binding))
	requireHubTestOK(t, c.Queue().Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1}}, time.Unix(1, 0)))
	requireHubTestOK(t, store.Save(context.Background(), "different"))
	if _, _, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false }); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	got, _ := c.Queue().DiscoveryCursor()
	if got != binding || len(c.Queue().Entries()) != 1 {
		t.Fatal("mismatch changed pending queue")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("COSLASH_HOME"), "hub-connections", "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatch published registry: %v", err)
	}
}

func TestPrimaryHubRuntimeFreshAndUnpairedThenPaired(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	if runtime.registry.snapshot().Primary != "" {
		t.Fatal("unpaired selector migrated")
	}
	requireHubTestOK(t, runtime.Ensure(selected))
	requireHubTestOK(t, store.Save(context.Background(), "approved"))
	requireHubTestOK(t, runtime.Ensure(selected))
	waitSignal(t, started)
	if runtime.registry.snapshot().Primary != "https://hub.coslash.io" {
		t.Fatal("approved primary not registered")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeEnvironmentAndKillSwitch(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "credential")
	requireHubTestOK(t, os.Remove(filepath.Join(os.Getenv("COSLASH_HOME"), storedHubURLFilename)))
	t.Setenv("COSLASH_HUB_URL", "https://hub.coslash.io")
	client, err := hubClientFromEnvironment("1.2.3")
	requireHubTestOK(t, err)
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	requireHubTestOK(t, runtime.Ensure(selected))
	waitSignal(t, started)
	if runtime.registry.snapshot().Primary != "https://hub.coslash.io" {
		t.Fatal("environment primary not registered")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
	if _, err := os.Stat(filepath.Join(os.Getenv("COSLASH_HOME"), storedHubURLFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("environment created selector: %v", err)
	}
	t.Setenv("COSLASH_V4_SYNC", "0")
	killed := primaryRuntimeController(t, started, stopped)
	if killed.Queue() != nil {
		t.Fatal("kill switch opened a queue")
	}
	disabled, selected, err := newPrimaryHubRuntime(client, killed, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	requireHubTestOK(t, disabled.Ensure(selected))
	if killed.Status().State != "auto_sync_off" {
		t.Fatalf("kill switch status = %s", killed.Status().State)
	}
	disabled.workers.StopAll()
}

func TestPrimaryHubRuntimeEmptyInstallDoesNotCommit(t *testing.T) {
	_, _ = primaryRuntimeFixture(t, "")
	requireHubTestOK(t, os.Remove(filepath.Join(os.Getenv("COSLASH_HOME"), storedHubURLFilename)))
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(nil, c, "1.2.3", func() bool { return false })
	if err != nil || selected != nil {
		t.Fatalf("empty runtime = %v, %v", selected, err)
	}
	requireHubTestOK(t, runtime.Ensure(nil))
	if c.Status().State != "not_connected" || runtime.registry.snapshot().Primary != "" {
		t.Fatal("empty install became paired")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("COSLASH_HOME"), "hub-connections", "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty registry persisted: %v", err)
	}
	runtime.workers.StopAll()
}

func TestPrimaryHubRuntimeEmptyCredentialIsUnpaired(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "")
	client.Credentials = fixedCredential("")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	requireHubTestOK(t, runtime.Ensure(selected))
	if runtime.registry.snapshot().Primary != "" {
		t.Fatal("empty credential was migrated")
	}
	runtime.workers.StopAll()
}

func TestPrimaryHubRuntimeDoesNotStartSecondary(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "credential")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	requireHubTestOK(t, runtime.Ensure(selected))
	waitSignal(t, started)
	requireHubTestOK(t, runtime.registry.addSecondary("https://beta.coslash.io"))
	requireHubTestOK(t, runtime.registry.setEnabled("https://beta.coslash.io", true))
	if err := runtime.workers.StartEnabled("https://beta.coslash.io"); err == nil {
		t.Fatal("primary-only runtime started secondary")
	}
	if !c.RetryNow() {
		t.Fatal("primary stopped after secondary request")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeStopsChangedCredentialWithoutRebinding(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "original")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	requireHubTestOK(t, runtime.Ensure(selected))
	waitSignal(t, started)
	old, _ := c.Queue().DiscoveryCursor()
	requireHubTestOK(t, store.Save(context.Background(), "replacement"))
	if err := runtime.Ensure(selected); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("replacement error = %v", err)
	}
	waitSignal(t, stopped)
	if binding, _ := c.Queue().DiscoveryCursor(); binding != old {
		t.Fatal("replacement rebound the primary queue")
	}
	if got, err := store.Load(context.Background()); err != nil || got != "replacement" {
		t.Fatalf("replacement credential = %q, %v", got, err)
	}
	runtime.workers.StopAll()
}

func TestPrimaryHubRuntimeRefusesForeignLegacySwitch(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "credential")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	manager := newOnboardingManager("1.2.3")
	t.Cleanup(manager.Close)
	manager.setHubClient(selected)
	manager.setSyncHooks(runtime)
	requireHubTestOK(t, runtime.Ensure(selected))
	waitSignal(t, started)
	if err := manager.StartPairing("https://beta.coslash.io", switchAttemptID, strings.Repeat("A", 43)); !errors.Is(err, errHubSwitchPending) {
		t.Fatalf("foreign request = %v", err)
	}
	if err := manager.ResolveSwitch("https://beta.coslash.io", true); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("foreign switch = %v", err)
	}
	if got, err := readStoredHubURL(); err != nil || got != "https://hub.coslash.io" {
		t.Fatalf("saved Hub = %q, %v", got, err)
	}
	if !c.RetryNow() {
		t.Fatal("foreign request stopped primary")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeUnpairedBoundQueueCannotChangeSelector(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	requireHubTestOK(t, c.Queue().Rebind(strings.Repeat("a", 64)))
	runtime, _, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	manager := newOnboardingManager("1.2.3")
	t.Cleanup(manager.Close)
	manager.setSyncHooks(runtime)
	if err := manager.startPairing("https://beta.coslash.io", switchAttemptID, strings.Repeat("A", 43)); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("pairing error = %v", err)
	}
	if got, err := readStoredHubURL(); err != nil || got != "https://hub.coslash.io" {
		t.Fatalf("saved Hub = %q, %v", got, err)
	}
	if got, _ := c.Queue().DiscoveryCursor(); got != strings.Repeat("a", 64) {
		t.Fatal("unpaired queue rebound")
	}
	runtime.workers.StopAll()
}

func TestUnknownSignalDoesNotAdoptUnregisteredPrimary(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	controller := primaryRuntimeController(t, started, stopped)
	runtime, _, err := newPrimaryHubRuntime(client, controller, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	requireHubTestOK(t, store.Save(context.Background(), "orphan-credential"))
	manager := newOnboardingManager("1.2.3")
	defer manager.Close()
	manager.setSyncHooks(runtime)
	if err := manager.RetryCheckIn(client.BaseURL.String()); err == nil {
		t.Fatal("unknown check-in was accepted")
	}
	if err := manager.RequestWake(client.BaseURL.String()); !errors.Is(err, errForeignWakeHub) {
		t.Fatalf("unknown wake: %v", err)
	}
	if runtime.registry.snapshot().Primary != "" {
		t.Fatal("unknown signal registered a primary")
	}
	select {
	case <-started:
		t.Fatal("unknown signal started sync")
	default:
	}
}

func TestPrimaryPairingCannotReplaceBoundCredential(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "original")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	controller := primaryRuntimeController(t, started, stopped)
	binding, err := client.V4Binding(context.Background())
	requireHubTestOK(t, err)
	requireHubTestOK(t, controller.Queue().Rebind(binding))
	requireHubTestOK(t, controller.Queue().Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "pending-bundle"}}, time.Unix(1, 0)))
	runtime, client, err := newPrimaryHubRuntime(client, controller, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	manager := newOnboardingManager("1.2.3")
	manager.setSyncHooks(runtime)
	manager.setHubClient(client)
	if err := manager.currentHubClient().Credentials.Save(context.Background(), "replacement"); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("replacement credential: %v", err)
	}
	if got, err := store.Load(context.Background()); err != nil || got != "original" {
		t.Fatalf("original credential = %q, %v", got, err)
	}
	if got, _ := controller.Queue().DiscoveryCursor(); got != binding || len(controller.Queue().Entries()) != 1 {
		t.Fatal("pending primary work changed")
	}
	if err := client.Credentials.Save(context.Background(), "original"); err != nil {
		t.Fatalf("matching credential: %v", err)
	}
	if status := controller.Status(); status.PairingIssue != "" {
		t.Fatalf("matching pairing retained issue: %q", status.PairingIssue)
	}
	t.Setenv("COSLASH_V4_SYNC", "0")
	killed := primaryRuntimeController(t, started, stopped)
	killRuntime, _, err := newPrimaryHubRuntime(client, killed, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	plainClient, err := hubClientForURL("1.2.3", "https://hub.coslash.io")
	requireHubTestOK(t, err)
	killRuntime.guardPairingCredential(plainClient)
	if err := plainClient.Credentials.Save(context.Background(), "replacement"); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("kill switch replacement: %v", err)
	}
	if got, err := store.Load(context.Background()); err != nil || got != "original" {
		t.Fatalf("kill switch credential = %q, %v", got, err)
	}
}

func TestPrimaryPairingAPIPreservesBoundCredential(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "original")
	hub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/device-authorizations":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pair", "deviceCode": "device-code", "userCode": "ABCD", "verificationUri": "https://" + r.Host + "/approve", "expiresAt": time.Now().Add(time.Minute), "intervalSeconds": 1})
		case "/v1/device-authorizations/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"deviceId": "device", "credential": "replacement", "tokenType": "Device", "scope": "ingest"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()
	client.BaseURL, _ = url.Parse(hub.URL)
	client.HTTP = hub.Client()
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	controller := primaryRuntimeController(t, started, stopped)
	binding, err := client.V4Binding(context.Background())
	requireHubTestOK(t, err)
	requireHubTestOK(t, controller.Queue().Rebind(binding))
	requireHubTestOK(t, controller.Queue().Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1}}, time.Unix(1, 0)))
	runtime, client, err := newPrimaryHubRuntime(client, controller, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	manager := newOnboardingManager("1.2.3")
	manager.setSyncHooks(runtime)
	mux := http.NewServeMux()
	registerHubRoutes(mux, client, nil, manager)
	begin := httptest.NewRecorder()
	mux.ServeHTTP(begin, httptest.NewRequest(http.MethodPost, "/api/hub/pairings", nil))
	if begin.Code != http.StatusCreated {
		t.Fatalf("begin status %d: %s", begin.Code, begin.Body.String())
	}
	poll := httptest.NewRecorder()
	mux.ServeHTTP(poll, httptest.NewRequest(http.MethodPost, "/api/hub/pairings/pair/poll", nil))
	var result hubclient.PairingResult
	if err := json.Unmarshal(poll.Body.Bytes(), &result); err != nil || result.State != hubclient.PairingStateBindingMismatch {
		t.Fatalf("poll = %s, %v", result.State, err)
	}
	if status := controller.Status(); status.PairingIssue != "binding_mismatch" {
		t.Fatalf("status pairing issue = %q", status.PairingIssue)
	}

	if got, err := store.Load(context.Background()); err != nil || got != "original" {
		t.Fatalf("credential = %q, %v", got, err)
	}
	if got, _ := controller.Queue().DiscoveryCursor(); got != binding || len(controller.Queue().Entries()) != 1 {
		t.Fatal("pending primary work changed")
	}
}

func TestPrimaryPairingWithDisabledSyncAndFreshQueue(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	_ = primaryRuntimeController(t, started, stopped) // Persist the fresh unbound queue.
	t.Setenv("COSLASH_V4_SYNC", "0")
	controller := primaryRuntimeController(t, started, stopped)
	runtime, client, err := newPrimaryHubRuntime(client, controller, "1.2.3", func() bool { return false })
	requireHubTestOK(t, err)
	runtime.guardPairingCredential(client)
	if err := client.Credentials.Save(context.Background(), "first-credential"); err != nil {
		t.Fatalf("first credential: %v", err)
	}
	if got, err := store.Load(context.Background()); err != nil || got != "first-credential" {
		t.Fatalf("credential = %q, %v", got, err)
	}
	requireHubTestOK(t, os.WriteFile(filepath.Join(os.Getenv("COSLASH_HOME"), "sync-v4", "queue.json"), []byte("corrupt"), 0o600))
	if err := client.Credentials.Save(context.Background(), "replacement"); err == nil {
		t.Fatal("corrupt queue accepted replacement credential")
	}
	if got, err := store.Load(context.Background()); err != nil || got != "first-credential" {
		t.Fatalf("credential after corrupt queue = %q, %v", got, err)
	}
}
