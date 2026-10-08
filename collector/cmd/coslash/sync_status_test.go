package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestSyncStatusRouteIsTokenGuardedAndContentFree(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	onboardings := newOnboardingManager("1.2.3")
	defer onboardings.Close()
	handler := httpsec.Guard{Addr: "127.0.0.1:8787", Token: "local-secret"}.Wrap(routesWithOnboarding(
		synthesis.NewManager(nil), reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil,
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
	if len(body) != 3 || body["state"] == nil || body["hubOrigin"] == nil || body["sessions"] == nil {
		t.Fatalf("status response keys = %v", body)
	}
}
