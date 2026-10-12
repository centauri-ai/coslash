package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestSyncStatusFailureJSONAndRetryInterval(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	previousLocation := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previousLocation })
	queue, err := syncv4.Open("")
	if err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 10, 12, 8, 12, 0, 0, time.UTC)
	if _, err := queue.RecordHubContact(since, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}); err != nil {
		t.Fatal(err)
	}
	controller := &syncController{
		queue: queue, connected: true,
		client: &hubclient.Client{BaseURL: &url.URL{Scheme: "https", Host: "hub.example.test"}},
	}
	if status := controller.statusAt(since.Add(queue.NextCheckInDelay())); status.HubFailureActive {
		t.Fatal("failure became active at exactly one retry interval")
	}
	status := controller.statusAt(since.Add(queue.NextCheckInDelay() + time.Nanosecond))
	if !status.HubFailureActive || status.LastFailure == nil || status.LastFailure.Cause != hubclient.FailureConnect {
		t.Fatalf("stale failure status = %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"state":"connected_idle","hubOrigin":"https://hub.example.test","sessions":{},"sessionFailures":{},"lastFailure":{"host":"hub.example.test","cause":"connect","sinceMs":1791792720000,"attempts":1,"sentence":"Hub: can't connect to hub.example.test since 08:12 (1 attempts) — Connection timed out. Check your network, VPN or proxy.","rawError":"dial tcp: i/o timeout"},"hubFailureActive":true}`
	if string(encoded) != wantJSON {
		t.Fatalf("sync status JSON = %s, want %s", encoded, wantJSON)
	}
}

func TestSyncStatusRouteIsTokenGuardedAndContentFree(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	onboardings := newOnboardingManager("1.2.3")
	defer onboardings.Close()
	handler := httpsec.Guard{Addr: "127.0.0.1:8787", Token: "local-secret"}.Wrap(routesWithOnboarding(
		synthesis.NewManager(nil, nil), reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil,
		onboardings, serverServices{syncController: &syncController{}},
	))

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/sync/status", nil)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status request = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/sync/status", nil)
	request.Header.Set("X-Coslash-Token", "local-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body map[string]json.RawMessage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("authorized status request = %d %s", response.Code, response.Body.String())
	}
	if len(body) != 4 || body["state"] == nil || body["hubOrigin"] == nil || body["sessions"] == nil || body["sessionFailures"] == nil {
		t.Fatalf("status response keys = %v", body)
	}
}

func TestSyncRetryRouteIsTokenGuardedAndStartsImmediatePass(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	onboardings := newOnboardingManager("1.2.3")
	defer onboardings.Close()
	wake := make(chan struct{}, 1)
	controller := &syncController{connected: true, wake: wake}
	handler := httpsec.Guard{Addr: "127.0.0.1:8787", Token: "local-secret"}.Wrap(routesWithOnboarding(
		synthesis.NewManager(nil, nil), reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil,
		onboardings, serverServices{syncController: controller},
	))

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/sync/retry", nil)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized retry status = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/sync/retry", nil)
	request.Header.Set("X-Coslash-Token", "local-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("authorized retry status = %d %s", response.Code, response.Body.String())
	}
	select {
	case <-wake:
	default:
		t.Fatal("retry request did not wake the sync loop")
	}
}
