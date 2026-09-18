// CLI command contract and loopback API tests.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestRuntimeRoundTripKeepsTokenSeparate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := writeToken("secret"); err != nil {
		t.Fatal(err)
	}
	if err := writeRuntime("http://127.0.0.1:4321"); err != nil {
		t.Fatal(err)
	}

	baseURL, token, err := readRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "http://127.0.0.1:4321" || token != "secret" {
		t.Fatalf("runtime = %q/%q", baseURL, token)
	}
	data, err := os.ReadFile(home + "/runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret")) {
		t.Fatalf("runtime descriptor contains token: %s", data)
	}
	if err := os.WriteFile(home+"/runtime.json", []byte(`{"baseURL":"https://example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readRuntime(); err == nil {
		t.Fatal("readRuntime accepted a non-loopback URL")
	}
}

func TestRuntimeLockAllowsOnlyOneServer(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	first, err := acquireRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRuntimeLock(); err == nil {
		first.Close()
		t.Fatal("second server acquired the runtime lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireRuntimeLock()
	if err != nil {
		t.Fatalf("lock remained held after close: %v", err)
	}
	third.Close()
}

func TestRunSessionsFiltersUIFieldsAndPrintsJSON(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Coslash-Token")
		if r.URL.Path != "/api/sessions" || r.URL.Query().Get("sourceAware") != "" {
			t.Fatalf("request = %s", r.URL.String())
		}
		io.WriteString(w, `[
			{"agent":"codex","id":"one","name":"Auth work","repo":"centauri/coslash","branch":"main","cwd":"/secret/path","firstPrompt":"SECRET"},
			{"agent":"claude","id":"two","name":"Docs","repo":"other/repo","branch":"docs"}
		]`)
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")

	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"sessions", "AUTH", "--json"}); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	var sessions []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if len(sessions) != 1 || sessions[0]["id"] != "one" || sessions[0]["selector"] != "codex:one" || gotToken != "secret" {
		t.Fatalf("sessions = %#v, token = %q", sessions, gotToken)
	}
	if _, present := sessions[0]["firstPrompt"]; present {
		t.Fatalf("sessions exposed transcript content: %#v", sessions)
	}
	if _, present := sessions[0]["cwd"]; present {
		t.Fatalf("sessions exposed working directory: %#v", sessions)
	}
}

func TestSessionMatchesOnlyUISearchFields(t *testing.T) {
	name, repo, branch := "Auth work", "centauri/coslash", "feature/login"
	value := session.Session{
		Agent: "codex", Name: &name, Repository: &repo, Branch: &branch,
		WorkingDirectory: "/private/workspace",
	}
	for _, query := range []string{"auth", "CENTAURI", "LOGIN", "CODEX"} {
		if !sessionMatches(value, query) {
			t.Fatalf("query %q did not match", query)
		}
	}
	for _, query := range []string{"/private/workspace"} {
		if sessionMatches(value, query) {
			t.Fatalf("query %q matched a private field", query)
		}
	}
}

func TestParseLocalSessionSelector(t *testing.T) {
	agent, id, ok := parseLocalSessionSelector("codex:session-1")
	if !ok || agent != "codex" || id != "session-1" {
		t.Fatalf("selector = %q/%q/%v", agent, id, ok)
	}
	for _, value := range []string{"session-1", "cursor:session-1", "codex:"} {
		if _, _, ok := parseLocalSessionSelector(value); ok {
			t.Fatalf("accepted selector %q", value)
		}
	}
}

func TestRunHandoffAndSendPreserveServerOutcomes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/handoff":
			if r.URL.Query().Get("agent") != "codex" || r.URL.Query().Get("id") != "session-1" {
				t.Fatalf("handoff identity = %q/%q", r.URL.Query().Get("agent"), r.URL.Query().Get("id"))
			}
			io.WriteString(w, "# Handoff — Test\n")
		case "/api/send":
			body, _ := io.ReadAll(r.Body)
			if r.URL.Query().Get("agent") != "codex" || r.URL.Query().Get("id") != "session-1" || r.URL.Query().Get("to") != "claude" || string(body) != "fix it" {
				t.Fatalf("send = %s, body = %q", r.URL.String(), body)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")

	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"handoff", "codex:session-1"}); code != 0 || stdout.String() != "# Handoff — Test\n" {
		t.Fatalf("handoff code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCLI(&stdout, &stderr, []string{"send", "codex:session-1", "--to", "claude", "fix it"}); code != 0 {
		t.Fatalf("send code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Success:") {
		t.Fatalf("send stdout = %q", stdout.String())
	}
}

func TestRunReviewPreservesServerOutcomes(t *testing.T) {
	reviewer := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/reviews" ||
			r.URL.Query().Get("source") != "local" || r.URL.Query().Get("agent") != "codex" || r.URL.Query().Get("id") != "session-1" ||
			r.URL.Query().Get("reviewer") != reviewer {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")

	for _, selected := range []string{"claude", "codex", "opencode"} {
		reviewer = selected
		var stdout, stderr bytes.Buffer
		if code := runCLI(&stdout, &stderr, []string{"review", "codex:session-1", "--with", selected}); code != 0 {
			t.Fatalf("reviewer = %q, code = %d, stderr = %q", selected, code, stderr.String())
		}
		if got := stdout.String(); got != "Success: started "+selected+" review for session codex:session-1\n" {
			t.Fatalf("reviewer = %q, stdout = %q", selected, got)
		}
	}
}

func TestRunReviewRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"review"},
		{"review", "session-1"},
		{"review", "session-1", "--with", "codex"},
		{"review", "codex:session-1", "--with"},
		{"review", "codex:session-1", "--with", "cursor"},
		{"review", "codex:session-1", "--with", "codex", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(&stdout, &stderr, args); code != 1 || !strings.HasPrefix(stderr.String(), "Error: ") {
			t.Fatalf("args = %#v, code = %d, stderr = %q", args, code, stderr.String())
		}
	}
}

func TestRunReviewReportsAPIError(t *testing.T) {
	for _, message := range []string{
		"reviewer is not installed or supported",
		"session not found",
		"review already running",
	} {
		t.Run(message, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, message, http.StatusConflict)
			}))
			defer server.Close()
			writeTestRuntime(t, server.URL, "secret")

			var stdout, stderr bytes.Buffer
			if code := runCLI(&stdout, &stderr, []string{"review", "codex:session-1", "--with", "codex"}); code != 1 {
				t.Fatalf("code = %d", code)
			}
			if got := stderr.String(); got != "Error: "+message+"\n" {
				t.Fatalf("stderr = %q", got)
			}
		})
	}
}

func TestRunCLIReportsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "session not found", http.StatusNotFound)
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")

	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"handoff", "codex:missing"}); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if got := stderr.String(); !strings.Contains(got, "Error: session not found") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestHandleHandoffAndSendUseCanonicalSession(t *testing.T) {
	name := "Test"
	found := &session.Session{Agent: "codex", ID: "session-1", Name: &name, WorkingDirectory: "/workspace"}
	getSession := func(agent, id string) (*session.Session, error) {
		if agent != found.Agent || id != found.ID {
			t.Fatalf("identity = %q/%q", agent, id)
		}
		return found, nil
	}

	response := httptest.NewRecorder()
	handleHandoff(response, httptest.NewRequest(http.MethodGet, "/api/handoff?agent=codex&id=session-1", nil), getSession)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "# Handoff — Test") {
		t.Fatalf("handoff response = %d %q", response.Code, response.Body.String())
	}

	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store := settings.Open()
	var launched []string
	open := func(_ context.Context, terminal, agent, cwd, sessionID, mode, handoff, prompt string) error {
		launched = []string{terminal, agent, cwd, sessionID, mode, handoff, prompt}
		return nil
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/send?agent=codex&id=session-1&to=claude", strings.NewReader("fix it"))
	handleSend(response, request, store, getSession, func(string) bool { return true }, open)
	if response.Code != http.StatusNoContent {
		t.Fatalf("send response = %d %q", response.Code, response.Body.String())
	}
	if len(launched) != 7 || launched[1] != "claude" || launched[2] != "/workspace" ||
		launched[4] != launch.NewSession || !strings.Contains(launched[5], "# Handoff — Test") || launched[6] != "fix it" {
		t.Fatalf("launch = %#v", launched)
	}
}

func TestHandleSendDoesNotLaunchAfterRequestCancellation(t *testing.T) {
	name := "Test"
	found := &session.Session{Agent: "codex", ID: "session-1", Name: &name, WorkingDirectory: "/workspace"}
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store := settings.Open()
	launched := false
	open := func(context.Context, string, string, string, string, string, string, string) error {
		launched = true
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/send?agent=codex&id=session-1&to=claude", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handleSend(response, request, store, func(string, string) (*session.Session, error) {
		return found, nil
	}, func(string) bool { return true }, open)
	if launched {
		t.Fatal("canceled request launched an agent")
	}
}

type unavailableWorkingDirectoryError struct{}

func (unavailableWorkingDirectoryError) Error() string {
	return "launch: working directory is unavailable"
}

func (unavailableWorkingDirectoryError) Is(target error) bool {
	return target.Error() == "launch: working directory is unavailable"
}

func TestHandleSendReportsUnavailableWorkingDirectory(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	response := httptest.NewRecorder()
	handleSend(
		response,
		httptest.NewRequest(http.MethodPost, "/api/send?agent=codex&id=session-1&to=claude", nil),
		settings.Open(),
		func(string, string) (*session.Session, error) {
			return &session.Session{Agent: "codex", ID: "session-1", WorkingDirectory: "/missing"}, nil
		},
		func(string) bool { return true },
		func(context.Context, string, string, string, string, string, string, string) error {
			return unavailableWorkingDirectoryError{}
		},
	)
	want := "session working directory is unavailable\n"
	if response.Code != http.StatusConflict || response.Body.String() != want {
		t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body.String(), http.StatusConflict, want)
	}
}

func TestHandleSendRejectsUnavailableTarget(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	launched := false
	response := httptest.NewRecorder()
	handleSend(
		response,
		httptest.NewRequest(http.MethodPost, "/api/send?id=session-1&to=codex", nil),
		settings.Open(),
		func(string, string) (*session.Session, error) {
			return &session.Session{Agent: "claude", ID: "session-1", WorkingDirectory: "/workspace"}, nil
		},
		func(string) bool { return false },
		func(context.Context, string, string, string, string, string, string, string) error {
			launched = true
			return nil
		},
	)
	if response.Code != http.StatusBadRequest || response.Body.String() != "target is not installed or supported\n" || launched {
		t.Fatalf("response = %d %q, launched = %v", response.Code, response.Body.String(), launched)
	}
}

func TestHandleSendRejectsOversizedGeneratedHandoff(t *testing.T) {
	prompt := strings.Repeat("x", launch.MaxHandoffBytes)
	found := &session.Session{
		Agent: "codex", ID: "session-1", WorkingDirectory: "/workspace",
		SessionDetails: session.SessionDetails{FirstPrompt: &prompt},
	}
	t.Setenv("COSLASH_HOME", t.TempDir())
	launched := false
	response := httptest.NewRecorder()
	handleSend(
		response,
		httptest.NewRequest(http.MethodPost, "/api/send?agent=codex&id=session-1&to=claude", nil),
		settings.Open(),
		func(string, string) (*session.Session, error) { return found, nil },
		func(string) bool { return true },
		func(context.Context, string, string, string, string, string, string, string) error {
			launched = true
			return nil
		},
	)
	if response.Code != http.StatusRequestEntityTooLarge || launched {
		t.Fatalf("response = %d %q, launched = %v", response.Code, response.Body.String(), launched)
	}
}

func TestHandleSendHidesInvalidSettingsDetails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := os.WriteFile(home+"/settings.json", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handleSend(
		response,
		httptest.NewRequest(http.MethodPost, "/api/send?id=session-1&to=claude", nil),
		settings.Open(),
		func(string, string) (*session.Session, error) {
			t.Fatal("loaded session with invalid settings")
			return nil, nil
		},
		func(string) bool { return true },
		func(context.Context, string, string, string, string, string, string, string) error { return nil },
	)
	want := "settings are invalid; open Settings to repair them\n"
	if response.Code != http.StatusConflict || response.Body.String() != want {
		t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body.String(), http.StatusConflict, want)
	}
}

func TestCanonicalSessionUsesListedNameAndSynthesis(t *testing.T) {
	name := "Resolved name"
	value := &session.Session{ID: "session-1", Name: &name, LastActivityTime: 42}
	mgr := synthesis.NewManager(nil)
	found, err := canonicalSession("codex", "session-1", mgr, func(agent, id string, revision int64) (*session.Session, error) {
		if agent != "codex" || id != "session-1" || revision != 0 {
			t.Fatalf("load = %q/%q/%d", agent, id, revision)
		}
		return value, nil
	})
	if err != nil || found == nil || found.Name == nil || *found.Name != name {
		t.Fatalf("session = %#v, err = %v", found, err)
	}
}

func writeTestRuntime(t *testing.T, baseURL, token string) {
	t.Helper()
	t.Setenv("COSLASH_HOME", t.TempDir())
	if err := writeToken(token); err != nil {
		t.Fatal(err)
	}
	if err := writeRuntime(baseURL); err != nil {
		t.Fatal(err)
	}
}
