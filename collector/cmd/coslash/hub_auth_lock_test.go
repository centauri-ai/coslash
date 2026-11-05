package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const hubLockChildEnv = "COSLASH_TEST_HUB_LOCK_CHILD"

// rotatingHubFixture is an OAuth and MCP server whose refresh tokens are
// single use. Replaying a used refresh token revokes the whole token family,
// as the Hub does.
type rotatingHubFixture struct {
	server   *httptest.Server
	endpoint string

	mu          sync.Mutex
	access      map[string]bool
	refresh     map[string]bool
	used        map[string]bool
	refreshes   int
	revoked     []string
	scope       *string
	mcpArrivals chan string
	mcpRelease  chan struct{}
	holdBearer  string
}

func newRotatingHubFixture(t *testing.T, access, refresh string) *rotatingHubFixture {
	t.Helper()
	fixture := &rotatingHubFixture{access: map[string]bool{access: true}, refresh: map[string]bool{refresh: true}, used: map[string]bool{}}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(fixture.server.Close)
	fixture.endpoint = fixture.server.URL + "/mcp"
	return fixture
}

func (f *rotatingHubFixture) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": f.endpoint, "authorization_servers": []string{serverOrigin(r)}})
	case "/.well-known/oauth-authorization-server":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": serverOrigin(r), "registration_endpoint": serverOrigin(r) + "/oauth/register",
			"device_authorization_endpoint": serverOrigin(r) + "/oauth/device_authorization",
			"token_endpoint":                serverOrigin(r) + "/oauth/token", "revocation_endpoint": serverOrigin(r) + "/oauth/revoke",
		})
	case "/oauth/token":
		if r.ParseForm() != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("resource") != f.endpoint {
			http.Error(w, "unexpected token request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		token := r.Form.Get("refresh_token")
		if f.used[token] || !f.refresh[token] {
			f.access, f.refresh = map[string]bool{}, map[string]bool{}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		f.used[token] = true
		delete(f.refresh, token)
		f.refreshes++
		access, refresh := fmt.Sprintf("access_%d", f.refreshes), fmt.Sprintf("refresh_%d", f.refreshes)
		f.access[access], f.refresh[refresh] = true, true
		response := map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600}
		if f.scope != nil {
			response["scope"] = *f.scope
		}
		_ = json.NewEncoder(w).Encode(response)
	case "/oauth/revoke":
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case "/mcp":
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		hold := f.mcpArrivals != nil && bearer == f.holdBearer
		f.mu.Unlock()
		if hold {
			f.mcpArrivals <- bearer
			<-f.mcpRelease
		}
		f.mu.Lock()
		valid := f.access[bearer]
		f.mu.Unlock()
		if !valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeTestRPC(w, 1, map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

func (f *rotatingHubFixture) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

func saveTestHubCredential(t *testing.T, store hubclient.CredentialStore, credential hubAgentCredential) {
	t.Helper()
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), string(encoded)); err != nil {
		t.Fatal(err)
	}
}

func storedTestHubCredential(t *testing.T, store hubclient.CredentialStore) hubAgentCredential {
	t.Helper()
	value, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var credential hubAgentCredential
	if err := json.Unmarshal([]byte(value), &credential); err != nil {
		t.Fatal(err)
	}
	return credential
}

func newTestHubCLI(output io.Writer, store hubclient.CredentialStore, client *http.Client, lockPath string) *hubAgentCLI {
	return &hubAgentCLI{output: output, credentials: store, http: client, now: time.Now, sleep: sleepHubContext, pollEvery: time.Millisecond, lockPath: lockPath}
}

// TestHubConcurrentRefreshProcesses runs two separate CLI processes against
// one credential file and a Hub that revokes a token family on replay.
func TestHubConcurrentRefreshProcesses(t *testing.T) {
	if mode := os.Getenv(hubLockChildEnv); mode != "" {
		os.Exit(runHubLockChild(mode))
	}
	for _, mode := range []string{"expired", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRotatingHubFixture(t, "access_old", "refresh_old")
			dir := t.TempDir()
			store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
			expires := time.Now().Add(-time.Minute)
			var release sync.Once
			if mode == "unauthorized" {
				// The Hub rejects this unexpired token, so both processes
				// force a refresh after the same HTTP 401.
				expires = time.Now().Add(time.Hour)
				fixture.access = map[string]bool{}
				fixture.mcpArrivals = make(chan string, 2)
				fixture.mcpRelease = make(chan struct{})
				fixture.holdBearer = "access_old"
				t.Cleanup(func() { release.Do(func() { close(fixture.mcpRelease) }) })
			}
			saveTestHubCredential(t, store, hubAgentCredential{HubURL: fixture.endpoint, Issuer: fixture.server.URL, ClientID: "client_test", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: expires})
			lockPath := filepath.Join(dir, "agent-access.lock")

			type child struct {
				cmd    *exec.Cmd
				stdin  io.WriteCloser
				stdout *bufio.Reader
				stderr strings.Builder
			}
			children := make([]*child, 2)
			for i := range children {
				c := &child{cmd: exec.Command(os.Args[0], "-test.run=^TestHubConcurrentRefreshProcesses$")}
				c.cmd.Env = append(os.Environ(), hubLockChildEnv+"="+mode, "COSLASH_TEST_HUB_STORE="+store.Path, "COSLASH_TEST_HUB_LOCK="+lockPath)
				c.cmd.Stderr = &c.stderr
				stdin, err := c.cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				stdout, err := c.cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				c.stdin, c.stdout = stdin, bufio.NewReader(stdout)
				if err := c.cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = c.cmd.Process.Kill() })
				children[i] = c
			}
			if mode == "expired" {
				// Both processes hold the expired snapshot before either refreshes.
				for _, c := range children {
					if line, err := c.stdout.ReadString('\n'); err != nil || line != "loaded\n" {
						t.Fatalf("child did not load credentials: %q %v %s", line, err, c.stderr.String())
					}
				}
				for _, c := range children {
					_, _ = io.WriteString(c.stdin, "go\n")
				}
			} else {
				// Both processes sent the stale bearer before either sees a 401.
				for range children {
					select {
					case <-fixture.mcpArrivals:
					case <-time.After(30 * time.Second):
						t.Fatal("children did not send concurrent MCP requests")
					}
				}
				fixture.mu.Lock()
				fixture.holdBearer = ""
				fixture.mu.Unlock()
				release.Do(func() { close(fixture.mcpRelease) })
			}
			for _, c := range children {
				_ = c.stdin.Close()
				_, _ = io.Copy(io.Discard, c.stdout)
				if err := c.cmd.Wait(); err != nil {
					t.Errorf("child failed: %v: %s", err, c.stderr.String())
				}
			}
			if got := fixture.refreshCount(); got != 1 {
				t.Errorf("refreshes = %d, want 1", got)
			}
			cli := newTestHubCLI(io.Discard, store, fixture.server.Client(), lockPath)
			if _, err := cli.mcpRequest(context.Background(), "ping", map[string]any{}); err != nil {
				t.Errorf("stored credential is no longer usable: %v", err)
			}
		})
	}
}

