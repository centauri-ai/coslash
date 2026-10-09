package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type testCredentialStore struct {
	mu           sync.Mutex
	value        string
	saveFailures int
	saveAttempts int
}

func (store *testCredentialStore) Load(context.Context) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.value, nil
}

func (store *testCredentialStore) Save(_ context.Context, value string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saveAttempts++
	if store.saveFailures > 0 {
		store.saveFailures--
		return fmt.Errorf("synthetic credential store failure")
	}
	store.value = value
	return nil
}

func (store *testCredentialStore) Delete(context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.value = ""
	return nil
}

func holdTestRuntime(t *testing.T, baseURL string) {
	t.Helper()
	runtimeLock, err := acquireRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := acquireRuntimeReadiness()
	if err != nil {
		runtimeLock.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = readiness.Close()
		_ = runtimeLock.Close()
	})
	if err := writeRuntime(baseURL); err != nil {
		t.Fatal(err)
	}
	if err := writeToken("local-token"); err != nil {
		t.Fatal(err)
	}
}

func setTestConnectContext(t *testing.T, factory func() (context.Context, context.CancelFunc)) {
	t.Helper()
	previous := connectSignalContext
	connectSignalContext = factory
	t.Cleanup(func() { connectSignalContext = previous })
}

func TestConnectArgumentsValidateCodeAndHubOrigin(t *testing.T) {
	opts, help, err := parseConnectArgs([]string{"k7qx-29pd", "--hub", "https://beta.coslash.io", "--json"})
	if err != nil || help || opts.code != "k7qx-29pd" || opts.hub != "https://beta.coslash.io" || !opts.json {
		t.Fatalf("options=%#v help=%t error=%v", opts, help, err)
	}
	for _, args := range [][]string{
		{"K7QX-29PI", "--hub", "https://beta.coslash.io"},
		{"K7QX-29PD", "--hub", "http://example.com"},
		{"K7QX-29PD", "--hub", "https://beta.coslash.io/path"},
		{"K7QX-29PD", "--hub", "https://beta.coslash.io?"},
		{"K7QX-29PD"},
	} {
		if _, _, err := parseConnectArgs(args); err == nil {
			t.Fatalf("parseConnectArgs(%q) succeeded", args)
		}
	}
}

func TestConnectTerminalFailureLinesAndExitCodes(t *testing.T) {
	for _, test := range []struct {
		code int
		want string
	}{
		{code: connectExitInvalid, want: "This connect code is invalid or expired. Get a new command in Hub.\n"},
		{code: connectExitUnreachable, want: "Couldn't reach https://hub.coslash.io. Check your connection and run the command again.\n"},
		{code: connectExitDeclined, want: "This setup was declined in Hub. Nothing was connected.\n"},
		{code: connectExitUnsupported, want: "This Hub doesn't support connect codes yet. Use Devices → Add device in Hub.\n"},
	} {
		var stdout, stderr bytes.Buffer
		got := printConnectFailure(&stdout, &stderr, connectOptions{hub: "https://hub.coslash.io"}, test.code, "")
		if got != test.code || stderr.String() != test.want || stdout.Len() != 0 {
			t.Fatalf("connect failure code=%d line=%q stdout=%q return=%d", test.code, stderr.String(), stdout.String(), got)
		}
	}
}

func TestConnectTerminalPairingStatesAreSanitizedForCLI(t *testing.T) {
	for _, test := range []struct {
		code  int
		state string
	}{
		{code: connectExitCredentialStore, state: connectJobCredentialStoreFailed},
		{code: connectExitRetryable, state: connectJobCheckInRetryable},
	} {
		var stdout, stderr bytes.Buffer
		got := printConnectFailure(&stdout, &stderr, connectOptions{json: true}, test.code, "internal detail")
		var result struct {
			State    string `json:"state"`
			ExitCode int    `json:"exitCode"`
		}
		if got != test.code || json.Unmarshal(stdout.Bytes(), &result) != nil || result.State != test.state ||
			result.ExitCode != test.code || strings.Contains(stdout.String(), "internal detail") || stderr.Len() != 0 {
			t.Fatal("CLI returned an invalid or unsanitized terminal state")
		}
	}
}

