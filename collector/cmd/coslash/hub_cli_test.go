package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestHubCLIDeviceLoginCallConfirmationAndResult(t *testing.T) {
	var devicePolls atomic.Int32
	var actionPolls atomic.Int32
	var toolRetries atomic.Int32
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint, "authorization_servers": []string{serverOrigin(r)}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": serverOrigin(r), "registration_endpoint": serverOrigin(r) + "/oauth/register",
				"device_authorization_endpoint": serverOrigin(r) + "/oauth/device_authorization",
				"token_endpoint":                serverOrigin(r) + "/oauth/token", "revocation_endpoint": serverOrigin(r) + "/oauth/revoke",
			})
		case "/oauth/register":
			var body struct {
				Name      string   `json:"client_name"`
				Redirects []string `json:"redirect_uris"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name != "coSlash Local CLI" || len(body.Redirects) != 1 {
				http.Error(w, "bad registration", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "client_test"})
		case "/oauth/device_authorization":
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "client_test" || r.Form.Get("resource") != endpoint || r.Form.Get("scope") != hubAgentDefaultScope {
				http.Error(w, "bad device request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "device_secret", "user_code": "ABCD-EFGH", "verification_uri": serverOrigin(r) + "/agents/activate",
				"verification_uri_complete": serverOrigin(r) + "/agents/activate?user_code=ABCD-EFGH", "expires_in": 60, "interval": 1,
			})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad token request", http.StatusBadRequest)
				return
			}
			if r.Form.Get("grant_type") != hubAgentDeviceGrant || r.Form.Get("resource") != endpoint {
				http.Error(w, "unexpected token request", http.StatusBadRequest)
				return
			}
			if devicePolls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access_secret", "token_type": "Bearer", "expires_in": 3600,
			})
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer access_secret" {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			var request struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
				Params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"params"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				http.Error(w, "bad MCP request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch request.Method {
			case "tools/list":
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"coslash_delete_sessions","title":"Delete sessions","inputSchema":{"type":"object"},"annotations":{"destructiveHint":true}}]}}`, request.ID)
			case "tools/call":
				switch request.Params.Name {
				case "coslash_delete_sessions":
					if toolRetries.Add(1) == 1 {
						result := map[string]any{"isError": true, "structuredContent": map[string]any{"error": map[string]any{
							"code": "CONFIRMATION_REQUIRED", "message": "Approval required", "approve_url": serverOrigin(r) + "/approve/pa_test",
							"action_id": "pa_test", "expires_in_s": 30,
						}}}
						writeTestRPC(w, request.ID, result)
						return
					}
					var args map[string]any
					_ = json.Unmarshal(request.Params.Arguments, &args)
					if args["session_ids"] == nil {
						http.Error(w, "missing call arguments", http.StatusBadRequest)
						return
					}
					writeTestRPC(w, request.ID, map[string]any{"structuredContent": map[string]any{"deleted": 1}})
				case "coslash_get_action":
					state := "pending"
					if actionPolls.Add(1) > 1 {
						state = "approved"
					}
					writeTestRPC(w, request.ID, map[string]any{"structuredContent": map[string]any{"action_id": "pa_test", "state": state}})
				default:
					http.Error(w, "unexpected tool", http.StatusBadRequest)
				}
			default:
				http.Error(w, "unexpected method", http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	endpoint = server.URL + "/mcp"
	credentialFile := filepath.Join(t.TempDir(), "hub-agent-credentials")
	store := hubclient.FileCredentialStore{Path: credentialFile}
	var output bytes.Buffer
	cli := &hubAgentCLI{output: &output, credentials: store, http: server.Client(), now: time.Now, sleep: func(context.Context, time.Duration) error { return nil }, pollEvery: time.Millisecond}

	if err := cli.run(context.Background(), []string{"login", "--hub", endpoint}); err != nil {
		t.Fatal(err)
	}
	if err := cli.run(context.Background(), []string{"tools"}); err != nil {
		t.Fatal(err)
	}
	if err := cli.run(context.Background(), []string{"tools", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := cli.run(context.Background(), []string{"call", "coslash_delete_sessions", "--json", `{"session_ids":["synthetic-session"]}`}); err != nil {
		t.Fatal(err)
	}
	if devicePolls.Load() != 2 || actionPolls.Load() != 2 || toolRetries.Load() != 2 {
		t.Fatalf("poll/call counts = device %d, action %d, call %d", devicePolls.Load(), actionPolls.Load(), toolRetries.Load())
	}
	for _, expected := range []string{
		"Open " + server.URL + "/agents/activate?user_code=ABCD-EFGH",
		"coslash_delete_sessions\tDelete sessions", `"inputSchema"`, `"destructiveHint": true`,
		"Human approval required. Open " + server.URL + "/approve/pa_test", `"deleted": 1`, "did not issue a refresh token",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("CLI output missing %q: %s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "access_secret") {
		t.Fatalf("CLI printed an OAuth token: %s", output.String())
	}
	stored, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var credential hubAgentCredential
	if json.Unmarshal([]byte(stored), &credential) != nil || credential.AccessToken != "access_secret" || credential.RefreshToken != "" || credential.ClientID != "client_test" {
		t.Fatalf("stored Hub agent credentials are incomplete: %s", stored)
	}
}

func TestHubCLILogoutRevokesAndDeletesAgentCredentials(t *testing.T) {
	var endpoint string
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint, "authorization_servers": []string{serverOrigin(r)}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": serverOrigin(r), "registration_endpoint": serverOrigin(r) + "/oauth/register",
				"device_authorization_endpoint": serverOrigin(r) + "/oauth/device_authorization",
				"token_endpoint":                serverOrigin(r) + "/oauth/token", "revocation_endpoint": serverOrigin(r) + "/oauth/revoke",
			})
		case "/oauth/revoke":
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "client_test" || r.Form.Get("token") != "refresh_secret" {
				http.Error(w, "unexpected revoke request", http.StatusBadRequest)
				return
			}
			revoked.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	endpoint = server.URL + "/mcp"
	store := hubclient.FileCredentialStore{Path: filepath.Join(t.TempDir(), "hub-agent-credentials")}
	credential := hubAgentCredential{HubURL: endpoint, Issuer: server.URL, ClientID: "client_test", AccessToken: "access_secret", RefreshToken: "refresh_secret", ExpiresAt: time.Now().Add(time.Hour)}
	encoded, _ := json.Marshal(credential)
	if err := store.Save(context.Background(), string(encoded)); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cli := &hubAgentCLI{output: &output, credentials: store, http: server.Client(), now: time.Now, sleep: sleepHubContext, pollEvery: time.Millisecond}
	if err := cli.run(context.Background(), []string{"logout"}); err != nil {
		t.Fatal(err)
	}
	if !revoked.Load() || !strings.Contains(output.String(), "Hub credentials removed") {
		t.Fatalf("logout did not revoke and report completion: revoked=%t output=%q", revoked.Load(), output.String())
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, hubclient.ErrNotPaired) {
		t.Fatalf("Hub credentials remain after logout: %v", err)
	}
}

func TestHubCLIRefreshesAndPersistsRotatedTokens(t *testing.T) {
	var refreshCalls atomic.Int32
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint, "authorization_servers": []string{serverOrigin(r)}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": serverOrigin(r), "registration_endpoint": serverOrigin(r) + "/oauth/register",
				"device_authorization_endpoint": serverOrigin(r) + "/oauth/device_authorization",
				"token_endpoint":                serverOrigin(r) + "/oauth/token", "revocation_endpoint": serverOrigin(r) + "/oauth/revoke",
			})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh_old" || r.Form.Get("resource") != endpoint {
				http.Error(w, "unexpected refresh request", http.StatusBadRequest)
				return
			}
			refreshCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access_new", "refresh_token": "refresh_new", "token_type": "Bearer", "expires_in": 3600})
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer access_new" {
				http.Error(w, "expected refreshed bearer", http.StatusUnauthorized)
				return
			}
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	endpoint = server.URL + "/mcp"
	store := hubclient.FileCredentialStore{Path: filepath.Join(t.TempDir(), "hub-agent-credentials")}
	credential := hubAgentCredential{HubURL: endpoint, Issuer: server.URL, ClientID: "client_test", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: time.Now().Add(-time.Second)}
	encoded, _ := json.Marshal(credential)
	if err := store.Save(context.Background(), string(encoded)); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cli := &hubAgentCLI{output: &output, credentials: store, http: server.Client(), now: time.Now, sleep: sleepHubContext, pollEvery: time.Millisecond}
	if err := cli.listTools(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls.Load())
	}
	stored, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var updated hubAgentCredential
	if json.Unmarshal([]byte(stored), &updated) != nil || updated.AccessToken != "access_new" || updated.RefreshToken != "refresh_new" {
		t.Fatalf("rotated credentials were not persisted: %s", stored)
	}
}

