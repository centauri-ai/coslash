package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestListenBindsIPv4Loopback(t *testing.T) {
	listener, err := listen(0)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("sandbox does not permit opening a loopback listener: %v", err)
		}
		t.Fatal(err)
	}
	defer listener.Close()

	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("listener host = %q, want 127.0.0.1", host)
	}
}

func TestAPIRoutesRejectUnsupportedMethods(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	handler := routes(synthesis.NewManager(nil), reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/sessions"},
		{method: http.MethodPost, path: "/api/session-detail"},
		{method: http.MethodPost, path: "/api/synthesis"},
		{method: http.MethodPost, path: "/api/diff"},
		{method: http.MethodGet, path: "/api/launch"},
		{method: http.MethodGet, path: "/api/reviews"},
		{method: http.MethodPost, path: "/api/handoff"},
		{method: http.MethodGet, path: "/api/send"},
		{method: http.MethodPost, path: "/api/diagnostics"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://127.0.0.1"+test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestRemoteHandoffTransferFailurePreventsTerminalLaunch(t *testing.T) {
	originalStage := stageRemoteHandoff
	originalLaunch := launchRemoteTerminal
	t.Cleanup(func() {
		stageRemoteHandoff = originalStage
		launchRemoteTerminal = originalLaunch
	})
	wantErr := errors.New("transfer failed")
	stageRemoteHandoff = func(context.Context, string, []byte) (string, error) {
		return "", wantErr
	}
	launched := false
	launchRemoteTerminal = func(string, string, string, string, string, string, string) error {
		launched = true
		return nil
	}

	err := openRemoteTerminalWithHandoff(
		context.Background(), "terminal", "agent-box", "codex", "/workspace", "session-id", "new", "handoff",
	)
	if !errors.Is(err, errRemoteHandoffTransfer) {
		t.Fatalf("error = %v, want %v", err, errRemoteHandoffTransfer)
	}
	if launched {
		t.Fatal("terminal launched after handoff transfer failed")
	}
}

func TestRemoteTerminalFailureRemovesStagedHandoff(t *testing.T) {
	originalStage := stageRemoteHandoff
	originalRemove := removeRemoteHandoff
	originalLaunch := launchRemoteTerminal
	t.Cleanup(func() {
		stageRemoteHandoff = originalStage
		removeRemoteHandoff = originalRemove
		launchRemoteTerminal = originalLaunch
	})
	const name = "0123456789abcdef0123456789abcdef"
	stageRemoteHandoff = func(_ context.Context, alias string, contents []byte) (string, error) {
		if alias != "agent-box" || !bytes.Contains(contents, []byte("private handoff")) {
			t.Fatalf("stage input = %q, %q", alias, contents)
		}
		return name, nil
	}
	wantErr := errors.New("terminal failed")
	launchRemoteTerminal = func(string, string, string, string, string, string, string) error {
		return wantErr
	}
	removed := ""
	var cleanupContextErr error
	removeRemoteHandoff = func(ctx context.Context, alias, handoffName string) error {
		if alias != "agent-box" {
			t.Fatalf("remove alias = %q", alias)
		}
		cleanupContextErr = ctx.Err()
		removed = handoffName
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := openRemoteTerminalWithHandoff(
		ctx, "terminal", "agent-box", "codex", "/workspace", "session-id", "new", "private handoff",
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if removed != name {
		t.Fatalf("removed handoff = %q, want %q", removed, name)
	}
	if cleanupContextErr != nil {
		t.Fatalf("cleanup inherited canceled request context: %v", cleanupContextErr)
	}
}

func TestReadHandoffKeepsThe64KiBBoundary(t *testing.T) {
	special := []byte("🦖\n'\"\\$();&|<>\n")
	maximum := append(special, bytes.Repeat([]byte("<"), launch.MaxHandoffBytes-len(special))...)
	request := httptest.NewRequest(http.MethodPost, "/api/launch", bytes.NewReader(maximum))
	got, err := readHandoff(httptest.NewRecorder(), request)
	if err != nil || !bytes.Equal([]byte(got), maximum) {
		t.Fatalf("maximum handoff changed: got %d bytes, err = %v", len(got), err)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/launch", bytes.NewReader(append(maximum, '<')))
	if _, err := readHandoff(httptest.NewRecorder(), request); err == nil {
		t.Fatal("readHandoff accepted 65,537 bytes")
	}
}

func TestHandleReviewLaunchesSelectedInstalledReviewer(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	name := "Fix checkout race"
	var gotReviewer, gotCWD, gotName, gotPrompt string
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/reviews?source=local&agent=codex&id=origin-id&reviewer=codex", nil)
	response := httptest.NewRecorder()
	handleReview(
		response,
		request,
		settings.Open(),
		func(agent, id string) (*session.Session, error) {
			if agent != "codex" || id != "origin-id" {
				t.Fatalf("session identity = %q, %q", agent, id)
			}
			return &session.Session{Agent: agent, ID: id, Name: &name, WorkingDirectory: "/repo"}, nil
		},
		func(reviewer string) bool { return reviewer == "codex" },
		func(id string, launch reviewpkg.Launch) bool {
			if id != "codex:origin-id" {
				t.Fatalf("origin id = %q", id)
			}
			gotReviewer, gotCWD, gotName, gotPrompt = launch.Reviewer, launch.WorkingDirectory, launch.Name, launch.Prompt
			return true
		},
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if gotReviewer != "codex" || gotCWD != "/repo" || gotName != "Review — Fix checkout race (origin-i)" {
		t.Fatalf("launch = reviewer %q, cwd %q, name %q", gotReviewer, gotCWD, gotName)
	}
	if !strings.HasPrefix(gotPrompt, gotName+"\n") {
		t.Fatalf("prompt = %q", gotPrompt)
	}
}

func TestHandleReviewResolvesSessionWhenOriginAgentIsOmitted(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/reviews?source=local&id=origin-id&reviewer=claude", nil)
	response := httptest.NewRecorder()
	handleReview(
		response,
		request,
		settings.Open(),
		func(agent, id string) (*session.Session, error) {
			if agent != "" || id != "origin-id" {
				t.Fatalf("session identity = %q, %q", agent, id)
			}
			return &session.Session{Agent: "codex", ID: id, WorkingDirectory: "/repo"}, nil
		},
		func(reviewer string) bool { return reviewer == "claude" },
		func(id string, launch reviewpkg.Launch) bool {
			if id != "codex:origin-id" || launch.Reviewer != "claude" {
				t.Fatalf("review = %q, %#v", id, launch)
			}
			return true
		},
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestHandleReviewRejectsUnsupportedRequests(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	tests := []struct {
		name string
		url  string
		code int
	}{
		{name: "remote", url: "/api/reviews?source=remote&id=origin&reviewer=codex", code: http.StatusBadRequest},
		{name: "reviewer", url: "/api/reviews?source=local&id=origin&reviewer=cursor", code: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.url, nil)
			response := httptest.NewRecorder()
			handleReview(
				response,
				request,
				settings.Open(),
				func(string, string) (*session.Session, error) {
					return &session.Session{ID: "origin", WorkingDirectory: "/repo"}, nil
				},
				func(string) bool { return false },
				func(string, reviewpkg.Launch) bool { t.Fatal("unexpected launch"); return false },
			)
			if response.Code != test.code {
				t.Fatalf("status = %d, want %d", response.Code, test.code)
			}
		})
	}
}

func TestHandleReviewRejectsDuplicateStart(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/reviews?source=local&agent=codex&id=origin&reviewer=codex", nil)
	response := httptest.NewRecorder()
	handleReview(
		response,
		request,
		settings.Open(),
		func(string, string) (*session.Session, error) {
			return &session.Session{Agent: "codex", ID: "origin", WorkingDirectory: "/repo"}, nil
		},
		func(string) bool { return true },
		func(string, reviewpkg.Launch) bool { return false },
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
}

func TestHandleReviewReturnsFixedSettingsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/reviews", nil)
	response := httptest.NewRecorder()
	handleReview(response, request, settings.Open(), nil, nil, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if got := response.Body.String(); got != "settings are invalid; open Settings to repair them\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestLocalMachineFactOmitsRemoteOnlyEnums(t *testing.T) {
	encoded, err := json.Marshal(localMachineFact())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if _, present := document["transport"]; present {
		t.Fatalf("local fact serialized empty transport: %s", encoded)
	}
	if _, present := document["helperProbeState"]; present {
		t.Fatalf("local fact serialized empty helper probe state: %s", encoded)
	}
}

func TestHelperSetupRequiresExactlyOneConsent(t *testing.T) {
	manager := remote.NewManager(remote.Options{})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"sshAlias":"agent-box","install":false,"upgrade":false}`, `{"sshAlias":"agent-box","install":true,"upgrade":true}`} {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/remote/helper/setup", bytes.NewBufferString(body))
		response := httptest.NewRecorder()
		handleRemoteHelperSetup(response, request, manager)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want %d", body, response.Code, http.StatusBadRequest)
		}
	}
}

func TestBoardRemoteSessionSerializesCollectionsAsArrays(t *testing.T) {
	encoded, err := json.Marshal(boardRemoteSession(remote.IndexedSession{
		Key:     remote.SessionKey{SourceID: "r_0123456789abcdef"},
		Session: &session.Session{Agent: "codex", ID: "empty"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"unpricedModels", "subagents", "commands", "commits", "todos", "digest", "fileEdits"} {
		if value, ok := body[field].([]any); !ok || value == nil {
			t.Fatalf("%s = %#v, want JSON array", field, body[field])
		}
	}
	if value, ok := body["tokens"].(map[string]any); !ok || value == nil {
		t.Fatalf("tokens = %#v, want JSON object", body["tokens"])
	}
}

func TestBoardSessionLibraryNormalizesIdentityEligibilityAndSSHLabel(t *testing.T) {
	private := true
	remoteSession := boardRemoteSession(remote.IndexedSession{
		Key:                   remote.SessionKey{SourceID: "r_0123456789abcdef"},
		SourceLabel:           "person@private-host",
		EligibleForAggregates: true,
		Session: &session.Session{
			Agent: "codex", ID: "session-1", LastActivityTime: 42,
			RepositoryLocalOnly: private,
		},
	})
	if remoteSession.SourceClass != "ssh_workspace" || remoteSession.SourceLabel != sshSourceLabel {
		t.Fatalf("source presentation = %q/%q, want ssh_workspace/%q", remoteSession.SourceClass, remoteSession.SourceLabel, sshSourceLabel)
	}
	if remoteSession.LogicalSessionID != "r_0123456789abcdef:codex:session-1" || remoteSession.Revision != 42 {
		t.Fatalf("logical identity = %q@%d", remoteSession.LogicalSessionID, remoteSession.Revision)
	}
	if remoteSession.Privacy != "private" || remoteSession.ShareEligibility != "private" {
		t.Fatalf("private remote eligibility = %q/%q", remoteSession.Privacy, remoteSession.ShareEligibility)
	}

	running := "busy"
	localSession := boardLocalSession(&session.Session{
		Agent: "claude", ID: "session-2", Status: &running, LastActivityTime: 7,
		DetailRevision: "retained-revision",
	})
	if localSession.SourceClass != "local" || localSession.Completion != "running" || localSession.ShareEligibility != "running" {
		t.Fatalf("local normalization = %#v", localSession)
	}
	if localSession.DetailRevision != "retained-revision" {
		t.Fatalf("local detail revision = %q, want retained revision", localSession.DetailRevision)
	}
}

func TestCompletedSessionWithUnknownCostIsNotShareable(t *testing.T) {
	localSession := boardLocalSession(&session.Session{Agent: "codex", ID: "session", LastActivityTime: 1})
	if localSession.ShareEligibility != "failed" {
		t.Fatalf("share eligibility = %q, want failed", localSession.ShareEligibility)
	}
}

func TestBoardRemoteSessionDoesNotSerializeRemoteOperationalOrContentFields(t *testing.T) {
	secret := "SECRET-REMOTE-CONTENT"
	repository := "centauri/coslash"
	encoded, err := json.Marshal(boardRemoteSession(remote.IndexedSession{
		Key:                   remote.SessionKey{SourceID: "r_0123456789abcdef"},
		SourceLabel:           "dev@private-host",
		EligibleForAggregates: true,
		Session: &session.Session{
			Agent: "codex", ID: "session-1", WorkingDirectory: "/private/workspace",
			Repository: &repository, Summary: &secret,
			SessionDetails: session.SessionDetails{
				FirstPrompt: &secret, Commands: []string{secret},
				Todos:     []session.Todo{{Text: secret}},
				FileEdits: []session.FileEdit{{Path: "/private/file"}},
			},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"dev@private-host", "/private/workspace", "/private/file", secret} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("remote board response exposed %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(string(encoded), repository) {
		t.Fatalf("remote board response removed canonical repository: %s", encoded)
	}
}

func TestHelperSetupFailureIsNotReportedAsGreenMachineSuccess(t *testing.T) {
	manager := remote.NewManager(remote.Options{})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/remote/helper/setup", bytes.NewBufferString(`{"sshAlias":"agent-box","install":true,"upgrade":false}`))
	response := httptest.NewRecorder()
	handleRemoteHelperSetup(response, request, manager)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	var body helperSetupResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Outcome != "sftp_fallback" || body.Error == "" || body.Machine.State != remote.StateLimited || body.Machine.Complete {
		t.Fatalf("failed setup response = %#v", body)
	}
}

func TestHelperSetupRejectsUnsavedAlias(t *testing.T) {
	manager := remote.NewManager(remote.Options{})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "saved-host", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/remote/helper/setup", bytes.NewBufferString(`{"sshAlias":"tested-draft","install":true,"upgrade":false}`))
	response := httptest.NewRecorder()
	handleRemoteHelperSetup(response, request, manager)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	var body apiErrorBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "remote_alias_mismatch" {
		t.Fatalf("error = %#v", body)
	}
}

func TestHelperSetupOutcomeUsesOperationSuccessNotBoardCoverage(t *testing.T) {
	health := remote.Health{
		State: remote.StateOK, Complete: false,
		Helper: &remote.HelperStatus{State: remote.LifecycleReady, Compatible: true},
	}
	outcome, _, succeeded := helperSetupOutcome(health, true)
	if !succeeded || outcome != "installed_and_tested" {
		t.Fatalf("outcome = %q, succeeded=%v", outcome, succeeded)
	}
}

func TestSettingsSaveCommitsOwnershipReleaseOnlyWithAliasReplacement(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := settings.Open()
	cache := remote.NewCache(t.TempDir())
	manager := remote.NewManager(remote.Options{Cache: cache})
	previous := settings.Defaults()
	previous.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	if err := store.Save(previous); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreHelperVersion(previous.Remote.ID, "v1", previous.Remote.SSHAlias); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(previous.Remote); err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Remote = &settings.RemoteSettings{ID: previous.Remote.ID, SSHAlias: "new-host", Enabled: true}
	body, err := json.Marshal(map[string]any{"settings": next, "remoteOwnershipAction": "release"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1/api/settings", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handleSaveSettings(response, request, store, synthesis.NewManager(nil), manager)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := store.State().Config.Remote; got == nil || got.SSHAlias != "new-host" {
		t.Fatalf("saved remote = %#v", got)
	}
	if _, owned, err := cache.LoadHelperOwnership(previous.Remote.ID); err != nil || owned {
		t.Fatalf("ownership was not released with replacement: owned=%v err=%v", owned, err)
	}
}

func TestSettingsSaveRestoresOldSettingsWhenOwnershipActionFails(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := settings.Open()
	cache := remote.NewCache(t.TempDir())
	manager := remote.NewManager(remote.Options{Cache: cache})
	previous := settings.Defaults()
	previous.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	if err := store.Save(previous); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreHelperVersion(previous.Remote.ID, "v1", previous.Remote.SSHAlias); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(previous.Remote); err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Remote = nil
	body, err := json.Marshal(map[string]any{"settings": next, "remoteOwnershipAction": "uninstall"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1/api/settings", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handleSaveSettings(response, request, store, synthesis.NewManager(nil), manager)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := store.State().Config.Remote; got == nil || got.SSHAlias != previous.Remote.SSHAlias {
		t.Fatalf("settings were not restored: %#v", got)
	}
	if _, owned, err := cache.LoadHelperOwnership(previous.Remote.ID); err != nil || !owned {
		t.Fatalf("failed uninstall lost ownership: owned=%v err=%v", owned, err)
	}
}

func TestSettingsSaveRemovesHostWithoutHelperOwnership(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := settings.Open()
	manager := remote.NewManager(remote.Options{Cache: remote.NewCache(t.TempDir())})
	previous := settings.Defaults()
	previous.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	if err := store.Save(previous); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(previous.Remote); err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Remote = nil
	body, err := json.Marshal(map[string]any{"settings": next, "remoteOwnershipAction": "release"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1/api/settings", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handleSaveSettings(response, request, store, synthesis.NewManager(nil), manager)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if store.State().Config.Remote != nil {
		t.Fatalf("remote was not removed: %#v", store.State().Config.Remote)
	}
}

func TestSettingsSaveCanExplicitlyRecoverCorruptOwnershipByRemovingHost(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := settings.Open()
	cache := remote.NewCache(t.TempDir())
	manager := remote.NewManager(remote.Options{Cache: cache})
	previous := settings.Defaults()
	previous.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	if err := store.Save(previous); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache.Root, "remotes", previous.Remote.ID, "helper.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":"?"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(previous.Remote); err != nil {
		t.Fatalf("corrupt ownership must retain a displayable host: %v", err)
	}
	if health := manager.DiagnosticsHealth(); !health.HelperOwnershipCorrupt || health.Helper == nil {
		t.Fatalf("corrupt ownership health = %#v", health)
	}
	next := previous
	next.Remote = nil
	body, err := json.Marshal(map[string]any{"settings": next, "remoteOwnershipAction": "release"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1/api/settings", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handleSaveSettings(response, request, store, synthesis.NewManager(nil), manager)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if store.State().Config.Remote != nil {
		t.Fatalf("recovery did not remove host: %#v", store.State().Config.Remote)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt ownership record remains: %v", err)
	}
}

func TestSettingsSaveEnvelopeRejectsUnknownFields(t *testing.T) {
	config := settings.Defaults()
	body, err := json.Marshal(map[string]any{"settings": config, "unexpected": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeSettingsSave(body); err == nil {
		t.Fatal("unknown envelope field was accepted")
	}
}

func TestServerWrapsRoutesWithGuard(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	server := newServer(
		httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"},
		synthesis.NewManager(nil),
		reviewpkg.NewManager(nil),
		settings.Open(),
		remote.NewManager(remote.Options{}),
		nil,
	)
	request := httptest.NewRequest(http.MethodGet, "http://evil.example:8787/", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestTokenLifecycle(t *testing.T) {
	token, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("token contains %d bytes, want 32", len(decoded))
	}

	home := filepath.Join(t.TempDir(), "coslash")
	t.Setenv("COSLASH_HOME", home)
	if err := writeToken(token); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "token")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != token {
		t.Fatal("token file does not contain generated token")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteTokenPreservesHomePermissions(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_HOME", home)

	if err := writeToken("secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("home mode = %o, want 750", info.Mode().Perm())
	}
}

func TestHandleDiffReturnsRecordedEditsInOrder(t *testing.T) {
	edits := session.NewFileEditSet()
	edits.Add("file.txt", 1, 1, false)
	edits.Change("file.txt", "before\n", "middle\n")
	edits.Add("file.txt", 1, 1, false)
	edits.Change("file.txt", "middle\n", "after\n")
	found := &session.Session{
		ID: "session-1",
		SessionDetails: session.SessionDetails{
			FileEdits: edits.Edits,
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/diff?id=session-1&path=file.txt", nil)
	response := httptest.NewRecorder()

	handleDiff(response, request, func(id string) (*session.Session, error) {
		if id != found.ID {
			t.Fatalf("session id = %q, want %q", id, found.ID)
		}
		return found, nil
	})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Changes []struct {
			Kind      string `json:"kind"`
			Text      string `json:"text"`
			Operation string `json:"operation"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Changes) != 2 {
		t.Fatalf("changes = %#v, want two recorded edits", body.Changes)
	}
	if body.Changes[0].Operation != "Edit" ||
		body.Changes[0].Additions != 1 || body.Changes[0].Deletions != 1 ||
		body.Changes[0].Text != "@@\n-before\n+middle\n" ||
		body.Changes[1].Text != "@@\n-middle\n+after\n" {
		t.Fatalf("changes = %#v, want recorded edits in transcript order", body.Changes)
	}
}

func TestHandleDiffRejectsFilesOutsideTheSession(t *testing.T) {
	found := &session.Session{
		ID:               "session-1",
		WorkingDirectory: t.TempDir(),
		SessionDetails: session.SessionDetails{
			FileEdits: []session.FileEdit{{Path: "recorded.txt"}},
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/diff?id=session-1&path=../secret.txt", nil)
	response := httptest.NewRecorder()

	handleDiff(response, request, func(string) (*session.Session, error) { return found, nil })

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
