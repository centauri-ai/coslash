package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestSessionsResponseIsSourceAware(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COSLASH_HOME", t.TempDir())
	manager := remote.NewManager(remote.Options{Cache: remote.NewCache(t.TempDir())})
	request := httptest.NewRequest(http.MethodGet, "/api/sessions?sourceAware=1", nil)
	response := httptest.NewRecorder()
	handleList(response, request, synthesis.NewManager(nil), manager)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body sessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Machines) != 1 || body.Machines[0].SourceID != localSourceID {
		t.Fatalf("machines=%+v", body.Machines)
	}
}

func TestSessionsResponsePreservesLegacyArrayByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COSLASH_HOME", t.TempDir())
	manager := remote.NewManager(remote.Options{Cache: remote.NewCache(t.TempDir())})
	request := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	response := httptest.NewRecorder()
	handleList(response, request, synthesis.NewManager(nil), manager)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body []json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("legacy response is not an array: %v; body=%s", err, response.Body.String())
	}
}

func TestRemoteActionsReturnStructuredUnsupportedError(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	manager := remote.NewManager(remote.Options{})
	handler := routes(synthesis.NewManager(nil), settings.Open(), manager, nil)
	for _, path := range []string{
		"/api/synthesis?source=r_0123456789abcdef&id=x",
		"/api/diff?source=r_0123456789abcdef&id=x",
		"/api/share-preview?source=r_0123456789abcdef&id=x&revision=1",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errCodeRemoteUnsupported) {
			t.Fatalf("%s: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/launch?source=r_0123456789abcdef&id=x", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errCodeRemoteUnsupported) {
		t.Fatalf("launch: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRemoteRetryRequiresConfiguredEnabledSource(t *testing.T) {
	manager := remote.NewManager(remote.Options{})
	request := httptest.NewRequest(http.MethodPost, "/api/remote/retry", nil)
	response := httptest.NewRecorder()
	handleRemoteRetry(response, request, manager)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unconfigured status=%d", response.Code)
	}
	config := &settings.RemoteSettings{
		ID: "r_0123456789abcdef", SSHAlias: "gpu-server", Enabled: false,
	}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handleRemoteRetry(response, request, manager)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errCodeRemoteDisabled) {
		t.Fatalf("disabled status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestParseSourceIDRejectsUntrustedValues(t *testing.T) {
	if source, err := parseSourceID(""); err != nil || source != localSourceID {
		t.Fatalf("default source=%q err=%v", source, err)
	}
	if _, err := parseSourceID("r_bad/../path"); err == nil {
		t.Fatal("expected invalid source")
	}
}