func TestConnectClaimStoresOnlyHubOriginAndStartsApprovalPolling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	var claimBody map[string]string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/device-onboarding-codes/claim":
			if err := json.NewDecoder(request.Body).Decode(&claimBody); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":"10000000-0000-4000-8000-000000000152","deviceCode":"device-code","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`)
		case "/v1/device-authorizations/token":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"code":"pairing_pending"}`)
		default:
			t.Errorf("unexpected Hub request %s %s", request.Method, request.URL.Path)
			http.NotFound(w, request)
		}
	}))
	defer hub.Close()

	manager := newOnboardingManager("0.1.0")
	id, err := manager.StartConnectCode(hub.URL, "K7QX-29PD")
	if err != nil || id == "" {
		t.Fatalf("StartConnectCode id=%q error=%v", id, err)
	}
	defer manager.Close()
	if state, ok := manager.ConnectJobState(id); !ok || state != "claimed" {
		t.Fatalf("connect state=%q found=%t", state, ok)
	}
	if claimBody["connectCode"] != "K7QX-29PD" || claimBody["deviceName"] == "" || len(claimBody) != 2 {
		t.Fatalf("claim body=%#v", claimBody)
	}
	savedHub, err := os.ReadFile(filepath.Join(home, storedHubURLFilename))
	if err != nil || string(savedHub) != hub.URL+"\n" || strings.Contains(string(savedHub), "K7QX-29PD") {
		t.Fatalf("saved Hub=%q error=%v", savedHub, err)
	}
	assertConnectCodeNotOnDisk(t, home, "K7QX-29PD")
}

