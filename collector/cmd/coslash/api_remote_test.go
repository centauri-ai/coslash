package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func TestSessionsEnvelopeLocalOnly(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	handler := routes(synthesis.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	request := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
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
	for _, session := range body.Sessions {
		if session.SourceID != localSourceID || session.SourceLabel != localSourceLabel {
			t.Fatalf("session source=%s label=%s", session.SourceID, session.SourceLabel)
		}
		if !session.EligibleForAggregates {
			t.Fatal("local sessions must be eligible")
		}
	}
}

func TestRemoteSinceIndependentOfSince(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	var lastSince atomic.Int64
	lastSince.Store(-1)
	fake := &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
		now := time.Now()
		result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
		if call.RemoteCommand == remote.ProbeCommand() {
			result.Stdout = mustFrame(t, mustMarshalProbe(t))
			return result, nil
		}
		since, requestNow := parseSnapshotArgs(t, call.RemoteCommand)
		lastSince.Store(since)
		result.Stdout = mustFrame(t, mustMarshalView(t, since, requestNow))
		return result, nil
	}}
	mgr := remote.NewManager(remote.Options{Runner: fake, Cache: remote.NewCache(t.TempDir())})
	cfg := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu-server", Enabled: true}
	if err := mgr.ApplySettings(cfg); err != nil {
		t.Fatal(err)
	}
	waitRemoteOK(t, mgr)

	handler := routes(synthesis.NewManager(nil), settings.Open(), mgr, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/sessions?remoteSince=5000", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	var body sessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Machines) != 2 {
		t.Fatalf("machines=%d", len(body.Machines))
	}
	_ = lastSince.Load()
}

func TestUnsupportedRemoteActions(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	handler := routes(synthesis.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	for _, path := range []string{
		"/api/diff?id=x&path=a&source=r_0123456789abcdef",
		"/api/synthesis?id=x&source=r_0123456789abcdef",
		"/api/share-preview?id=x&revision=1&source=r_0123456789abcdef",
		"/api/hub/shares?source=r_0123456789abcdef",
	} {
		method := http.MethodGet
		if strings.HasPrefix(path, "/api/hub/shares") {
			method = http.MethodPost
		}
		request := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf("%s status=%d", path, response.Code)
		}
		var body apiErrorBody
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Code != errCodeRemoteUnsupported {
			t.Fatalf("%s code=%q", path, body.Code)
		}
	}
}

func TestRemoteRetryMissingAndDisabled(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	mgr := remote.NewManager(remote.Options{})
	handler := routes(synthesis.NewManager(nil), settings.Open(), mgr, nil)

	request := httptest.NewRequest(http.MethodPost, "/api/remote/retry", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}

	cfg := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu-server", Enabled: false}
	if err := mgr.ApplySettings(cfg); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d", response.Code)
	}
	var body apiErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != errCodeRemoteDisabled {
		t.Fatalf("code=%q", body.Code)
	}
}

