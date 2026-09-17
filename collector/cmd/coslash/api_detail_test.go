package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const testRemoteSourceID = "r_0123456789abcdef"

func exactDetailSession(body string) session.Session {
	edits := session.NewFileEditSet()
	edits.Add("src/example.go", 1, 1, false)
	edits.Patch("src/example.go", body)
	return session.Session{
		Agent: vendors.AgentCodex, ID: "same-session", WorkingDirectory: "/workspace",
		EditedFileCount: 1, StartedAt: 1000, LastActivityTime: 2000,
		Tokens: map[string]session.ModelTokens{}, Subagents: []session.Subagent{},
		SessionDetails: session.SessionDetails{
			Commands: []string{}, Commits: []string{}, CommitSHAs: []string{}, Todos: []session.Todo{},
			Digest: []session.DigestEntry{}, FileEdits: edits.Edits,
		},
	}
}

func TestExactLocalDetailAndDiffUseRevisionAndChangeMembership(t *testing.T) {
	local := exactDetailSession("@@\n-local old\n+local new\n")
	reader := func(agent, sessionID string) (*session.Session, error) {
		if agent != vendors.AgentCodex || sessionID != local.ID {
			t.Fatalf("local identity = %q/%q", agent, sessionID)
		}
		copy := local
		return &copy, nil
	}

	detailRequest := httptest.NewRequest(http.MethodGet,
		"/api/session-detail?source=local&agent=codex&session=same-session&revision=2000", nil)
	detailResponse := httptest.NewRecorder()
	handleSessionDetail(detailResponse, detailRequest, reader, remote.NewManager(remote.Options{}))
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail sessionDetailResponse
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.SourceID != localSourceID || detail.Agent != vendors.AgentCodex || detail.SessionID != local.ID || detail.Revision != "2000" {
		t.Fatalf("detail identity = %#v", detail)
	}
	if len(detail.Session.FileEdits) != 1 || len(detail.Session.FileEdits[0].ChangeIDs) != 1 {
		t.Fatalf("detail changes = %#v", detail.Session.FileEdits)
	}
	changeID := detail.Session.FileEdits[0].ChangeIDs[0]

	diffRequest := httptest.NewRequest(http.MethodGet,
		"/api/diff?source=local&agent=codex&session=same-session&revision=2000&path=../../secret&change="+changeID, nil)
	diffResponse := httptest.NewRecorder()
	handleExactDiff(diffResponse, diffRequest, reader, remote.NewManager(remote.Options{}))
	if diffResponse.Code != http.StatusOK {
		t.Fatalf("diff status = %d: %s", diffResponse.Code, diffResponse.Body.String())
	}
	var diffBody struct {
		Changes []session.FileChange `json:"changes"`
	}
	if err := json.Unmarshal(diffResponse.Body.Bytes(), &diffBody); err != nil {
		t.Fatal(err)
	}
	if len(diffBody.Changes) != 1 || diffBody.Changes[0].Text != "@@\n-local old\n+local new\n" {
		t.Fatalf("changes = %#v", diffBody.Changes)
	}

	for _, test := range []struct {
		name   string
		target string
		status int
		code   string
	}{
		{name: "stale revision", target: "/api/session-detail?source=local&agent=codex&session=same-session&revision=1999", status: http.StatusConflict, code: errCodeDetailStale},
		{name: "foreign change", target: "/api/diff?source=local&agent=codex&session=same-session&revision=2000&change=change-999999-999999", status: http.StatusNotFound, code: errCodeChangeMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			response := httptest.NewRecorder()
			if test.name == "stale revision" {
				handleSessionDetail(response, request, reader, remote.NewManager(remote.Options{}))
			} else {
				handleExactDiff(response, request, reader, remote.NewManager(remote.Options{}))
			}
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			var body apiErrorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code != test.code {
				t.Fatalf("error = %#v, decode=%v", body, err)
			}
		})
	}
}

func TestSessionDetailReportsMissingAndUnreadableLocalStates(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet,
		"/api/session-detail?source=local&agent=codex&session=same-session&revision=2000", nil)
	for _, test := range []struct {
		name   string
		reader localDetailReader
		status int
		code   string
	}{
		{
			name: "missing", reader: func(string, string) (*session.Session, error) { return nil, nil },
			status: http.StatusNotFound, code: errCodeDetailMissing,
		},
		{
			name: "unreadable", reader: func(string, string) (*session.Session, error) { return nil, errors.New("parse failed") },
			status: http.StatusInternalServerError, code: errCodeDetailCorrupt,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handleSessionDetail(response, request.Clone(request.Context()), test.reader, remote.NewManager(remote.Options{}))
			var body apiErrorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || body.Code != test.code {
				t.Fatalf("response = %d %#v, want %d/%s", response.Code, body, test.status, test.code)
			}
		})
	}
}