func assertConnectCodeNotOnDisk(t *testing.T, root, code string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(code)) {
			return fmt.Errorf("connect code was persisted in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConnectCodeIsNotLoggedOrPersistedOnClaimFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousLogOutput)

	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"code":"connect_code_invalid"}`)
	}))
	defer hub.Close()
	manager := newOnboardingManager("0.1.0")
	defer manager.Close()
	if _, err := manager.StartConnectCode(hub.URL, "K7QX-29PD"); !errors.Is(err, hubclient.ErrConnectCodeInvalid) {
		t.Fatalf("StartConnectCode error=%v, want invalid code", err)
	}
	assertConnectCodeNotOnDisk(t, home, "K7QX-29PD")
	if strings.Contains(logs.String(), "K7QX-29PD") {
		t.Fatalf("connect code appeared in logs: %q", logs.String())
	}
}

func TestConnectApprovalWaitsAndTracksTerminalState(t *testing.T) {
	for _, test := range []struct {
		name         string
		problem      string
		want         string
		wantEnsure   bool
		wantCheckIn  bool
		saveFailures int
	}{
		{name: "connected", want: connectJobConnected, wantEnsure: true, wantCheckIn: true},
		{name: "declined", problem: "onboarding_declined", want: "declined"},
		{name: "expired", problem: "pairing_expired", want: "expired"},
		{name: "credential-store-failed", want: connectJobCredentialStoreFailed, saveFailures: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("COSLASH_HOME", t.TempDir())
			var ensureCalls atomic.Int32
			var polls atomic.Int32
			var checkIns atomic.Int32
			credentials := &testCredentialStore{saveFailures: test.saveFailures}
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/v1/device-onboarding-codes/claim":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = fmt.Fprint(w, `{"id":"10000000-0000-4000-8000-000000000152","deviceCode":"device-code","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":1}`)
				case "/v1/device-authorizations/token":
					polls.Add(1)
					if test.problem != "" {
						w.Header().Set("Content-Type", "application/problem+json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = fmt.Fprintf(w, `{"code":%q}`, test.problem)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"deviceId":"device","credential":"saved-credential","tokenType":"Device","scope":"ingest"}`)
				case "/v4/devices/me/check-in":
					checkIns.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"nextCheckInSeconds":60}`)
				default:
					t.Errorf("unexpected Hub request %s %s", request.Method, request.URL.Path)
					http.NotFound(w, request)
				}
			}))
			defer hub.Close()

			manager := newOnboardingManager("0.1.0")
			manager.SetV4SyncActive(true)
			manager.setHubClientBinder(func(client *hubclient.Client) { client.Credentials = credentials })
			manager.setSyncHooks(syncHookFuncs{ensure: func(*hubclient.Client) error {
				ensureCalls.Add(1)
				if checkIns.Load() == 0 {
					t.Error("sync started before a successful first check-in")
				}
				return nil
			}})
			id, err := manager.StartConnectCode(hub.URL, "K7QX-29PD")
			if err != nil {
				manager.Close()
				t.Fatal(err)
			}
			deadline := time.Now().Add(4 * time.Second)
			state := ""
			for time.Now().Before(deadline) {
				state, _ = manager.ConnectJobState(id)
				if state == test.want {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			manager.Close()
			if state != test.want || polls.Load() == 0 {
				t.Fatalf("connect state=%q polls=%d, want %q", state, polls.Load(), test.want)
			}
			wantEnsureCalls := int32(0)
			if test.wantEnsure {
				wantEnsureCalls = 1
			}
			if got := ensureCalls.Load(); got != wantEnsureCalls {
				t.Fatalf("sync Ensure calls=%d, want %d", got, wantEnsureCalls)
			}
			if test.wantEnsure {
				if value, _ := credentials.Load(context.Background()); value == "" {
					t.Fatal("credential store did not retain the pairing result")
				}
			}
			wantCheckIns := int32(0)
			if test.wantCheckIn {
				wantCheckIns = 1
			}
			if got := checkIns.Load(); got != wantCheckIns {
				t.Fatalf("unexpected first check-in count: got %d, want %d", got, wantCheckIns)
			}
			wantSaveAttempts := 0
			if test.saveFailures > 0 {
				wantSaveAttempts = 3
			} else if test.wantCheckIn {
				wantSaveAttempts = 1
			}
			credentials.mu.Lock()
			saveAttempts := credentials.saveAttempts
			credentials.mu.Unlock()
			if saveAttempts != wantSaveAttempts {
				t.Fatal("credential-store retry count was outside the expected bound")
			}
		})
	}
}

func TestEnsureBackgroundLocalSpawnsOrForwards(t *testing.T) {
	t.Run("forwards to existing Local", func(t *testing.T) {
		t.Setenv("COSLASH_HOME", t.TempDir())
		local := httptest.NewServer(http.NotFoundHandler())
		defer local.Close()
		holdTestRuntime(t, local.URL)
		startCalled := false
		client, alreadyRunning, err := ensureBackgroundLocalWith(func() error {
			startCalled = true
			return nil
		})
		if err != nil || client == nil || !alreadyRunning || startCalled {
			t.Fatalf("client=%v alreadyRunning=%t startCalled=%t error=%v", client, alreadyRunning, startCalled, err)
		}
	})
	t.Run("starts Local in the background", func(t *testing.T) {
		t.Setenv("COSLASH_HOME", t.TempDir())
		local := httptest.NewServer(http.NotFoundHandler())
		defer local.Close()
		var runtimeLock, readiness *os.File
		startCalled := false
		client, alreadyRunning, err := ensureBackgroundLocalWith(func() error {
			startCalled = true
			var err error
			runtimeLock, err = acquireRuntimeLock()
			if err != nil {
				return err
			}
			readiness, err = acquireRuntimeReadiness()
			if err != nil {
				return err
			}
			if err := writeRuntime(local.URL); err != nil {
				return err
			}
			return writeToken("local-token")
		})
		if readiness != nil {
			defer readiness.Close()
		}
		if runtimeLock != nil {
			defer runtimeLock.Close()
		}
		if err != nil || client == nil || alreadyRunning || !startCalled {
			t.Fatalf("client=%v alreadyRunning=%t startCalled=%t error=%v", client, alreadyRunning, startCalled, err)
		}
	})
}

func TestConnectCLIWaitsForApprovalAndHandlesInterrupt(t *testing.T) {
	t.Run("connected after approval", func(t *testing.T) {
		t.Setenv("COSLASH_HOME", t.TempDir())
		var statusPolls atomic.Int32
		local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.Header.Get("X-Coslash-Token") != "local-token" {
				t.Errorf("missing Local API token")
			}
			w.Header().Set("Content-Type", "application/json")
			switch {
			case request.Method == http.MethodPost && request.URL.Path == "/api/hub/onboarding/connect":
				_, _ = fmt.Fprint(w, `{"id":"job","state":"claimed"}`)
			case request.Method == http.MethodGet && request.URL.Path == "/api/hub/onboarding/connect/job":
				if statusPolls.Add(1) == 1 {
					_, _ = fmt.Fprint(w, `{"state":"claimed"}`)
				} else {
					_, _ = fmt.Fprint(w, `{"state":"connected"}`)
				}
			default:
				t.Errorf("unexpected Local request %s %s", request.Method, request.URL.Path)
				http.NotFound(w, request)
			}
		}))
		defer local.Close()
		holdTestRuntime(t, local.URL)
		setTestConnectContext(t, func() (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		})
		var stdout, stderr bytes.Buffer
		code := runConnectCLI(&stdout, &stderr, []string{"K7QX-29PD", "--hub", "https://hub.coslash.io"})
		if code != connectExitOK || !strings.Contains(stdout.String(), "✓ Connected. Your recent sessions are syncing to My space.") ||
			statusPolls.Load() < 2 || stderr.Len() != 0 {
			t.Fatalf("exit=%d polls=%d stdout=%q stderr=%q", code, statusPolls.Load(), stdout.String(), stderr.String())
		}
	})
	t.Run("interrupt leaves approval running in Local", func(t *testing.T) {
		t.Setenv("COSLASH_HOME", t.TempDir())
		var statusPolls atomic.Int32
		local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet {
				statusPolls.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"job","state":"claimed"}`)
		}))
		defer local.Close()
		holdTestRuntime(t, local.URL)
		setTestConnectContext(t, func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		})
		var stdout, stderr bytes.Buffer
		code := runConnectCLI(&stdout, &stderr, []string{"K7QX-29PD", "--hub", "https://hub.coslash.io"})
		if code != connectExitOK || !strings.Contains(stdout.String(), "coSlash Local keeps waiting in the background. Finish in Hub.") ||
			statusPolls.Load() != 0 || stderr.Len() != 0 {
			t.Fatalf("exit=%d polls=%d stdout=%q stderr=%q", code, statusPolls.Load(), stdout.String(), stderr.String())
		}
	})
}

