package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type failingSecondaryStore struct{ hubclient.CredentialStore }

func (f failingSecondaryStore) Save(context.Context, string) error {
	return errors.New("storage failed")
}

func TestSecondaryPairingConsentApprovalAndRestart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises the Linux secondary credential store")
	}
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	registry := workerTestRegistry(t, home, "https://primary.example")
	primary := filepath.Join(home, "hub-url")
	requireHubTestOK(t, os.WriteFile(primary, []byte("primary data"), 0600))
	primaryFiles := map[string]string{
		"hub-credentials/primary.example": "primary credential",
		"sync-v4/queue.json":              "pending primary queue",
		"session-backups/prepared/bundle": "pending primary spool",
	}
	for name, contents := range primaryFiles {
		path := filepath.Join(home, filepath.FromSlash(name))
		requireHubTestOK(t, os.MkdirAll(filepath.Dir(path), 0700))
		requireHubTestOK(t, os.WriteFile(path, []byte(contents), 0600))
	}
	var mode atomic.Int32
	var requests atomic.Int32
	pollStarted, releasePoll := make(chan struct{}, 1), make(chan struct{})
	pollReleased := make(chan struct{}, 1)
	hub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/v1/device-authorizations" {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "attempt", "deviceCode": "device-code", "userCode": "ABCD", "verificationUri": "https://" + r.Host + "/approve", "expiresAt": time.Now().Add(time.Minute), "intervalSeconds": 1})
			return
		}
		if mode.Load() == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"code":"onboarding_declined"}`)
			return
		}
		if mode.Load() == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"code":"pairing_pending"}`)
			return
		}
		if mode.Load() == 3 {
			pollStarted <- struct{}{}
			<-releasePoll
			pollReleased <- struct{}{}
		}
		_, _ = fmt.Fprint(w, `{"deviceId":"device","credential":"secondary-secret","tokenType":"Device","scope":"ingest"}`)
	}))
	defer hub.Close()
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	t.Cleanup(workers.StopAll)
	original := workers.client
	var fail atomic.Bool
	workers.client = func(c hubConnection, primary bool) (*hubclient.Client, error) {
		client, err := original(c, primary)
		if err == nil {
			client.HTTP = hub.Client()
			if fail.Load() {
				client.Credentials = failingSecondaryStore{client.Credentials}
			}
		}
		return client, err
	}
	pairings := newSecondaryPairings(workers)
	ctx := context.Background()
	mux := http.NewServeMux()
	registerSecondaryPairingRoutes(mux, pairings)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/hubs/pairings", strings.NewReader(`{"origin":"`+hub.URL+`","consent":false}`)))
	if response.Code != http.StatusConflict || requests.Load() != 0 {
		t.Fatal("API started pairing without consent")
	}
	if _, _, err := pairings.begin(ctx, hub.URL, false); err == nil || requests.Load() != 0 {
		t.Fatal("pairing occurred without consent")
	}
	mode.Store(1)
	id, _, err := pairings.begin(ctx, hub.URL, true)
	requireHubTestOK(t, err)
	if state, err := pairings.poll(ctx, id); err != nil || state != "declined" {
		t.Fatalf("decline %q %v", state, err)
	}
	if len(registry.snapshot().Connections) != 1 {
		t.Fatal("decline changed registry")
	}
	mode.Store(2)
	id, _, err = pairings.begin(ctx, hub.URL, true)
	requireHubTestOK(t, err)
	if _, _, err = pairings.begin(ctx, hub.URL, true); err == nil {
		t.Fatal("concurrent attempt accepted")
	}
	if state, err := pairings.poll(ctx, id); err != nil || state != "pending" {
		t.Fatalf("pending %q %v", state, err)
	}
	restarted := newSecondaryPairings(workers)
	if _, err := restarted.poll(ctx, id); err == nil || len(registry.snapshot().Connections) != 1 {
		t.Fatal("pending attempt survived restart or changed registry")
	}
	pairings.mu.Lock()
	pairings.attempts[id].pairing.ExpiresAt = time.Now().Add(-time.Second)
	pairings.mu.Unlock()
	if state, err := pairings.poll(ctx, id); err != nil || state != "expired" {
		t.Fatalf("expiry %q %v", state, err)
	}
	if _, err := pairings.poll(ctx, id); err == nil {
		t.Fatal("stale attempt accepted")
	}
	mode.Store(3)
	id, _, err = pairings.begin(ctx, hub.URL, true)
	requireHubTestOK(t, err)
	pollCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _, _ = pairings.poll(pollCtx, id); close(done) }()
	<-pollStarted
	if _, _, err := pairings.begin(ctx, hub.URL, true); err == nil {
		t.Fatal("overlapping attempt accepted during approval")
	}
	cancel()
	close(releasePoll)
	<-done
	<-pollReleased
	if len(registry.snapshot().Connections) != 1 {
		t.Fatal("cancelled approval changed registry")
	}
	pollStarted, releasePoll = make(chan struct{}, 1), make(chan struct{})
	id, _, err = pairings.begin(ctx, hub.URL, true)
	requireHubTestOK(t, err)
	result := make(chan string, 1)
	go func() { state, _ := pairings.poll(ctx, id); result <- state }()
	<-pollStarted
	pairings.mu.Lock()
	pairings.attempts[id].pairing.ExpiresAt = time.Now().Add(-time.Second)
	pairings.mu.Unlock()
	close(releasePoll)
	if state := <-result; state != "expired" || len(registry.snapshot().Connections) != 1 {
		t.Fatalf("stale approval committed: %q", state)
	}
	<-pollReleased
	mode.Store(0)
	fail.Store(true)
	id, _, err = pairings.begin(ctx, hub.URL, true)
	requireHubTestOK(t, err)
	if state, err := pairings.poll(ctx, id); err != nil || state != "failed" {
		t.Fatalf("save failure %q %v", state, err)
	}
	if len(registry.snapshot().Connections) != 1 {
		t.Fatal("failed credential write enabled Hub")
	}
	fail.Store(false)
	id, _, err = pairings.begin(ctx, hub.URL, true)
	requireHubTestOK(t, err)
	writer := registry.write
	registry.write = func(string, []byte) (bool, error) { return false, errors.New("write failed") }
	if state, err := pairings.poll(ctx, id); err != nil || state != "failed" {
		t.Fatalf("registry failure %q %v", state, err)
	}
	registry.write = writer
	if len(registry.snapshot().Connections) != 1 {
		t.Fatal("failed registry write enabled Hub")
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/hubs/pairings", strings.NewReader(`{"origin":"`+hub.URL+`","consent":true}`)))
	var started struct {
		ID string `json:"id"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &started) != nil || started.ID == "" {
		t.Fatalf("pairing API start %d: %s", response.Code, response.Body.String())
	}
	id = started.ID
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/hubs/pairings/"+id+"/poll", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"paired_disabled"`) {
		t.Fatalf("pairing API poll %d: %s", response.Code, response.Body.String())
	}
	reopened, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	state := reopened.snapshot()
	if state.Primary != "https://primary.example" || len(state.Connections) != 2 || state.Connections[1].Enabled || state.Connections[1].Origin != hub.URL {
		t.Fatalf("restarted registry %+v", state)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/hubs/enable", strings.NewReader(`{"origin":"`+hub.URL+`","consent":false}`)))
	if response.Code != http.StatusConflict || registry.snapshot().Connections[1].Enabled {
		t.Fatal("enabled without separate consent")
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/hubs/enable", strings.NewReader(`{"origin":"`+hub.URL+`","consent":true}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "started") || !registry.snapshot().Connections[1].Enabled || workers.slot(hub.URL).controller == nil {
		t.Fatal("explicit enable did not start worker")
	}
	client, err := original(hubConnection{Origin: hub.URL}, false)
	requireHubTestOK(t, err)
	credential, err := client.Credentials.Load(ctx)
	if err != nil || credential != "secondary-secret" {
		t.Fatalf("worker credential %q %v", credential, err)
	}
	if data, err := os.ReadFile(primary); err != nil || string(data) != "primary data" {
		t.Fatal("primary changed")
	}
	for name, contents := range primaryFiles {
		data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
		if err != nil || string(data) != contents {
			t.Fatalf("primary data changed: %s", name)
		}
	}
	if _, err := pairings.poll(ctx, id); err == nil {
		t.Fatal("completed attempt reused")
	}
	if _, _, err := pairings.begin(ctx, hub.URL, true); err == nil {
		t.Fatal("existing secondary was overwritten")
	}
	home2 := t.TempDir()
	registry2, err := openHubRegistry(home2, "", nil)
	requireHubTestOK(t, err)
	requireHubTestOK(t, registry2.addPrimary("https://primary.example"))
	workers2, err := newHubWorkerSupervisor(registry2, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	workers2.home = home2
	t.Cleanup(workers2.StopAll)
	workers2.client = func(c hubConnection, primary bool) (*hubclient.Client, error) {
		client, err := workers2.clientForConnection(c, primary)
		if err == nil {
			client.HTTP = hub.Client()
		}
		return client, err
	}
	second := newSecondaryPairings(workers2)
	id, _, err = second.begin(ctx, hub.URL, true)
	requireHubTestOK(t, err)
	if state, err := second.poll(ctx, id); err != nil || state != "paired_disabled" {
		t.Fatalf("pairing %q %v", state, err)
	}
	registry2.write = func(string, []byte) (bool, error) { return false, errors.New("enable write failed") }
	if err := second.enable(ctx, hub.URL, true); err == nil {
		t.Fatal("enable write failure accepted")
	}
	reopened, err = openHubRegistry(home2, "", nil)
	requireHubTestOK(t, err)
	state = reopened.snapshot()
	if len(state.Connections) != 2 || state.Connections[1].Enabled || state.Primary != "https://primary.example" {
		t.Fatalf("failed enable after restart: %+v", state)
	}
	workers2.registry = reopened
	second = newSecondaryPairings(workers2)
	if err := second.enable(ctx, hub.URL, false); err == nil {
		t.Fatal("recovery without consent accepted")
	}
	if err := second.enable(ctx, hub.URL, true); err != nil {
		t.Fatalf("enable retry: %v", err)
	}
	reopened, err = openHubRegistry(home2, "", nil)
	if err != nil || !reopened.snapshot().Connections[1].Enabled {
		t.Fatal("enable retry was not durable")
	}
}

func TestSecondaryPairingAPIRequiresConsent(t *testing.T) {
	mux := http.NewServeMux()
	registerSecondaryPairingRoutes(mux, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/hubs/pairing-disclosure", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "does not authorize") {
		t.Fatalf("disclosure %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/hubs/enable-disclosure", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "starts sync") || !strings.Contains(response.Body.String(), "prompts, commands and source snippets") {
		t.Fatalf("enable disclosure %d %s", response.Code, response.Body.String())
	}
}

func TestSecondaryPairCLIShowsDisclosureBeforeConsent(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input struct {
			Origin            string `json:"origin"`
			Consent           bool   `json:"consent"`
			NewBindingConsent bool   `json:"newBindingConsent"`
		}
		if (r.URL.Path != "/api/hubs/pairings" && r.URL.Path != "/api/hubs/re-pairings") || r.Header.Get("X-Coslash-Token") != "secret" || json.NewDecoder(r.Body).Decode(&input) != nil || input.Origin != "https://secondary.example" || !input.Consent || (r.URL.Path == "/api/hubs/re-pairings" && !input.NewBindingConsent) {
			t.Error("invalid consent request")
		}
		_, _ = fmt.Fprint(w, `{"id":"attempt"}`)
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")
	var out, errOut bytes.Buffer
	if code := runCLI(&out, &errOut, []string{"secondary-pair", "https://secondary.example"}); code != 0 || calls.Load() != 0 || !strings.Contains(out.String(), "does not authorize") {
		t.Fatalf("disclosure exit=%d output=%s error=%s", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runCLI(&out, &errOut, []string{"secondary-pair", "recover", "https://secondary.example"}); code != 0 || calls.Load() != 0 || !strings.Contains(out.String(), "matches the previous one") {
		t.Fatalf("recovery disclosure exit=%d output=%s error=%s", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runCLI(&out, &errOut, []string{"secondary-pair", "enable", "https://secondary.example"}); code != 0 || calls.Load() != 0 || !strings.Contains(out.String(), "starts sync") {
		t.Fatalf("enable disclosure exit=%d output=%s error=%s", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runCLI(&out, &errOut, []string{"secondary-pair", "https://secondary.example", "--consent"}); code != 0 || calls.Load() != 1 || !strings.Contains(out.String(), `"id":"attempt"`) {
		t.Fatalf("consent exit=%d output=%s error=%s", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runCLI(&out, &errOut, []string{"secondary-pair", "recover", "https://secondary.example", "--consent", "--new-binding-consent"}); code != 0 || calls.Load() != 2 || !strings.Contains(out.String(), "another account") {
		t.Fatalf("new-binding consent exit=%d output=%s error=%s", code, out.String(), errOut.String())
	}
}

func TestSecondaryActionsRejectMisplacedConsentFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	registry := workerTestRegistry(t, home, "https://primary.example")
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	t.Cleanup(workers.StopAll)
	workers.client = func(hubConnection, bool) (*hubclient.Client, error) {
		t.Fatal("invalid action contacted a Hub")
		return nil, nil
	}
	mux := http.NewServeMux()
	registerSecondaryPairingRoutes(mux, newSecondaryPairings(workers))
	for _, action := range []string{"pairings", "enable", "disable", "retry"} {
		t.Run(action, func(t *testing.T) {
			body := `{"origin":"https://secondary.example","newBindingConsent":true}`
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/hubs/"+action, strings.NewReader(body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("misplaced consent: %d %s", response.Code, response.Body.String())
			}
		})
	}
	for _, action := range []string{"pairings", "re-pairings", "enable"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/hubs/"+action, strings.NewReader(`{"origin":"https://secondary.example"}`)))
		if response.Code != http.StatusConflict {
			t.Fatalf("%s without consent: %d %s", action, response.Code, response.Body.String())
		}
	}
}
