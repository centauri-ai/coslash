package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

// statusHub is a Hub OAuth and MCP fixture for `hub auth status`. It accepts
// access_old and any refreshed token, enforces hub:read for the account tool
// from the grant it knows, and counts every path.
type statusHub struct {
	server   *httptest.Server
	endpoint string

	mu          sync.Mutex
	calls       map[string]int
	grant       *string
	valid       map[string]bool
	pingStatus  int
	pingBody    string
	account     func(http.ResponseWriter)
	tokenStatus int
	tokenBody   string
	onPing      func()
}

func newStatusHub(t *testing.T) *statusHub {
	t.Helper()
	hub := &statusHub{calls: map[string]int{}, valid: map[string]bool{"access_old": true}}
	hub.server = httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(hub.server.Close)
	hub.endpoint = hub.server.URL + "/mcp"
	return hub
}

func (h *statusHub) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	key := r.URL.Path
	var call struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if key == "/mcp" {
		_ = json.NewDecoder(r.Body).Decode(&call)
		key = "mcp:" + call.Method + call.Params.Name
	}
	h.calls[key]++
	valid := h.valid[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	onPing := h.onPing
	h.mu.Unlock()
	switch {
	case r.URL.Path == "/.well-known/oauth-protected-resource/mcp":
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": h.endpoint, "authorization_servers": []string{serverOrigin(r)}})
	case r.URL.Path == "/.well-known/oauth-authorization-server":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": serverOrigin(r), "registration_endpoint": serverOrigin(r) + "/oauth/register",
			"device_authorization_endpoint": serverOrigin(r) + "/oauth/device_authorization",
			"token_endpoint":                serverOrigin(r) + "/oauth/token", "revocation_endpoint": serverOrigin(r) + "/oauth/revoke",
		})
	case r.URL.Path == "/oauth/token":
		if h.tokenStatus != 0 {
			w.WriteHeader(h.tokenStatus)
			_, _ = io.WriteString(w, h.tokenBody)
			return
		}
		h.mu.Lock()
		h.valid["access_new"] = true
		h.mu.Unlock()
		response := map[string]any{"access_token": "access_new", "refresh_token": "refresh_new", "token_type": "Bearer", "expires_in": 3600}
		if h.grant != nil {
			response["scope"] = *h.grant
		}
		_ = json.NewEncoder(w).Encode(response)
	case r.URL.Path != "/mcp":
		http.NotFound(w, r)
	case !valid:
		w.WriteHeader(http.StatusUnauthorized)
	case call.Method == "ping":
		if onPing != nil {
			onPing()
		}
		if h.pingStatus != 0 {
			w.WriteHeader(h.pingStatus)
			return
		}
		if h.pingBody != "" {
			_, _ = io.WriteString(w, h.pingBody)
			return
		}
		writeTestRPC(w, 1, map[string]any{})
	case call.Method == "tools/call" && call.Params.Name == "coslash_get_account":
		if h.account != nil {
			h.account(w)
			return
		}
		if h.grant != nil && !slices.Contains(strings.Fields(*h.grant), "hub:read") {
			writeTestRPC(w, 1, map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": `{"error":{"code":"SCOPE_MISSING","message":"This agent has not been granted the required scope."}}`}}})
			return
		}
		writeTestRPC(w, 1, testAccountResult("synthetic-user", "Test User", "user@example.test"))
	default:
		http.Error(w, "unexpected MCP call", http.StatusBadRequest)
	}
}

func testAccountResult(id, name, email string) map[string]any {
	profile := map[string]any{"id": id, "name": name, "email": email, "photoUrl": "https://example.test/photo", "preferences": map[string]any{"theme": "secret_preference"}, "untrusted": true}
	return map[string]any{"structuredContent": map[string]any{"profile": profile, "notices": []any{map[string]any{"title": "secret_notice"}}, "unresolved_count": 1}}
}

func (h *statusHub) count(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[key]
}

type statusEnv struct {
	hub      *statusHub
	store    hubclient.CredentialStore
	lockPath string
	cli      *hubAgentCLI
}

func newStatusEnv(t *testing.T, scope *string, expires time.Duration) *statusEnv {
	t.Helper()
	hub := newStatusHub(t)
	hub.grant = scope
	dir := t.TempDir()
	env := &statusEnv{hub: hub, store: hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}, lockPath: filepath.Join(dir, "agent-access.lock")}
	env.cli = newTestHubCLI(io.Discard, env.store, hub.server.Client(), env.lockPath)
	saveTestHubCredential(t, env.store, hubAgentCredential{HubURL: hub.endpoint, Issuer: hub.server.URL, ClientID: "client_test", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: time.Now().Add(expires), Scope: scope})
	return env
}

