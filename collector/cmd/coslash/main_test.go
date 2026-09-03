package main

import (
	"bytes"
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
	"github.com/centauri-ai/coslash/collector/internal/remote"
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
	handler := routes(synthesis.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/sessions"},
		{method: http.MethodPost, path: "/api/synthesis"},
		{method: http.MethodPost, path: "/api/diff"},
		{method: http.MethodGet, path: "/api/launch"},
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

func TestSessionsAlwaysReturnsEnvelope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COSLASH_HOME", t.TempDir())
	handler := routes(synthesis.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/sessions", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Sessions []json.RawMessage `json:"sessions"`
		Machines []json.RawMessage `json:"machines"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Sessions == nil || body.Machines == nil {
		t.Fatalf("response = %s, want sessions and machines arrays", response.Body.String())
	}
}

func TestLocalMachineHealthOmitsRemoteOnlyEnums(t *testing.T) {
	encoded, err := json.Marshal(localMachineHealth())
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

func TestSettingsSaveRejectsLegacyBareSettings(t *testing.T) {
	body, err := json.Marshal(settings.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeSettingsSave(body); err == nil {
		t.Fatal("legacy bare settings were accepted")
	}
}

func TestServerWrapsRoutesWithGuard(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	server := newServer(
		httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"},
		synthesis.NewManager(nil),
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