func TestConnectClaimMapsInvalidUnsupportedAndUnreachable(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		problem string
		want    error
	}{
		{name: "invalid", status: http.StatusNotFound, problem: "connect_code_invalid", want: hubclient.ErrConnectCodeInvalid},
		{name: "unsupported", status: http.StatusNotFound, problem: "not_found", want: hubclient.ErrConnectUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("COSLASH_HOME", t.TempDir())
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(test.status)
				_, _ = fmt.Fprintf(w, `{"code":%q}`, test.problem)
			}))
			defer hub.Close()
			manager := newOnboardingManager("0.1.0")
			defer manager.Close()
			_, err := manager.StartConnectCode(hub.URL, "K7QX-29PD")
			if !errors.Is(err, test.want) {
				t.Fatalf("StartConnectCode error=%v, want %v", err, test.want)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		t.Setenv("COSLASH_HOME", t.TempDir())
		hub := httptest.NewServer(http.NotFoundHandler())
		origin := hub.URL
		hub.Close()
		manager := newOnboardingManager("0.1.0")
		defer manager.Close()
		_, err := manager.StartConnectCode(origin, "K7QX-29PD")
		if err == nil || errors.Is(err, hubclient.ErrConnectCodeInvalid) || errors.Is(err, hubclient.ErrConnectUnsupported) {
			t.Fatalf("StartConnectCode error=%v, want unreachable error", err)
		}
	})
}