func TestRemoteDetailAndDiffRemainReadableFromRestartedOfflineCache(t *testing.T) {
	remoteSession := exactDetailSession("@@\n-remote old\n+remote new\n")
	record, err := fullsessionrecord.FromSession(testRemoteSourceID, remoteSession)
	if err != nil {
		t.Fatal(err)
	}
	family := remotefacts.Family{
		SchemaVersion: remotefacts.SchemaVersion, ParserVersion: "test/1", Vendor: vendors.AgentCodex,
		FamilyID: remoteSession.ID, State: remotefacts.StateComplete,
		Sessions: []remotefacts.Session{{
			ID: remoteSession.ID, StartedAtMs: remoteSession.StartedAt, LastActivityAtMs: remoteSession.LastActivityTime,
			Counts: remotefacts.Counts{}, Usage: []remotefacts.ModelUsage{}, Spawns: []remotefacts.Spawn{}, CommandLabels: []string{},
		}},
		Fingerprints: []remotefacts.Fingerprint{{Key: "file-1", Size: 10, ModifiedAtMs: 1000}},
	}
	if err := remotefacts.Validate(family); err != nil {
		t.Fatal(err)
	}
	cache := remote.NewCache(t.TempDir())
	if err := cache.StoreV2(testRemoteSourceID, remote.CachedSnapshotV2{
		BaselineID: "generation-1", FetchedAtMs: 1000,
		Families: []remote.CachedFamilyV2{{
			Vendor: vendors.AgentCodex, FamilyID: remoteSession.ID, Facts: family,
			Fingerprint: "fingerprint-1", LastSuccessAtMs: 1000,
		}},
		FullRecords: []remoteprotocol.FullRecord{{FamilyID: remoteSession.ID, Record: record}},
	}); err != nil {
		t.Fatal(err)
	}
	manager := remote.NewManager(remote.Options{Cache: cache})
	t.Cleanup(manager.Shutdown)
	if err := manager.ApplySettings(&settings.RemoteSettings{
		ID: testRemoteSourceID, SSHAlias: "offline-host", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	localCalls := 0
	localReader := func(string, string) (*session.Session, error) {
		localCalls++
		return nil, errors.New("remote request reached local parser")
	}
	detailTarget := "/api/session-detail?source=" + testRemoteSourceID + "&agent=codex&session=" + remoteSession.ID + "&revision=" + record.RevisionID
	detailResponse := httptest.NewRecorder()
	handleSessionDetail(detailResponse, httptest.NewRequest(http.MethodGet, detailTarget, nil), localReader, manager)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail sessionDetailResponse
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.CachedOffline || localCalls != 0 || len(detail.Session.FileEdits) != 1 || len(detail.Session.FileEdits[0].ChangeIDs) != 1 {
		t.Fatalf("cached detail = %#v, local calls=%d", detail, localCalls)
	}
	localParityReader := func(agent, sessionID string) (*session.Session, error) {
		if agent != remoteSession.Agent || sessionID != remoteSession.ID {
			t.Fatalf("local parity identity = %q/%q", agent, sessionID)
		}
		copy := remoteSession
		return &copy, nil
	}
	localDetailResponse := httptest.NewRecorder()
	handleSessionDetail(
		localDetailResponse,
		httptest.NewRequest(http.MethodGet, "/api/session-detail?source=local&agent=codex&session=same-session&revision=2000", nil),
		localParityReader,
		manager,
	)
	if localDetailResponse.Code != http.StatusOK {
		t.Fatalf("local parity detail status = %d: %s", localDetailResponse.Code, localDetailResponse.Body.String())
	}
	var localDetail sessionDetailResponse
	if err := json.Unmarshal(localDetailResponse.Body.Bytes(), &localDetail); err != nil {
		t.Fatal(err)
	}
	localDetailJSON, _ := json.Marshal(localDetail.Session)
	remoteDetailJSON, _ := json.Marshal(detail.Session)
	if string(localDetailJSON) != string(remoteDetailJSON) {
		t.Fatalf("local/remote detail differ\nlocal=%s\nremote=%s", localDetailJSON, remoteDetailJSON)
	}

	changeID := detail.Session.FileEdits[0].ChangeIDs[0]
	diffTarget := "/api/diff?source=" + testRemoteSourceID + "&agent=codex&session=" + remoteSession.ID + "&revision=" + record.RevisionID + "&change=" + changeID
	diffResponse := httptest.NewRecorder()
	handleExactDiff(diffResponse, httptest.NewRequest(http.MethodGet, diffTarget, nil), localReader, manager)
	if diffResponse.Code != http.StatusOK || !strings.Contains(diffResponse.Body.String(), "remote new") {
		t.Fatalf("diff status = %d: %s", diffResponse.Code, diffResponse.Body.String())
	}
	localDiffResponse := httptest.NewRecorder()
	handleExactDiff(
		localDiffResponse,
		httptest.NewRequest(http.MethodGet, "/api/diff?source=local&agent=codex&session=same-session&revision=2000&change="+changeID, nil),
		localParityReader,
		manager,
	)
	if localDiffResponse.Code != http.StatusOK || localDiffResponse.Body.String() != diffResponse.Body.String() {
		t.Fatalf("local/remote diff differ\nlocal=%s\nremote=%s", localDiffResponse.Body.String(), diffResponse.Body.String())
	}

	staleTarget := "/api/session-detail?source=" + testRemoteSourceID + "&agent=codex&session=" + remoteSession.ID + "&revision=" + strings.Repeat("a", 64)
	staleResponse := httptest.NewRecorder()
	handleSessionDetail(staleResponse, httptest.NewRequest(http.MethodGet, staleTarget, nil), localReader, manager)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale status = %d: %s", staleResponse.Code, staleResponse.Body.String())
	}
}