func runHubLockChild(mode string) int {
	ctx := context.Background()
	store := hubclient.FileCredentialStore{Path: os.Getenv("COSLASH_TEST_HUB_STORE")}
	client := &http.Client{Timeout: 30 * time.Second}
	cli := newTestHubCLI(io.Discard, store, client, os.Getenv("COSLASH_TEST_HUB_LOCK"))
	switch mode {
	case "expired":
		// This is mcpRequest split at its snapshot so the parent can make
		// both processes load before either refreshes.
		snapshot, err := cli.loadCredential(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		fmt.Println("loaded")
		if line, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil || line != "go\n" {
			return 4
		}
		credential, err := cli.refreshIfNeeded(ctx, snapshot, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 5
		}
		_, status, err := cli.sendMCP(ctx, credential, "ping", map[string]any{})
		if err != nil || status != http.StatusOK {
			fmt.Fprintln(os.Stderr, status, err)
			return 6
		}
	case "unauthorized":
		if _, err := cli.mcpRequest(ctx, "ping", map[string]any{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 7
		}
	case "hold-lock":
		file, err := lockHubAgentCredentials(ctx, os.Getenv("COSLASH_TEST_HUB_LOCK"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 8
		}
		defer file.Close()
		fmt.Println("locked")
		_, _ = io.Copy(io.Discard, os.Stdin)
	default:
		return 9
	}
	return 0
}

// loginHubFixture registers numbered clients and approves device logins
// immediately unless the client is held.
type loginHubFixture struct {
	server   *httptest.Server
	endpoint string

	mu      sync.Mutex
	clients int
	hold    map[string]bool
	revoked []string
	scope   *string
}

func newLoginHubFixture(t *testing.T) *loginHubFixture {
	t.Helper()
	fixture := &loginHubFixture{hold: map[string]bool{}}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(fixture.server.Close)
	fixture.endpoint = fixture.server.URL + "/mcp"
	return fixture
}

func (f *loginHubFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": f.endpoint, "authorization_servers": []string{serverOrigin(r)}})
	case "/.well-known/oauth-authorization-server":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": serverOrigin(r), "registration_endpoint": serverOrigin(r) + "/oauth/register",
			"device_authorization_endpoint": serverOrigin(r) + "/oauth/device_authorization",
			"token_endpoint":                serverOrigin(r) + "/oauth/token", "revocation_endpoint": serverOrigin(r) + "/oauth/revoke",
		})
	case "/oauth/register":
		f.clients++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": fmt.Sprintf("client_%d", f.clients)})
	case "/oauth/device_authorization":
		_ = r.ParseForm()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "device_" + r.Form.Get("client_id"), "user_code": "ABCD-EFGH",
			"verification_uri": serverOrigin(r) + "/agents/activate", "expires_in": 60, "interval": 1,
		})
	case "/oauth/token":
		_ = r.ParseForm()
		client := r.Form.Get("client_id")
		if f.hold[client] {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		response := map[string]any{"access_token": "access_" + client, "refresh_token": "refresh_" + client, "token_type": "Bearer", "expires_in": 3600}
		if f.scope != nil {
			response["scope"] = *f.scope
		}
		_ = json.NewEncoder(w).Encode(response)
	case "/oauth/revoke":
		_ = r.ParseForm()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *loginHubFixture) setHold(client string, hold bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hold[client] = hold
}

