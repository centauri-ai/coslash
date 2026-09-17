// CLI command contract and loopback API tests.
package main

import (
	"bytes"
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
	if len(sessions) != 1 || sessions[0]["id"] != "one" || gotToken != "secret" {
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

func TestRunHandoffAndSendPreserveServerOutcomes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/handoff":
			if r.URL.Query().Get("id") != "session-1" {
				t.Fatalf("handoff id = %q", r.URL.Query().Get("id"))
			}
			io.WriteString(w, "# Handoff — Test\n")
		case "/api/send":
			body, _ := io.ReadAll(r.Body)
			if r.URL.Query().Get("id") != "session-1" || r.URL.Query().Get("to") != "claude" || string(body) != "fix it" {
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
	if code := runCLI(&stdout, &stderr, []string{"handoff", "session-1"}); code != 0 || stdout.String() != "# Handoff — Test\n" {
		t.Fatalf("handoff code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCLI(&stdout, &stderr, []string{"send", "session-1", "--to", "claude", "fix it"}); code != 0 {
		t.Fatalf("send code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Success:") {
		t.Fatalf("send stdout = %q", stdout.String())
	}
}

func TestRunCLIReportsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "session not found", http.StatusNotFound)
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")

	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"handoff", "missing"}); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if got := stderr.String(); !strings.Contains(got, "Error: session not found") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestHandleHandoffAndSendUseCanonicalSession(t *testing.T) {
	name := "Test"
	found := &session.Session{Agent: "codex", ID: "session-1", Name: &name, WorkingDirectory: "/workspace"}
	getSession := func(id string) (*session.Session, error) {
		if id != found.ID {
			t.Fatalf("id = %q", id)
		}
		return found, nil
	}

	response := httptest.NewRecorder()
	handleHandoff(response, httptest.NewRequest(http.MethodGet, "/api/handoff?id=session-1", nil), getSession)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "# Handoff — Test") {
		t.Fatalf("handoff response = %d %q", response.Code, response.Body.String())
	}

	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store := settings.Open()
	var launched []string
	open := func(terminal, agent, cwd, sessionID, mode, handoff, prompt string) error {
		launched = []string{terminal, agent, cwd, sessionID, mode, handoff, prompt}
		return nil
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/send?id=session-1&to=claude", strings.NewReader("fix it"))
	handleSend(response, request, store, getSession, open)
	if response.Code != http.StatusNoContent {
		t.Fatalf("send response = %d %q", response.Code, response.Body.String())
	}
	if len(launched) != 7 || launched[1] != "claude" || launched[2] != "/workspace" ||
		launched[4] != launch.NewSession || !strings.Contains(launched[5], "# Handoff — Test") || launched[6] != "fix it" {
		t.Fatalf("launch = %#v", launched)
	}
}

func TestCanonicalSessionUsesListedNameAndSynthesis(t *testing.T) {
	name := "Resolved name"
	value := &session.Session{ID: "session-1", Name: &name, LastActivityTime: 42}
	mgr := synthesis.NewManager(nil)
	found, err := canonicalSession("session-1", mgr, func(int64) ([]*session.Session, error) {
		return []*session.Session{value}, nil
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