func TestWakeRequestMatchesStoredHubAndRateLimits(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	const storedHub = "https://beta.coslash.io"
	if err := writeStoredHubURL(storedHub); err != nil {
		t.Fatal(err)
	}
	called := 0
	manager := newOnboardingManager("0.1.0")
	defer manager.Close()
	manager.setSyncHooks(syncHookFuncs{pass: func(reason string) {
		if reason != "wake" {
			t.Errorf("RequestPass reason=%q, want wake", reason)
		}
		called++
	}})
	if err := manager.RequestWake("https://foreign.example"); !errors.Is(err, errForeignWakeHub) {
		t.Fatalf("foreign wake error=%v", err)
	}
	if err := manager.RequestWake(storedHub); err != nil {
		t.Fatal(err)
	}
	if err := manager.RequestWake(storedHub); !errors.Is(err, errWakeRateLimited) {
		t.Fatalf("repeat wake error=%v", err)
	}
	if called != 1 {
		t.Fatalf("RequestPass calls=%d, want 1", called)
	}
}

func TestBackgroundOptionsDisableBrowser(t *testing.T) {
	opts, err := parseOptions([]string{"--background"})
	if err != nil || !opts.background || !opts.noOpen {
		t.Fatalf("options=%#v error=%v", opts, err)
	}
}

func TestBackgroundLogRotatesAtFiveMiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), backgroundLogName)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), backgroundLogSize), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer := &rotatingLogWriter{path: path, file: file, size: backgroundLogSize}
	if _, err := writer.Write([]byte("next log line\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	rotated, err := os.Stat(path + ".1")
	if err != nil || rotated.Size() != backgroundLogSize {
		t.Fatalf("rotated log size=%v error=%v", rotated, err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != "next log line\n" {
		t.Fatalf("current log=%q error=%v", current, err)
	}
}

func TestConnectStatusRouteReturnsOnlyState(t *testing.T) {
	for _, state := range []string{connectJobConnected, connectJobRetrying, connectJobCredentialStoreFailed} {
		t.Run(state, func(t *testing.T) {
			manager := newOnboardingManager("0.1.0")
			defer manager.Close()
			manager.connectJobs["job"] = connectJob{state: state, updatedAt: time.Now()}
			api := http.NewServeMux()
			registerHubRoutes(api, nil, nil, manager)
			response := httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/hub/onboarding/connect/job", nil))
			var body map[string]string
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil ||
				body["state"] != state || len(body) != 1 {
				t.Fatalf("status route returned an invalid state-only response: status=%d", response.Code)
			}
		})
	}
}

func TestConnectAndWakeRoutesRequireLocalToken(t *testing.T) {
	manager := newOnboardingManager("0.1.0")
	defer manager.Close()
	api := http.NewServeMux()
	registerHubRoutes(api, nil, nil, manager)
	handler := httpsec.Guard{Addr: "127.0.0.1:8787", Token: "local-token"}.Wrap(api)
	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/hub/onboarding/connect", body: `{"hubUrl":"https://hub.coslash.io","connectCode":"K7QX-29PD"}`},
		{method: http.MethodPost, path: "/api/hub/wake", body: `{"hubUrl":"https://hub.coslash.io"}`},
	} {
		request := httptest.NewRequest(test.method, "http://127.0.0.1:8787"+test.path, strings.NewReader(test.body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d, want unauthorized", test.method, test.path, response.Code)
		}
	}
}

func TestWakeProtocolForwardsThroughTokenGuardedLocalAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	var gotPath, gotToken string
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gotPath, gotToken = request.URL.Path, request.Header.Get("X-Coslash-Token")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer local.Close()
	runtimeLock, err := acquireRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeLock.Close()
	readiness, err := acquireRuntimeReadiness()
	if err != nil {
		t.Fatal(err)
	}
	defer readiness.Close()
	if err := writeRuntime(local.URL); err != nil {
		t.Fatal(err)
	}
	if err := writeToken("local-token"); err != nil {
		t.Fatal(err)
	}
	hub, _ := url.Parse("https://hub.coslash.io")
	if err := forwardProtocolActivation(&hubclient.LaunchIntent{Action: "wake", HubURL: hub}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/hub/wake" || gotToken != "local-token" {
		t.Fatalf("wake forwarded to %q with token %q", gotPath, gotToken)
	}
}

func TestConnectHubOriginRejectsUnexpectedPath(t *testing.T) {
	if _, err := hubOrigin("https://hub.coslash.io/path"); err == nil {
		t.Fatal("hubOrigin accepted a path")
	}
}