func (f *loginHubFixture) revokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

// startPendingLogin starts a login for client_1 and returns once it polls.
func startPendingLogin(t *testing.T, fixture *loginHubFixture, store hubclient.CredentialStore, lockPath string) (release func(), result <-chan error) {
	t.Helper()
	fixture.setHold("client_1", true)
	polling, proceed := make(chan struct{}), make(chan struct{})
	cli := newTestHubCLI(io.Discard, store, fixture.server.Client(), lockPath)
	var once sync.Once
	cli.sleep = func(ctx context.Context, _ time.Duration) error {
		once.Do(func() { close(polling) })
		select {
		case <-proceed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- cli.login(context.Background(), fixture.endpoint, "hub:read") }()
	select {
	case <-polling:
	case err := <-done:
		t.Fatalf("login ended before polling: %v", err)
	}
	return func() {
		fixture.setHold("client_1", false)
		close(proceed)
	}, done
}

func TestHubCredentialTransactionLifecycle(t *testing.T) {
	t.Run("logout supersedes a pending login", func(t *testing.T) {
		fixture := newLoginHubFixture(t)
		dir := t.TempDir()
		store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
		lockPath := filepath.Join(dir, "agent-access.lock")
		release, result := startPendingLogin(t, fixture, store, lockPath)
		if err := newTestHubCLI(io.Discard, store, fixture.server.Client(), lockPath).logout(context.Background()); err != nil {
			t.Fatal(err)
		}
		release()
		if err := <-result; !errors.Is(err, errHubLoginSuperseded) {
			t.Fatalf("pending login after logout = %v", err)
		}
		if _, err := store.Load(context.Background()); !errors.Is(err, hubclient.ErrNotPaired) {
			t.Fatalf("superseded login saved credentials: %v", err)
		}
		if revoked := fixture.revokedTokens(); len(revoked) != 1 || revoked[0] != "refresh_client_1" {
			t.Fatalf("revoked tokens = %q", revoked)
		}
	})
	t.Run("later login supersedes an earlier pending login", func(t *testing.T) {
		fixture := newLoginHubFixture(t)
		dir := t.TempDir()
		store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
		lockPath := filepath.Join(dir, "agent-access.lock")
		release, result := startPendingLogin(t, fixture, store, lockPath)
		if err := newTestHubCLI(io.Discard, store, fixture.server.Client(), lockPath).login(context.Background(), fixture.endpoint, "hub:read"); err != nil {
			t.Fatal(err)
		}
		release()
		if err := <-result; !errors.Is(err, errHubLoginSuperseded) {
			t.Fatalf("earlier login = %v", err)
		}
		if stored := storedTestHubCredential(t, store); stored.ClientID != "client_2" {
			t.Fatalf("stored client = %q, want the later login", stored.ClientID)
		}
		if revoked := fixture.revokedTokens(); len(revoked) != 1 || revoked[0] != "refresh_client_1" {
			t.Fatalf("revoked tokens = %q", revoked)
		}
	})
	t.Run("refresh does not overwrite a later login", func(t *testing.T) {
		fixture := newRotatingHubFixture(t, "access_old", "refresh_old")
		dir := t.TempDir()
		store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
		cli := newTestHubCLI(io.Discard, store, fixture.server.Client(), filepath.Join(dir, "agent-access.lock"))
		old := hubAgentCredential{HubURL: fixture.endpoint, Issuer: fixture.server.URL, ClientID: "client_old", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: time.Now().Add(-time.Minute)}
		saveTestHubCredential(t, store, old)
		newer := old
		newer.ClientID, newer.AccessToken, newer.RefreshToken = "client_new", "access_new", "refresh_new"
		saveTestHubCredential(t, store, newer)
		for _, force := range []bool{false, true} {
			if _, err := cli.refreshIfNeeded(context.Background(), old, force); !errors.Is(err, errHubConnectionChanged) {
				t.Fatalf("stale refresh force=%t = %v", force, err)
			}
		}
		if stored := storedTestHubCredential(t, store); stored.ClientID != "client_new" || stored.AccessToken != "access_new" || fixture.refreshCount() != 0 {
			t.Fatalf("stale refresh changed credentials: client=%q refreshes=%d", stored.ClientID, fixture.refreshCount())
		}
	})
	t.Run("refresh does not resurrect a logout", func(t *testing.T) {
		fixture := newRotatingHubFixture(t, "access_old", "refresh_old")
		dir := t.TempDir()
		store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
		cli := newTestHubCLI(io.Discard, store, fixture.server.Client(), filepath.Join(dir, "agent-access.lock"))
		old := hubAgentCredential{HubURL: fixture.endpoint, Issuer: fixture.server.URL, ClientID: "client_old", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: time.Now().Add(-time.Minute)}
		saveTestHubCredential(t, store, old)
		if err := cli.logout(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.refreshIfNeeded(context.Background(), old, true); !errors.Is(err, errHubConnectionChanged) {
			t.Fatalf("refresh after logout = %v", err)
		}
		if _, err := store.Load(context.Background()); !errors.Is(err, hubclient.ErrNotPaired) || fixture.refreshCount() != 0 {
			t.Fatalf("refresh after logout restored credentials: %v refreshes=%d", err, fixture.refreshCount())
		}
	})
	t.Run("refresh does not supersede a pending login", func(t *testing.T) {
		fixture := newLoginHubFixture(t)
		dir := t.TempDir()
		store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
		lockPath := filepath.Join(dir, "agent-access.lock")
		old := hubAgentCredential{HubURL: fixture.endpoint, Issuer: fixture.server.URL, ClientID: "client_old", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: time.Now().Add(time.Hour)}
		saveTestHubCredential(t, store, old)
		release, result := startPendingLogin(t, fixture, store, lockPath)
		if _, err := newTestHubCLI(io.Discard, store, fixture.server.Client(), lockPath).refreshIfNeeded(context.Background(), old, true); err != nil {
			t.Fatal(err)
		}
		release()
		if err := <-result; err != nil {
			t.Fatalf("pending login after refresh = %v", err)
		}
		if stored := storedTestHubCredential(t, store); stored.ClientID != "client_1" {
			t.Fatalf("stored client = %q, want the pending login", stored.ClientID)
		}
	})
	t.Run("lock timeout after consent revokes the new tokens", func(t *testing.T) {
		fixture := newLoginHubFixture(t)
		dir := t.TempDir()
		store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
		lockPath := filepath.Join(dir, "agent-access.lock")
		release, result := startPendingLogin(t, fixture, store, lockPath)
		holder, err := lockHubAgentCredentials(context.Background(), lockPath)
		if err != nil {
			t.Fatal(err)
		}
		previous := hubAgentLockWait
		hubAgentLockWait = 100 * time.Millisecond
		t.Cleanup(func() { hubAgentLockWait = previous })
		release()
		err = <-result
		holder.Close()
		if !errors.Is(err, errHubCredentialsBusy) {
			t.Fatalf("login with a busy lock = %v", err)
		}
		if revoked := fixture.revokedTokens(); len(revoked) != 1 || revoked[0] != "refresh_client_1" {
			t.Fatalf("revoked tokens = %q", revoked)
		}
	})
	t.Run("failed read-back is not success", func(t *testing.T) {
		fixture := newLoginHubFixture(t)
		dir := t.TempDir()
		store := discardingCredentialStore{FileCredentialStore: hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}}
		var output strings.Builder
		err := newTestHubCLI(&output, store, fixture.server.Client(), filepath.Join(dir, "agent-access.lock")).login(context.Background(), fixture.endpoint, "hub:read")
		if err == nil || !strings.Contains(err.Error(), "not retained") || strings.Contains(output.String(), "Connected") {
			t.Fatalf("login without read-back = %v, output %q", err, output.String())
		}
	})
	t.Run("busy and cancelled locks return promptly", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "agent-access.lock")
		holder, err := lockHubAgentCredentials(context.Background(), lockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		cli := newTestHubCLI(io.Discard, hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}, nil, lockPath)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := cli.loadCredential(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled load = %v", err)
		}
		previous := hubAgentLockWait
		hubAgentLockWait = 100 * time.Millisecond
		defer func() { hubAgentLockWait = previous }()
		if _, err := cli.loadCredential(context.Background()); !errors.Is(err, errHubCredentialsBusy) {
			t.Fatalf("busy load = %v", err)
		}
		if _, err := cli.mcpRequest(context.Background(), "ping", map[string]any{}); !errors.Is(err, errHubCredentialsBusy) {
			t.Fatalf("busy MCP request = %v", err)
		}
	})
	t.Run("a killed holder releases the lock", func(t *testing.T) {
		lockPath := filepath.Join(t.TempDir(), "agent-access.lock")
		cmd := exec.Command(os.Args[0], "-test.run=^TestHubConcurrentRefreshProcesses$")
		cmd.Env = append(os.Environ(), hubLockChildEnv+"=hold-lock", "COSLASH_TEST_HUB_LOCK="+lockPath)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer stdin.Close()
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "locked\n" {
			t.Fatalf("holder did not lock: %q %v", line, err)
		}
		previous := hubAgentLockWait
		hubAgentLockWait = 100 * time.Millisecond
		t.Cleanup(func() { hubAgentLockWait = previous })
		if _, err := lockHubAgentCredentials(context.Background(), lockPath); !errors.Is(err, errHubCredentialsBusy) {
			t.Fatalf("lock while held = %v", err)
		}
		hubAgentLockWait = previous
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		file, err := lockHubAgentCredentials(context.Background(), lockPath)
		if err != nil {
			t.Fatalf("lock after holder died = %v", err)
		}
		file.Close()
	})
	t.Run("malformed revision fails closed", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "agent-access.lock")
		if err := os.WriteFile(lockPath, []byte("not-a-revision\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fixture := newLoginHubFixture(t)
		store := hubclient.FileCredentialStore{Path: filepath.Join(dir, "agent-access")}
		err := newTestHubCLI(io.Discard, store, fixture.server.Client(), lockPath).login(context.Background(), fixture.endpoint, "hub:read")
		if err == nil || !strings.Contains(err.Error(), "malformed") || fixture.clients != 0 {
			t.Fatalf("login with malformed lock = %v, registrations=%d", err, fixture.clients)
		}
	})
}