func TestHubCLIURLAndToolValidation(t *testing.T) {
	for _, raw := range []string{"http://example.com/mcp", "https://example.com/", "https://user:pass@example.com/mcp", "https://example.com/mcp?token=x"} {
		if _, err := validateHubURL(raw); err == nil {
			t.Errorf("accepted Hub URL %q", raw)
		}
	}
	if _, err := validateHubURL("http://127.0.0.1:8000/mcp"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"coslash_delete_sessions", "coslash_get_action"} {
		if !validHubToolName(name) {
			t.Errorf("rejected tool %q", name)
		}
	}
	for _, name := range []string{"coslash.search", "other_tool", "coslash_", "coslash_delete_sessions;echo"} {
		if validHubToolName(name) {
			t.Errorf("accepted tool %q", name)
		}
	}
	if _, err := parseHubLoginArgs([]string{"--hub", "https://example.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parseHubLoginArgs([]string{"--hub", "https://example.com"}); err == nil {
		t.Fatal("accepted Hub origin without /mcp")
	}
}

func TestHubCLIHelpDoesNotTouchCredentialStore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"hub", "--help"}); code != 0 || !strings.Contains(stdout.String(), "coslash hub login") || stderr.Len() != 0 {
		t.Fatalf("hub help: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestHubAgentCredentialStoreUsesIsolatedServiceAndLinuxFile(t *testing.T) {
	mac, ok := hubAgentCredentialStore("darwin").(hubclient.OSKeychain)
	if !ok || mac.Service != hubAgentKeychainService || mac.Service == "ai.coslash.hub-device" {
		t.Fatalf("macOS Hub agent credential store = %#v", mac)
	}
	linux, ok := hubAgentCredentialStore("linux").(hubclient.FileCredentialStore)
	if !ok || filepath.Base(linux.Path) != "agent-access" || strings.Contains(linux.Path, "hub-device") {
		t.Fatalf("Linux Hub agent credential store = %#v", linux)
	}
}

func serverOrigin(r *http.Request) string {
	return "http://" + r.Host
}

func writeTestRPC(w http.ResponseWriter, id int, result any) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
