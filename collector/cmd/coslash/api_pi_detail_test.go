package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func TestLocalPiDetailAcceptsOpaqueIdentityButRemoteRefuses(t *testing.T) {
	for _, id := range []string{"normal", "../SDK custom/ identity"} {
		value := exactDetailSession("package main\n")
		value.Agent, value.ID = "pi", id
		reader := func(agent, sessionID string) (*session.Session, error) {
			if agent != "pi" || sessionID != id {
				t.Fatalf("identity changed: %s/%s", agent, sessionID)
			}
			return &value, nil
		}
		query := url.Values{"source": {"local"}, "agent": {"pi"}, "session": {id}, "revision": {"latest"}}
		response := httptest.NewRecorder()
		handleSessionDetail(response, httptest.NewRequest(http.MethodGet, "/api/session-detail?"+query.Encode(), nil), reader, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("Pi detail = %d: %s", response.Code, response.Body.String())
		}
		query.Set("revision", mustLocalDetailRevision(t, value))
		query.Add("change", session.WithLocalChangeIDs(value).FileEdits[0].ChangeIDs[0])
		response = httptest.NewRecorder()
		handleExactDiff(response, httptest.NewRequest(http.MethodGet, "/api/diff?"+query.Encode(), nil), reader, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("Pi diff = %d: %s", response.Code, response.Body.String())
		}
		query.Set("source", testRemoteSourceID)
		response = httptest.NewRecorder()
		handleSessionDetail(response, httptest.NewRequest(http.MethodGet, "/api/session-detail?"+query.Encode(), nil), reader, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("remote Pi accepted: %d", response.Code)
		}
	}
	for _, id := range []string{"", "bad\x00identity", string([]byte{0xff})} {
		if validLocalPiIdentifier(id) {
			t.Fatalf("unsafe Pi identity accepted: %q", id)
		}
	}
}