func ptr[T any](value T) *T { return &value }

func TestHubAuthStatus(t *testing.T) {
	type want struct {
		status, identity, code string
		exit                   int
	}
	check := func(t *testing.T, env *statusEnv, w want) hubAuthStatusResult {
		t.Helper()
		result, exit := env.cli.authStatus(context.Background())
		code := ""
		if result.ErrorCode != nil {
			code = *result.ErrorCode
		}
		if exit != w.exit || result.Status != w.status || result.IdentityStatus != w.identity || code != w.code {
			t.Fatalf("status = exit %d %q identity %q code %q; want exit %d %q identity %q code %q", exit, result.Status, result.IdentityStatus, code, w.exit, w.status, w.identity, w.code)
		}
		if env.hub.count("/oauth/register") != 0 || env.hub.count("/oauth/device_authorization") != 0 {
			t.Fatal("status started a login")
		}
		return result
	}
	keepsCredential := func(t *testing.T, env *statusEnv) {
		t.Helper()
		if _, err := env.store.Load(context.Background()); err != nil {
			t.Fatalf("status removed credentials: %v", err)
		}
	}

	t.Run("every granted scope subset", func(t *testing.T) {
		all := []string{"hub:admin", "hub:read", "hub:share", "hub:write"}
		for mask := 0; mask < 1<<len(all); mask++ {
			var granted []string
			for i, scope := range all {
				if mask&(1<<i) != 0 {
					granted = append(granted, scope)
				}
			}
			slices.Reverse(granted)
			env := newStatusEnv(t, ptr(strings.Join(granted, " ")), time.Hour)
			canRead := slices.Contains(granted, "hub:read")
			identity := map[bool]string{true: "available", false: "insufficient_scope"}[canRead]
			result := check(t, env, want{"authenticated", identity, "", 0})
			slices.Sort(granted)
			if result.Scopes == nil || !slices.Equal(*result.Scopes, append([]string{}, granted...)) {
				t.Fatalf("grant %q reported as %v", granted, result.Scopes)
			}
			if canRead != (result.Account != nil) || env.hub.count("mcp:tools/callcoslash_get_account") != map[bool]int{true: 1, false: 0}[canRead] {
				t.Fatalf("grant %q: account=%v reads=%d", granted, result.Account, env.hub.count("mcp:tools/callcoslash_get_account"))
			}
			if env.hub.count("/oauth/token") != 0 {
				t.Fatal("status rotated a valid token")
			}
		}
	})
	t.Run("unknown scope tries the account once", func(t *testing.T) {
		env := newStatusEnv(t, nil, time.Hour)
		result := check(t, env, want{"authenticated", "available", "", 0})
		if result.Scopes != nil || result.Account == nil || result.Account.ID != "synthetic-user" || env.hub.count("/oauth/token") != 0 {
			t.Fatalf("unknown scope result = %+v", result)
		}
	})
	t.Run("unknown scope without read access", func(t *testing.T) {
		env := newStatusEnv(t, nil, time.Hour)
		env.hub.grant = ptr("hub:write")
		check(t, env, want{"authenticated", "insufficient_scope", "", 0})
		if env.hub.count("mcp:tools/callcoslash_get_account") != 1 {
			t.Fatal("account read was retried")
		}
	})
	t.Run("expired token refreshes and reports the new grant", func(t *testing.T) {
		env := newStatusEnv(t, nil, -time.Minute)
		env.hub.grant = ptr("hub:read hub:share")
		result := check(t, env, want{"authenticated", "available", "", 0})
		if result.Scopes == nil || strings.Join(*result.Scopes, " ") != "hub:read hub:share" || env.hub.count("/oauth/token") != 1 {
			t.Fatalf("refreshed result scopes = %v, refreshes = %d", result.Scopes, env.hub.count("/oauth/token"))
		}
	})
	t.Run("account request refresh updates status", func(t *testing.T) {
		env := newStatusEnv(t, nil, time.Hour)
		start := time.Date(2040, time.January, 2, 3, 4, 5, 0, time.UTC)
		saveTestHubCredential(t, env.store, hubAgentCredential{HubURL: env.hub.endpoint, Issuer: env.hub.server.URL, ClientID: "client_test", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: start.Add(31 * time.Second)})
		var clockMu sync.Mutex
		current := start
		env.cli.now = func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return current
		}
		env.hub.grant = ptr("hub:read hub:share")
		env.hub.onPing = func() {
			clockMu.Lock()
			current = start.Add(2 * time.Second)
			clockMu.Unlock()
		}
		result := check(t, env, want{"authenticated", "available", "", 0})
		if result.Scopes == nil || strings.Join(*result.Scopes, " ") != "hub:read hub:share" || result.ExpiresAt == nil || *result.ExpiresAt != start.Add(2*time.Second+time.Hour).Format(time.RFC3339) || env.hub.count("/oauth/token") != 1 {
			t.Fatalf("status after account request refresh = %+v, refreshes = %d", result, env.hub.count("/oauth/token"))
		}
	})
	t.Run("scope authorization uses raw tokens", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:re\u200b"+"ad"), time.Hour)
		result := check(t, env, want{"authenticated", "insufficient_scope", "", 0})
		if result.Scopes == nil || !slices.Equal(*result.Scopes, []string{"hub:read"}) || env.hub.count("mcp:tools/callcoslash_get_account") != 0 {
			t.Fatalf("invisible scope token status = %+v, account reads = %d", result, env.hub.count("mcp:tools/callcoslash_get_account"))
		}
	})
	t.Run("not logged in", func(t *testing.T) {
		env := newStatusEnv(t, nil, time.Hour)
		_ = env.store.Delete(context.Background())
		result := check(t, env, want{"login_required", "not_checked", "not_logged_in", 3})
		if result.HubURL != nil || *result.RecoveryCommand != "coslash hub login" {
			t.Fatalf("not logged in result = %+v", result)
		}
	})
	t.Run("expired without refresh token", func(t *testing.T) {
		env := newStatusEnv(t, nil, -time.Minute)
		saveTestHubCredential(t, env.store, hubAgentCredential{HubURL: env.hub.endpoint, Issuer: env.hub.server.URL, ClientID: "client_test", AccessToken: "access_old", ExpiresAt: time.Now().Add(-time.Minute)})
		check(t, env, want{"login_required", "not_checked", "token_expired", 3})
		keepsCredential(t, env)
	})
	t.Run("revoked login", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.valid = map[string]bool{}
		env.hub.tokenStatus, env.hub.tokenBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
		check(t, env, want{"login_required", "not_checked", "invalid_grant", 3})
		keepsCredential(t, env)
	})
	t.Run("persistent 401 after one refresh", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.valid = map[string]bool{}
		env.hub.tokenStatus, env.hub.tokenBody = http.StatusOK, `{"access_token":"access_new","refresh_token":"refresh_new","token_type":"Bearer","expires_in":3600}`
		check(t, env, want{"login_required", "not_checked", "unauthorized", 3})
		if env.hub.count("/oauth/token") != 1 || env.hub.count("mcp:ping") != 2 {
			t.Fatalf("refreshes=%d pings=%d", env.hub.count("/oauth/token"), env.hub.count("mcp:ping"))
		}
	})
	t.Run("refreshed credential cannot be saved", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), -time.Minute)
		env.cli.credentials = failingSaveStore{env.store}
		check(t, env, want{"credential_error", "not_checked", "save_failed", 5})
	})
	t.Run("invalid stored credential", func(t *testing.T) {
		env := newStatusEnv(t, nil, time.Hour)
		_ = env.store.Save(context.Background(), `{"access_token":"truncated`)
		result := check(t, env, want{"credential_error", "not_checked", "invalid_credentials", 5})
		if result.HubURL != nil || *result.RecoveryCommand != "coslash hub login" {
			t.Fatalf("invalid credential result = %+v", result)
		}
		keepsCredential(t, env)
	})
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{"credential store unavailable", hubclient.ErrCredentialStoreUnavailable, "store_unavailable"},
		{"keychain prompt left open until the deadline", context.DeadlineExceeded, "store_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := newStatusEnv(t, nil, time.Hour)
			env.cli.credentials = failingLoadStore{err: test.err}
			check(t, env, want{"credential_error", "not_checked", test.code, 5})
		})
	}
	t.Run("undecryptable sealed credential", func(t *testing.T) {
		env := newStatusEnv(t, nil, time.Hour)
		dir := t.TempDir()
		sealed := hubclient.SealedFileCredentialStore{Path: filepath.Join(dir, "agent-access.enc"), Keychain: &fakeKeychain{}}
		saveTestHubCredential(t, sealed, hubAgentCredential{HubURL: env.hub.endpoint, Issuer: env.hub.server.URL, ClientID: "client_test", AccessToken: "access_old", ExpiresAt: time.Now().Add(time.Hour)})
		sealed.Keychain = &fakeKeychain{value: strings.Repeat("ab", 32)}
		env.cli.credentials = sealed
		result := check(t, env, want{"credential_error", "not_checked", "invalid_credentials", 5})
		if *result.RecoveryCommand != "coslash hub login" {
			t.Fatalf("recovery = %q", *result.RecoveryCommand)
		}
	})
	t.Run("network failure is not a revoked login", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.server.Close()
		result := check(t, env, want{"unreachable", "not_checked", "network_error", 4})
		if *result.RecoveryCommand != "coslash hub auth status" {
			t.Fatalf("network recovery = %q", *result.RecoveryCommand)
		}
		keepsCredential(t, env)
	})
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   want
	}{
		{"malformed MCP response", 0, "not json", want{"error", "not_checked", "protocol_error", 1}},
		{"JSON-RPC error", 0, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601}}`, want{"error", "not_checked", "protocol_error", 1}},
		{"forbidden", http.StatusForbidden, "", want{"error", "not_checked", "access_denied", 1}},
		{"rate limited", http.StatusTooManyRequests, "", want{"error", "not_checked", "rate_limited", 1}},
		{"server error", http.StatusServiceUnavailable, "", want{"error", "not_checked", "server_error", 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := newStatusEnv(t, ptr("hub:read"), time.Hour)
			env.hub.pingStatus, env.hub.pingBody = test.status, test.body
			check(t, env, test.want)
			if env.hub.count("mcp:ping") != 1 {
				t.Fatalf("ping retried %d times", env.hub.count("mcp:ping"))
			}
			keepsCredential(t, env)
		})
	}
	t.Run("account read failure after ping is partial", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.account = func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }
		result := check(t, env, want{"authenticated", "unavailable", "identity_unavailable", 0})
		if result.Account != nil || env.hub.count("mcp:tools/callcoslash_get_account") != 1 {
			t.Fatalf("partial result = %+v", result)
		}
	})
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprintf("account read HTTP %d is partial and not retried", status), func(t *testing.T) {
			env := newStatusEnv(t, ptr("hub:read"), time.Hour)
			env.hub.account = func(w http.ResponseWriter) { w.WriteHeader(status) }
			check(t, env, want{"authenticated", "unavailable", "identity_unavailable", 0})
			if env.hub.count("mcp:tools/callcoslash_get_account") != 1 || env.hub.count("/oauth/token") != 0 {
				t.Fatalf("account reads=%d refreshes=%d", env.hub.count("mcp:tools/callcoslash_get_account"), env.hub.count("/oauth/token"))
			}
		})
	}
	t.Run("network failure during the account read is partial", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.account = func(w http.ResponseWriter) {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
		}
		check(t, env, want{"authenticated", "unavailable", "identity_unavailable", 0})
	})
	t.Run("rejected token without refresh token is unauthorized", func(t *testing.T) {
		env := newStatusEnv(t, nil, time.Hour)
		saveTestHubCredential(t, env.store, hubAgentCredential{HubURL: env.hub.endpoint, Issuer: env.hub.server.URL, ClientID: "client_test", AccessToken: "access_revoked", ExpiresAt: time.Now().Add(time.Hour)})
		check(t, env, want{"login_required", "not_checked", "unauthorized", 3})
	})
	t.Run("malformed lock file", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		if err := os.WriteFile(env.lockPath, []byte("garbage\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		result := check(t, env, want{"credential_error", "not_checked", "invalid_lock_file", 5})
		if result.RecoveryCommand != nil {
			t.Fatalf("malformed lock recovery = %q", *result.RecoveryCommand)
		}
	})
	t.Run("account tool error is partial", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.account = func(w http.ResponseWriter) {
			writeTestRPC(w, 1, map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": `{"error":{"code":"UNAVAILABLE"}}`}}})
		}
		check(t, env, want{"authenticated", "unavailable", "identity_unavailable", 0})
	})
	t.Run("authentication fails during the account read", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.account = func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) }
		env.hub.tokenStatus, env.hub.tokenBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
		result := check(t, env, want{"login_required", "not_checked", "invalid_grant", 3})
		if result.Account != nil {
			t.Fatal("login required result kept an account")
		}
		keepsCredential(t, env)
	})
	t.Run("connection replaced during verification", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.onPing = func() {
			saveTestHubCredential(t, env.store, hubAgentCredential{HubURL: env.hub.endpoint, Issuer: env.hub.server.URL, ClientID: "client_other", AccessToken: "access_other", ExpiresAt: time.Now().Add(time.Hour)})
		}
		result := check(t, env, want{"busy", "not_checked", "connection_changed", 6})
		if result.Account != nil || result.HubURL != nil || result.Scopes != nil {
			t.Fatalf("changed connection result mixed logins: %+v", result)
		}
	})
	t.Run("login started during verification", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.onPing = func() {
			_ = env.cli.withCredentialLock(context.Background(), func(_ context.Context, lock *os.File) error {
				_, err := advanceHubAgentRevision(lock)
				return err
			})
		}
		check(t, env, want{"busy", "not_checked", "connection_changed", 6})
	})
	t.Run("lock busy", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		holder, err := lockHubAgentCredentials(context.Background(), env.lockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		previous := hubAgentLockWait
		hubAgentLockWait = 100 * time.Millisecond
		defer func() { hubAgentLockWait = previous }()
		check(t, env, want{"busy", "not_checked", "lock_timeout", 6})
	})
	t.Run("cancelled", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, exit := env.cli.authStatus(ctx)
		if exit != 6 || *result.ErrorCode != "cancelled" {
			t.Fatalf("cancelled status = %d %+v", exit, result)
		}
	})
}

type failingSaveStore struct{ hubclient.CredentialStore }

func (failingSaveStore) Save(context.Context, string) error { return errors.New("disk full") }

type failingLoadStore struct {
	hubclient.CredentialStore
	err error
}

func (s failingLoadStore) Load(context.Context) (string, error) { return "", s.err }

func TestHubAuthStatusOutput(t *testing.T) {
	decodeOne := func(t *testing.T, data []byte) map[string]json.RawMessage {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(data))
		var object map[string]json.RawMessage
		if err := decoder.Decode(&object); err != nil {
			t.Fatalf("output is not JSON: %v: %q", err, data)
		}
		if _, err := decoder.Token(); err != io.EOF {
			t.Fatalf("output has more than one JSON value: %q", data)
		}
		for _, key := range []string{"status", "hub_url", "scopes", "expires_at", "account", "identity_status", "error_code", "recovery_command"} {
			if _, ok := object[key]; !ok {
				t.Fatalf("output lacks %q: %s", key, data)
			}
		}
		if len(object) != 8 {
			t.Fatalf("output has extra fields: %s", data)
		}
		return object
	}
	render := func(t *testing.T, result hubAuthStatusResult) (string, string) {
		t.Helper()
		var jsonOut, textOut bytes.Buffer
		if err := writeHubAuthStatus(&jsonOut, result, true); err != nil {
			t.Fatal(err)
		}
		if err := writeHubAuthStatus(&textOut, result, false); err != nil {
			t.Fatal(err)
		}
		return jsonOut.String(), textOut.String()
	}

	t.Run("null and empty scopes differ", func(t *testing.T) {
		for _, test := range []struct {
			scope      *string
			json, text string
		}{{nil, "null", "unknown"}, {ptr(""), "[]", "none granted"}, {ptr("hub:read \x1b]0;pwned\x07"), `["]0;pwned","hub:read"]`, "]0;pwned hub:read"}, {ptr("hub:write hub:read hub:write"), `["hub:read","hub:write"]`, "hub:read hub:write"}} {
			env := newStatusEnv(t, test.scope, time.Hour)
			result, _ := env.cli.authStatus(context.Background())
			jsonOut, textOut := render(t, result)
			object := decodeOne(t, []byte(jsonOut))
			var compact bytes.Buffer
			_ = json.Compact(&compact, object["scopes"])
			if compact.String() != test.json || !strings.Contains(textOut, "Scopes:    "+test.text) {
				t.Fatalf("scope %v: json %s text %q", test.scope, compact.String(), textOut)
			}
		}
	})
	t.Run("untrusted account text and secrets", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		saveTestHubCredential(t, env.store, hubAgentCredential{HubURL: env.hub.endpoint, Issuer: env.hub.server.URL, ClientID: "client_test", AccessToken: "access_old", RefreshToken: "canary_refresh_secret", ExpiresAt: time.Now().Add(time.Hour), Scope: ptr("hub:read")})
		long := strings.Repeat("é", 1000)
		env.hub.account = func(w http.ResponseWriter) {
			writeTestRPC(w, 1, testAccountResult("synthetic-user", "\x1b[31mEvil\x1b[0m\u202e\x07 Name", long))
		}
		result, exit := env.cli.authStatus(context.Background())
		if exit != 0 || result.Account == nil {
			t.Fatalf("status = %d %+v", exit, result)
		}
		if result.Account.Name != "[31mEvil[0m Name" || len([]rune(result.Account.Email)) != 256 {
			t.Fatalf("account text not sanitized: %q (%d runes)", result.Account.Name, len([]rune(result.Account.Email)))
		}
		jsonOut, textOut := render(t, result)
		decodeOne(t, []byte(jsonOut))
		for _, output := range []string{jsonOut, textOut} {
			for _, secret := range []string{"\x1b", "\u202e", "\x07", "access_old", "canary_refresh_secret", "client_test", "secret_preference", "secret_notice", "photo", "agent-access"} {
				if strings.Contains(output, secret) {
					t.Fatalf("output contains %q:\n%s", secret, output)
				}
			}
		}
	})
	t.Run("recovery names the saved nondefault Hub", func(t *testing.T) {
		env := newStatusEnv(t, ptr("hub:read"), time.Hour)
		env.hub.valid = map[string]bool{}
		env.hub.tokenStatus, env.hub.tokenBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
		result, _ := env.cli.authStatus(context.Background())
		if *result.RecoveryCommand != "coslash hub login --hub "+env.hub.endpoint {
			t.Fatalf("recovery = %q", *result.RecoveryCommand)
		}
		if got := shellQuoteHubArgument("http://localhost:1/mcp'; rm -rf ~ #"); got != `'http://localhost:1/mcp'\''; rm -rf ~ #'` {
			t.Fatalf("quoted URL = %s", got)
		}
	})
	t.Run("write failure reports on stderr only", func(t *testing.T) {
		if err := writeHubAuthStatus(failingWriter{}, hubAuthStatusResult{Status: "authenticated"}, true); err == nil {
			t.Fatal("write failure was ignored")
		}
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }

func TestHubAuthStatusDispatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	for _, args := range [][]string{{"hub", "auth", "--help"}, {"hub", "auth", "status", "--help"}, {"hub", "auth", "status", "-h"}} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(&stdout, &stderr, args); code != 0 || stdout.String() != hubAuthUsage+"\n" || stderr.Len() != 0 {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	for _, args := range [][]string{{"hub", "auth"}, {"hub", "auth", "login"}, {"hub", "auth", "status", "--bogus"}, {"hub", "auth", "status", "--json", "--json"}} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(&stdout, &stderr, args); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), hubAuthUsage) {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, "coslash")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("help or usage touched the coSlash home: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"hub", "--help"}); code != 0 || !strings.Contains(stdout.String(), "auth status [--json]") {
		t.Fatalf("hub help = %d %q", code, stdout.String())
	}
	if runtime.GOOS != "linux" {
		t.Skip("only Linux keeps Hub credentials in an isolated file; other platforms use the OS keychain")
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCLI(&stdout, &stderr, []string{"hub", "auth", "status", "--json"}); code != 3 || stderr.Len() != 0 {
		t.Fatalf("status without login: code=%d stderr=%q", code, stderr.String())
	}
	var result hubAuthStatusResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Status != "login_required" || *result.ErrorCode != "not_logged_in" {
		t.Fatalf("status without login = %s", stdout.String())
	}
	stdout.Reset()
	if code := runCLI(&stdout, &stderr, []string{"hub", "auth", "status"}); code != 3 || !strings.Contains(stdout.String(), "Status:    login required") || stderr.Len() != 0 {
		t.Fatalf("text status without login: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if code := runHubAuthCLIWithWriter(t); code != 1 {
		t.Fatalf("write failure exit = %d", code)
	}
}

func runHubAuthCLIWithWriter(t *testing.T) int {
	t.Helper()
	var stderr bytes.Buffer
	code := runHubAuthCLI(failingWriter{}, &stderr, []string{"status", "--json"})
	if stderr.String() != "Error: could not write the Hub authentication status\n" {
		t.Fatalf("write failure stderr = %q", stderr.String())
	}
	return code
}
