package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type syncTestCredentials struct {
	mu      sync.Mutex
	value   string
	deleted int
}

func (c *syncTestCredentials) Load(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == "" {
		return "", hubclient.ErrNotPaired
	}
	return c.value, nil
}

func (c *syncTestCredentials) Save(_ context.Context, value string) error {
	c.mu.Lock()
	c.value = value
	c.mu.Unlock()
	return nil
}

func (c *syncTestCredentials) Delete(context.Context) error {
	c.mu.Lock()
	c.value = ""
	c.deleted++
	c.mu.Unlock()
	return nil
}

func (c *syncTestCredentials) deletedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleted
}

func syncTestClient(t *testing.T, credentials hubclient.CredentialStore) *hubclient.Client {
	t.Helper()
	base, err := url.Parse("https://hub.example")
	if err != nil {
		t.Fatal(err)
	}
	return &hubclient.Client{BaseURL: base, Credentials: credentials, CollectorVersion: "1.2.3"}
}

func newTestSyncController(t *testing.T, start syncLoopStarter, localPause func() bool) *syncController {
	t.Helper()
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("COSLASH_V4_SYNC", "1")
	controller, err := newSyncController(start, localPause)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.Stop)
	return controller
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sync loop")
	}
}

func TestPairingStartsSyncWithoutRestart(t *testing.T) {
	credentials := &syncTestCredentials{}
	var ensureCalls atomic.Int32
	var loadedBeforeEnsure atomic.Bool
	onboardings := newOnboardingManager("1.2.3")
	defer onboardings.Close()
	onboardings.setSyncHooks(syncHooksSpy{ensureCalls: &ensureCalls, loadedBeforeEnsure: &loadedBeforeEnsure})

	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/device-authorizations":
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":"pair","deviceCode":"private-device-code","userCode":"ABCD-EFGH","verificationUri":"http://%s/device","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`, r.Host)
		case "/v1/device-authorizations/token":
			_, _ = w.Write([]byte(`{"deviceId":"device","credential":"saved-device-credential","tokenType":"Device","scope":"ingest"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()
	base, err := url.Parse(hub.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &hubclient.Client{BaseURL: base, Credentials: credentials, HTTP: hub.Client()}
	api := http.NewServeMux()
	registerHubRoutes(api, client, nil, nil, onboardings)

	begin := httptest.NewRecorder()
	api.ServeHTTP(begin, httptest.NewRequest(http.MethodPost, "/api/hub/pairings", nil))
	var pairing hubclient.PairingResult
	if begin.Code != http.StatusCreated || json.Unmarshal(begin.Body.Bytes(), &pairing) != nil {
		t.Fatalf("begin pairing response = %d %s", begin.Code, begin.Body.String())
	}
	poll := httptest.NewRecorder()
	api.ServeHTTP(poll, httptest.NewRequest(http.MethodPost, "/api/hub/pairings/"+pairing.PairingID+"/poll", nil))
	var result hubclient.PairingResult
	if poll.Code != http.StatusOK || json.Unmarshal(poll.Body.Bytes(), &result) != nil || result.State != "paired" {
		t.Fatalf("pairing poll response = %d %s", poll.Code, poll.Body.String())
	}
	if ensureCalls.Load() != 1 || !loadedBeforeEnsure.Load() {
		t.Fatalf("sync ensure calls=%d credential-loaded-before-ensure=%t", ensureCalls.Load(), loadedBeforeEnsure.Load())
	}
}

type syncHooksSpy struct {
	ensureCalls        *atomic.Int32
	loadedBeforeEnsure *atomic.Bool
}

func (s syncHooksSpy) Ensure(client *hubclient.Client) error {
	credential, err := client.Credentials.Load(context.Background())
	if err == nil && credential == "saved-device-credential" {
		s.loadedBeforeEnsure.Store(true)
	}
	s.ensureCalls.Add(1)
	return err
}

func (syncHooksSpy) RequestPass(string) {}

func TestStartupWithStoredCredentialStartsSync(t *testing.T) {
	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	var starts atomic.Int32
	controller := newTestSyncController(t, func(ctx context.Context, _ *syncv4.Queue, _ *hubclient.Client, _ chan struct{}) {
		starts.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
	}, nil)
	manager := newOnboardingManager("1.2.3")
	defer manager.Close()
	manager.setSyncHooks(controller)

	manager.StartCheckIns(syncTestClient(t, &syncTestCredentials{value: "stored-device-credential"}))
	waitSignal(t, started)
	if starts.Load() != 1 {
		t.Fatalf("sync loops started=%d, want 1", starts.Load())
	}
	controller.Stop()
	waitSignal(t, stopped)
}

func TestEnsureIsIdempotent(t *testing.T) {
	started := make(chan struct{}, 1)
	var starts atomic.Int32
	controller := newTestSyncController(t, func(ctx context.Context, _ *syncv4.Queue, _ *hubclient.Client, _ chan struct{}) {
		starts.Add(1)
		started <- struct{}{}
		<-ctx.Done()
	}, nil)
	client := syncTestClient(t, &syncTestCredentials{value: "stored-device-credential"})
	if err := controller.Ensure(client); err != nil {
		t.Fatal(err)
	}
	if err := controller.Ensure(client); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	if starts.Load() != 1 {
		t.Fatalf("sync loops started=%d, want 1", starts.Load())
	}
}

func TestLocalSyncKillSwitchReportsOff(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("COSLASH_V4_SYNC", "0")
	controller, err := newSyncController(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Stop()
	if err := controller.Ensure(syncTestClient(t, &syncTestCredentials{value: "stored-device-credential"})); err != nil {
		t.Fatal(err)
	}
	if status := controller.Status(); status.State != "auto_sync_off" {
		t.Fatalf("status with local sync disabled = %+v", status)
	}
}

type revokedSyncWorker struct{}

func (revokedSyncWorker) SyncOnce(context.Context) error {
	return hubclient.V4Problem{Code: "device_revoked", HTTPStatus: http.StatusForbidden}
}

func TestRevokedCredentialIsDeletedAndCallsStop(t *testing.T) {
	stopped := make(chan struct{}, 1)
	credentials := &syncTestCredentials{value: "revoked-device-credential"}
	controller := newTestSyncController(t, func(ctx context.Context, _ *syncv4.Queue, _ *hubclient.Client, _ chan struct{}) {
		<-ctx.Done()
		stopped <- struct{}{}
	}, nil)
	client := syncTestClient(t, credentials)
	if err := controller.Ensure(client); err != nil {
		t.Fatal(err)
	}
	controller.mu.RLock()
	binding := controller.binding
	controller.mu.RUnlock()
	worker := controller.observed(revokedSyncWorker{}, client)
	if err := worker.SyncOnce(context.Background()); err == nil {
		t.Fatal("revocation error was swallowed")
	}
	waitSignal(t, stopped)
	if credentials.deletedCount() != 1 {
		t.Fatalf("deleted credentials=%d, want 1", credentials.deletedCount())
	}
	if status := controller.Status(); status.State != "disconnected" {
		t.Fatalf("status after revocation = %+v (binding %s)", status, binding)
	}
}