func TestPiHTTPSynthesisTracksRuntimeBranchWithoutTranscriptAppend(t *testing.T) {
	if !vendors.PiSupported() {
		t.Skip("Pi collection is supported only on macOS")
	}
	agentDir, stateDir := t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_HOME", stateDir)
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	sessionDir := filepath.Join(agentDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatal(err)
	}
	id := "SDK/ custom identity"
	transcript := filepath.Join(sessionDir, "branch.jsonl")
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": t.TempDir(), "timestamp": "2026-01-01T00:00:00Z"})
	records := string(header) + "\n" + `{"type":"message","id":"root","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"shared request"}}` + "\n"
	for _, leaf := range []string{"a", "b"} {
		records += fmt.Sprintf(`{"type":"message","id":%q,"parentId":"root","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","provider":"anthropic","model":"claude-sonnet-4","content":[{"type":"text","text":%q}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":1}}}}`, leaf, "branch "+leaf) + "\n"
	}
	if err := os.WriteFile(transcript, []byte(records), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(transcript)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := pi.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(stateDir, "pi-runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	manager := synthesis.NewManager(nil)
	cache := synthesis.NewCache()
	var previous int64
	for sequence, leaf := range []string{"a", "b"} {
		record := pi.RuntimeRecord{Version: 1, RuntimeID: "http-test-owner", PID: os.Getpid(), ProcessStartIdentity: identity, StartedAtMs: 1, SessionID: id, TranscriptPath: transcript, LeafID: &leaf, WorkState: "idle", Entrypoint: "pi-rpc", Sequence: int64(sequence + 1), UpdatedAtMs: 2}
		payload, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(runtimeDir, record.RuntimeID+".json"), payload, 0600); err != nil {
			t.Fatal(err)
		}
		detail := httptest.NewRecorder()
		query := url.Values{"source": {"local"}, "agent": {"pi"}, "session": {id}, "revision": {"latest"}}
		handleSessionDetail(detail, httptest.NewRequest(http.MethodGet, "/api/session-detail?"+query.Encode(), nil), collector.GetSessionDetail, nil)
		if detail.Code != http.StatusOK {
			t.Fatalf("detail = %d: %s", detail.Code, detail.Body.String())
		}
		var decoded sessionDetailResponse
		if err := json.Unmarshal(detail.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Session.Entrypoint == nil || *decoded.Session.Entrypoint != "pi-rpc" {
			t.Fatalf("runtime modality lost: %#v", decoded.Session.Entrypoint)
		}
		if decoded.Session.Summary == nil || *decoded.Session.Summary != "branch "+leaf {
			t.Fatalf("runtime leaf not reflected in detail: %#v", decoded.Session.Summary)
		}
		record.Entrypoint, record.Sequence = "pi-sdk", record.Sequence+10
		payload, _ = json.Marshal(record)
		if err := os.WriteFile(filepath.Join(runtimeDir, record.RuntimeID+".json"), payload, 0600); err != nil {
			t.Fatal(err)
		}
		refreshed := httptest.NewRecorder()
		handleSessionDetail(refreshed, httptest.NewRequest(http.MethodGet, "/api/session-detail?"+query.Encode(), nil), collector.GetSessionDetail, nil)
		var changed sessionDetailResponse
		if err := json.Unmarshal(refreshed.Body.Bytes(), &changed); err != nil {
			t.Fatal(err)
		}
		if changed.Session.Entrypoint == nil || *changed.Session.Entrypoint != "pi-sdk" || changed.Revision != decoded.Revision || changed.SynthesisRevision != decoded.SynthesisRevision {
			t.Fatalf("modality/revision changed: first=%#v/%s/%d next=%#v/%s/%d", decoded.Session.Entrypoint, decoded.Revision, decoded.SynthesisRevision, changed.Session.Entrypoint, changed.Revision, changed.SynthesisRevision)
		}
		revision := decoded.SynthesisRevision
		if revision <= 0 || revision > (1<<53)-1 || revision == previous {
			t.Fatalf("unsafe/stale synthesis revision: %d", revision)
		}
		if sequence > 0 {
			response := httptest.NewRecorder()
			handleSynthesis(response, "pi", id, manager)
			var result struct {
				Synthesis *session.SessionSynthesis
				Revision  int64
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Synthesis != nil || result.Revision != revision {
				t.Fatalf("previous branch synthesis reused: %#v", result)
			}
		}
		expected := "summary " + leaf
		if err := cache.Store("pi", id, synthesis.Record{Revision: revision, Synthesis: session.SessionSynthesis{Outcome: expected}}); err != nil {
			t.Fatal(err)
		}
		// A new manager represents a server restart and reads the persisted opaque-ID cache.
		manager = synthesis.NewManager(nil)
		response := httptest.NewRecorder()
		handleSynthesis(response, "pi", id, manager)
		if response.Code != http.StatusOK {
			t.Fatalf("synthesis = %d", response.Code)
		}
		var result struct {
			Synthesis *session.SessionSynthesis
			Revision  int64
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Revision != revision || result.Synthesis == nil || result.Synthesis.Outcome != expected {
			t.Fatalf("detail/synthesis mismatch: %#v, detail %d", result, revision)
		}
		previous = revision
	}
	after, err := os.Stat(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("runtime branch test modified transcript")
	}
}

func TestPiProviderErrorSurvivesExactLocalDetail(t *testing.T) {
	if !vendors.PiSupported() {
		t.Skip("Pi collection is supported only on macOS")
	}
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	root := filepath.Join(agentDir, "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	records := `{"type":"session","version":3,"id":"provider-error","cwd":"/tmp","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","id":"failed","parentId":null,"message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"Refresh SSO credentials"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "failure.jsonl"), []byte(records), 0600); err != nil {
		t.Fatal(err)
	}
	query := url.Values{"source": {"local"}, "agent": {"pi"}, "session": {"provider-error"}, "revision": {"latest"}}
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		handleSessionDetail(response, httptest.NewRequest(http.MethodGet, "/api/session-detail?"+query.Encode(), nil), collector.GetSessionDetail, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("Pi detail = %d: %s", response.Code, response.Body.String())
		}
		var decoded sessionDetailResponse
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Session.AgentError == nil || *decoded.Session.AgentError != "Refresh SSO credentials" {
			t.Fatalf("local diagnostic lost: %#v", decoded.Session.AgentError)
		}
	}
}

func TestPiHandoffChoicesMatchSupportedPlatform(t *testing.T) {
	found := false
	for _, option := range launch.HandoffTargetOptions(context.Background()) {
		found = found || option.Agent == vendors.AgentPi
	}
	if found != vendors.PiSupported() {
		t.Fatalf("Pi handoff choice %t differs from platform support", found)
	}
	if !vendors.PiSupported() && launch.PiAvailable() {
		t.Fatal("Pi launcher available on unsupported platform")
	}
}