type discardingCredentialStore struct {
	hubclient.FileCredentialStore
}

func (discardingCredentialStore) Save(context.Context, string) error { return nil }

func TestHubAgentLockPath(t *testing.T) {
	home, coslashHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", coslashHome)
	shared := filepath.Join(home, ".coslash", "hub-credentials", "agent-access.lock")
	for goos, want := range map[string]string{
		"linux":   filepath.Join(coslashHome, "hub-credentials", "agent-access.lock"),
		"darwin":  shared,
		"windows": shared,
	} {
		if got, err := hubAgentLockPath(goos); err != nil || got != want {
			t.Errorf("%s lock path = %q, %v; want %q", goos, got, err, want)
		}
	}
	t.Setenv("COSLASH_HOME", "relative-home")
	if got, err := hubAgentLockPath("linux"); err != nil || !filepath.IsAbs(got) || filepath.Base(filepath.Dir(filepath.Dir(got))) != "relative-home" {
		t.Errorf("relative Linux lock path = %q, %v", got, err)
	}
	t.Setenv("COSLASH_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	home, err := filepath.Abs(settings.Home())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "hub-credentials", "agent-access.lock")
	if got, err := hubAgentLockPath("linux"); err != nil || got != want {
		t.Errorf("Linux lock path without home environment = %q, %v; want %q", got, err, want)
	}
}

// fakeKeychain stands in for the macOS keychain item that holds either the
// legacy OAuth JSON or the sealed-file key.
type fakeKeychain struct {
	mu    sync.Mutex
	value string
	saves int
}

func (k *fakeKeychain) Load(context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.value == "" {
		return "", hubclient.ErrNotPaired
	}
	return k.value, nil
}

func (k *fakeKeychain) Save(_ context.Context, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.value = value
	k.saves++
	return nil
}

func (k *fakeKeychain) Delete(context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.value = ""
	return nil
}

func TestHubLegacyMigrationTransaction(t *testing.T) {
	fixture := newRotatingHubFixture(t, "access_old", "refresh_old")
	dir := t.TempDir()
	legacy, _ := json.Marshal(hubAgentCredential{HubURL: fixture.endpoint, Issuer: fixture.server.URL, ClientID: "client_test", AccessToken: "access_old", RefreshToken: "refresh_old", ExpiresAt: time.Now().Add(-time.Minute)})
	keychain := &fakeKeychain{value: string(legacy)}
	store := hubclient.SealedFileCredentialStore{Path: filepath.Join(dir, "agent-access.enc"), Keychain: keychain}
	lockPath := filepath.Join(dir, "agent-access.lock")
	// Both CLIs read the legacy credential before either migrates it.
	clis := make([]*hubAgentCLI, 2)
	snapshots := make([]hubAgentCredential, 2)
	for i := range clis {
		clis[i] = newTestHubCLI(io.Discard, store, fixture.server.Client(), lockPath)
		snapshot, err := clis[i].loadCredential(context.Background())
		if err != nil || snapshot.AccessToken != "access_old" {
			t.Fatalf("legacy load = %+v, %v", snapshot, err)
		}
		snapshots[i] = snapshot
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range clis {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := clis[i].refreshIfNeeded(context.Background(), snapshots[i], false)
			if err == nil {
				if _, status, sendErr := clis[i].sendMCP(context.Background(), credential, "ping", map[string]any{}); sendErr != nil || status != http.StatusOK {
					err = fmt.Errorf("ping: HTTP %d %v", status, sendErr)
				}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ping during migration = %v", err)
		}
	}
	if fixture.refreshCount() != 1 || keychain.saves != 1 || len(keychain.value) != 64 {
		t.Fatalf("migration: refreshes=%d key saves=%d key length=%d", fixture.refreshCount(), keychain.saves, len(keychain.value))
	}
	if stored := storedTestHubCredential(t, store); stored.RefreshToken != "refresh_1" {
		t.Fatalf("migrated credential refresh token = %q", stored.RefreshToken)
	}
	if _, err := os.Stat(store.Path); err != nil {
		t.Fatalf("sealed credential file missing: %v", err)
	}
}