func TestRemoteTestRejectsHostileAlias(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	handler := routes(synthesis.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	request := httptest.NewRequest(http.MethodPost, "/api/remote/test", strings.NewReader(`{"sshAlias":"--BatchMode=no"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestNormalizeRemoteIdentityAliasChange(t *testing.T) {
	previous := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	incoming := &settings.RemoteSettings{ID: "r_fedcba9876543210", SSHAlias: "new-host", Enabled: true}
	out, err := normalizeRemoteIdentity(previous, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == previous.ID || out.ID == incoming.ID {
		t.Fatalf("expected freshly generated id, got %q", out.ID)
	}
	sameAlias := &settings.RemoteSettings{ID: "r_aaaaaaaaaaaaaaaa", SSHAlias: "old-host", Enabled: false}
	out, err = normalizeRemoteIdentity(previous, sameAlias)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != previous.ID {
		t.Fatalf("same alias should keep id, got %q", out.ID)
	}
}

func TestRemoteLaunchCapabilityAndAgentGates(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := settings.Open()
	cfg := settings.Defaults()
	cfg.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu-server", Enabled: true}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	fake := &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
		now := time.Now()
		result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
		if call.RemoteCommand == remote.ProbeCommand() {
			probe := remoteviewv1.Probe{
				SchemaVersion:    remoteviewv1.SchemaVersion,
				CollectorVersion: "dev",
				Capabilities:     []string{remoteviewv1.CapabilityRemoteView},
				LaunchableAgents: []string{},
				HostNowMs:        now.UnixMilli(),
				Host:             remoteviewv1.Host{OS: "linux", Arch: "amd64"},
			}
			payload, err := remoteviewv1.MarshalProbe(probe)
			if err != nil {
				t.Fatal(err)
			}
			result.Stdout = mustFrame(t, payload)
			return result, nil
		}
		view := remoteviewv1.View{
			SchemaVersion:    remoteviewv1.SchemaVersion,
			CollectorVersion: "dev",
			Capabilities:     []string{remoteviewv1.CapabilityRemoteView},
			LaunchableAgents: []string{},
			RequestedSinceMs: 0,
			RequestNowMs:     now.UnixMilli(),
			HostNowMs:        now.UnixMilli(),
			CollectedAtMs:    now.UnixMilli(),
			CoverageSinceMs:  0,
			Host:             remoteviewv1.Host{OS: "linux", Arch: "amd64"},
			Sessions: []remoteviewv1.Session{{
				Agent:              remoteviewv1.AgentCodex,
				SourceSessionID:    "9c73be46-52af-4b1d-9ee7-123456789abc",
				SessionStartedAtMs: now.UnixMilli() - 1000,
				LastActivityAtMs:   now.UnixMilli(),
				Counts:             remoteviewv1.Counts{Turns: 1},
				Usage:              remoteviewv1.Usage{Models: []remoteviewv1.ModelUsage{}, UnpricedModels: []string{}},
				Digest:             []remoteviewv1.Digest{},
				Todos:              []remoteviewv1.Todo{},
				FileEdits:          []remoteviewv1.FileEdit{},
				Commits:            []string{},
				Subagents:          []remoteviewv1.Subagent{},
			}},
		}
		payload, err := remoteviewv1.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		result.Stdout = mustFrame(t, payload)
		return result, nil
	}}
	mgr := remote.NewManager(remote.Options{Runner: fake, Cache: remote.NewCache(t.TempDir())})
	if err := mgr.ApplySettings(cfg.Remote); err != nil {
		t.Fatal(err)
	}
	waitRemoteOK(t, mgr)

	handler := routes(synthesis.NewManager(nil), store, mgr, nil)
	sessionID := remoteViewSessionID(t, mgr)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/launch?source=r_0123456789abcdef&agent=codex&id="+sessionID+"&mode=resume",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != errCodeRemoteUpgradeRequired {
		t.Fatalf("code=%q", body.Code)
	}
}

func TestSaveSettingsAppliesRemoteAndGuidePath(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := settings.Open()
	mgr := remote.NewManager(remote.Options{
		Runner: &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
			now := time.Now()
			result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
			if call.RemoteCommand == remote.ProbeCommand() {
				result.Stdout = mustFrame(t, mustMarshalProbe(t))
				return result, nil
			}
			result.Stdout = mustFrame(t, mustMarshalView(t, 0, now.UnixMilli()))
			return result, nil
		}},
		Cache: remote.NewCache(t.TempDir()),
	})
	handler := routes(synthesis.NewManager(nil), store, mgr, nil)

	payload := `{
  "$schema": "https://raw.githubusercontent.com/centauri-ai/coslash/main/settings.schema.json",
  "version": 1,
  "synthesis": {"enabled": false, "backend": "claude-cli", "model": "claude-haiku-4-5"},
  "launch": {"terminal": "terminal"},
  "remote": {"sshAlias": "gpu-server", "enabled": true}
}`
	request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(payload))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body settingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Options.RemoteInstallationGuidePath != remoteInstallationGuidePath {
		t.Fatalf("guide=%q", body.Options.RemoteInstallationGuidePath)
	}
	if body.Settings.Remote == nil || !settings.ValidRemoteID(body.Settings.Remote.ID) {
		t.Fatalf("remote=%+v", body.Settings.Remote)
	}
	waitRemoteOK(t, mgr)
}

func waitRemoteOK(t *testing.T, mgr *remote.Manager) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h := mgr.DiagnosticsHealth()
		if h.State == remote.StateOK && !h.Refreshing {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("remote not ok: %+v", mgr.DiagnosticsHealth())
}

func remoteViewSessionID(t *testing.T, mgr *remote.Manager) string {
	t.Helper()
	list := mgr.ListView(0)
	if len(list.Sessions) == 0 {
		t.Fatal("no remote sessions")
	}
	return list.Sessions[0].Key.SourceSessionID
}

func mustMarshalProbe(t *testing.T) []byte {
	t.Helper()
	payload, err := remoteviewv1.MarshalProbe(remoteviewv1.Probe{
		SchemaVersion:    remoteviewv1.SchemaVersion,
		CollectorVersion: "dev",
		Capabilities:     []string{remoteviewv1.CapabilityRemoteView, remoteviewv1.CapabilityRemoteLaunch},
		LaunchableAgents: []string{remoteviewv1.AgentClaude, remoteviewv1.AgentCodex},
		HostNowMs:        time.Now().UnixMilli(),
		Host:             remoteviewv1.Host{OS: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustMarshalView(t *testing.T, since, now int64) []byte {
	t.Helper()
	status := "active"
	view := remoteviewv1.View{
		SchemaVersion:    remoteviewv1.SchemaVersion,
		CollectorVersion: "dev",
		Capabilities:     []string{remoteviewv1.CapabilityRemoteView, remoteviewv1.CapabilityRemoteLaunch},
		LaunchableAgents: []string{remoteviewv1.AgentClaude, remoteviewv1.AgentCodex},
		RequestedSinceMs: since,
		RequestNowMs:     now,
		HostNowMs:        now,
		CollectedAtMs:    now,
		CoverageSinceMs:  since,
		Host:             remoteviewv1.Host{OS: "linux", Arch: "amd64"},
		Sessions: []remoteviewv1.Session{{
			Agent:              remoteviewv1.AgentCodex,
			SourceSessionID:    "9c73be46-52af-4b1d-9ee7-123456789abc",
			Status:             &status,
			SessionStartedAtMs: now - 1000,
			LastActivityAtMs:   now,
			Counts:             remoteviewv1.Counts{Turns: 1},
			Usage:              remoteviewv1.Usage{Models: []remoteviewv1.ModelUsage{}, UnpricedModels: []string{}},
			Digest:             []remoteviewv1.Digest{},
			Todos:              []remoteviewv1.Todo{},
			FileEdits:          []remoteviewv1.FileEdit{},
			Commits:            []string{},
			Subagents:          []remoteviewv1.Subagent{},
		}},
	}
	payload, err := remoteviewv1.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	frame, err := remoteviewv1.EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func parseSnapshotArgs(t *testing.T, command string) (since, requestNow int64) {
	t.Helper()
	var gotSince, gotNow int64
	_, err := fmt.Sscanf(
		command,
		`exec "$HOME/.local/bin/coslash" snapshot --since %d --request-now %d --agents claude,codex`,
		&gotSince,
		&gotNow,
	)
	if err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	return gotSince, gotNow
}

func TestAPIRoutesRejectUnsupportedRemoteMethods(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	handler := routes(synthesis.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/remote/test"},
		{method: http.MethodGet, path: "/api/remote/retry"},
		{method: http.MethodPut, path: "/api/remote/retry"},
	} {
		request := httptest.NewRequest(test.method, "http://127.0.0.1"+test.path, bytes.NewReader(nil))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status=%d", test.method, test.path, response.Code)
		}
	}
}
