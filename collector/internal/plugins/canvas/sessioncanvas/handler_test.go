package sessioncanvas

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessiondetail"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const sharedID = "11111111-1111-4111-8111-111111111111"

type fakeResolver struct {
	values map[contracts.SessionIdentity]ResolvedSession
	err    error
}

func (resolver *fakeResolver) Resolve(_ context.Context, identity contracts.SessionIdentity) (ResolvedSession, error) {
	if resolver.err != nil {
		return ResolvedSession{}, resolver.err
	}
	value, found := resolver.values[identity]
	if !found {
		return ResolvedSession{}, ErrSessionNotFound
	}
	return value, nil
}

type fakeProjector struct {
	err error
}

func (projector *fakeProjector) ProjectKnown(_ context.Context, known session.Session, _ string) (*sessiondetail.Detail, error) {
	if projector.err != nil {
		return nil, projector.err
	}
	return &sessiondetail.Detail{
		Session: known,
		TurnLog: []sessiondetail.Turn{{Index: 1, Prompt: "fix the bug", ToolUses: 2, FileEdits: []string{"a.go"}}},
	}, nil
}

type fakeRenamer struct {
	identities []contracts.SessionIdentity
	names      []string
	err        error
}

func (renamer *fakeRenamer) Rename(_ context.Context, identity contracts.SessionIdentity, name string) error {
	renamer.identities = append(renamer.identities, identity)
	renamer.names = append(renamer.names, name)
	return renamer.err
}

type fakeWorkspace struct{ called bool }

func (workspace *fakeWorkspace) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/canvas/workspaces/{agent}/{id}", func(w http.ResponseWriter, _ *http.Request) {
		workspace.called = true
		writeJSON(w, http.StatusOK, map[string]bool{"workspace": true})
	})
}

type fakeTerminals struct {
	statuses  map[string]contracts.TerminalStatus
	specs     []terminal.Spec
	statusErr error
	createErr error
}

func (terminals *fakeTerminals) Create(_ context.Context, spec terminal.Spec) (contracts.TerminalStatus, error) {
	if terminals.createErr != nil {
		return contracts.TerminalStatus{}, terminals.createErr
	}
	terminals.specs = append(terminals.specs, spec)
	status := contracts.TerminalStatus{TerminalID: spec.ID, State: "running", Writable: spec.Writable}
	if terminals.statuses == nil {
		terminals.statuses = map[string]contracts.TerminalStatus{}
	}
	terminals.statuses[spec.ID] = status
	return status, nil
}

func (terminals *fakeTerminals) Status(_ context.Context, id string) (contracts.TerminalStatus, error) {
	if terminals.statusErr != nil {
		return contracts.TerminalStatus{}, terminals.statusErr
	}
	status, found := terminals.statuses[id]
	if !found {
		return contracts.TerminalStatus{}, terminal.ErrNotFound
	}
	return status, nil
}

type fakeTerminalAPI struct{ calls []string }

func (api *fakeTerminalAPI) Status(w http.ResponseWriter, _ *http.Request, id string) {
	api.calls = append(api.calls, "status:"+id)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}
func (api *fakeTerminalAPI) Input(w http.ResponseWriter, _ *http.Request, id string) {
	api.calls = append(api.calls, "input:"+id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (api *fakeTerminalAPI) Stop(w http.ResponseWriter, _ *http.Request, id string) {
	api.calls = append(api.calls, "stop:"+id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (api *fakeTerminalAPI) WebSocket(w http.ResponseWriter, _ *http.Request, id string) {
	api.calls = append(api.calls, "ws:"+id)
	w.WriteHeader(http.StatusTeapot)
}

type fakeAnalyzer struct {
	result TurnAnalysis
	err    error
	calls  int
	input  TurnAnalysisInput
}

func (analyzer *fakeAnalyzer) Analyze(_ context.Context, input TurnAnalysisInput) (TurnAnalysis, error) {
	analyzer.calls++
	analyzer.input = input
	return analyzer.result, analyzer.err
}

type testServer struct {
	handler   *Handler
	resolver  *fakeResolver
	projector *fakeProjector
	renamer   *fakeRenamer
	workspace *fakeWorkspace
	terminals *fakeTerminals
	api       *fakeTerminalAPI
	analyzer  *fakeAnalyzer
	served    http.Handler
}

func newTestServer(t *testing.T, analyzer *fakeAnalyzer) *testServer {
	t.Helper()
	cwd := t.TempDir()
	resolver := &fakeResolver{values: map[contracts.SessionIdentity]ResolvedSession{}}
	for _, agent := range []string{"claude", "codex"} {
		identity := contracts.SessionIdentity{Agent: agent, ID: sharedID}
		resolver.values[identity] = ResolvedSession{Session: session.Session{Agent: agent, ID: sharedID, WorkingDirectory: cwd, SessionDetails: session.SessionDetails{LogPath: filepath.Join(cwd, agent+".jsonl")}}, TranscriptPath: filepath.Join(cwd, agent+".jsonl")}
	}
	server := &testServer{
		resolver: resolver, projector: &fakeProjector{}, renamer: &fakeRenamer{}, workspace: &fakeWorkspace{},
		terminals: &fakeTerminals{statuses: map[string]contracts.TerminalStatus{}}, api: &fakeTerminalAPI{}, analyzer: analyzer,
	}
	var turnAnalyzer TurnAnalyzer
	if analyzer != nil {
		turnAnalyzer = analyzer
	}
	var err error
	server.handler, err = New(Options{
		Sessions: resolver, Projector: server.projector, Renamer: server.renamer, Workspaces: server.workspace,
		Terminals: server.terminals, TerminalAPI: server.api, Analyzer: turnAnalyzer,
		NewUUID: func() (string, error) { return "22222222-2222-4222-8222-222222222222", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.handler.Register(mux)
	server.served = httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}.Wrap(mux)
	return server
}

func (server *testServer) request(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	request.Host = "127.0.0.1:8787"
	if authenticated {
		request.Header.Set("X-Coslash-Token", "secret")
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	server.served.ServeHTTP(response, request)
	return response
}

func TestDetailUsesCompositeIdentityBehindGuard(t *testing.T) {
	server := newTestServer(t, nil)
	if response := server.request(http.MethodGet, "/api/canvas/sessions/codex/"+sharedID, "", false); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	response := server.request(http.MethodGet, "/api/canvas/sessions/codex/"+sharedID, "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"agent":"codex"`) {
		t.Fatalf("detail = %d %s", response.Code, response.Body.String())
	}
	response = server.request(http.MethodGet, "/api/canvas/sessions/claude/"+sharedID, "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"agent":"claude"`) {
		t.Fatalf("duplicate id detail = %d %s", response.Code, response.Body.String())
	}
}

func TestInvalidUnknownAndMethodRequestsFailSafely(t *testing.T) {
	server := newTestServer(t, nil)
	for _, test := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/canvas/sessions/other/" + sharedID, http.StatusBadRequest},
		{http.MethodGet, "/api/canvas/sessions/codex/missing", http.StatusNotFound},
		{http.MethodDelete, "/api/canvas/sessions/codex/" + sharedID, http.StatusMethodNotAllowed},
	} {
		response := server.request(test.method, test.path, "", true)
		if response.Code != test.want {
			t.Errorf("%s %s = %d, want %d: %s", test.method, test.path, response.Code, test.want, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "/Users/") {
			t.Fatal("safe error leaked a private path")
		}
	}
}

func TestRenameValidationAndCompositeDispatch(t *testing.T) {
	server := newTestServer(t, nil)
	path := "/api/canvas/sessions/codex/" + sharedID + "/name"
	response := server.request(http.MethodPut, path, `{"name":"new\nname"}`, true)
	if response.Code != http.StatusBadRequest || len(server.renamer.names) != 0 {
		t.Fatalf("invalid rename = %d %s", response.Code, response.Body.String())
	}
	response = server.request(http.MethodPut, path, `{"name":"  New name  "}`, true)
	if response.Code != http.StatusOK || server.renamer.names[0] != "New name" || server.renamer.identities[0].Agent != "codex" {
		t.Fatalf("rename = %d %s %#v", response.Code, response.Body.String(), server.renamer)
	}
}

func TestTerminalUsesServerKnownSessionAndReusesIt(t *testing.T) {
	server := newTestServer(t, nil)
	path := "/api/canvas/sessions/claude/" + sharedID + "/terminal"
	response := server.request(http.MethodPost, path, `{}`, true)
	if response.Code != http.StatusOK || len(server.terminals.specs) != 1 {
		t.Fatalf("start = %d %s", response.Code, response.Body.String())
	}
	spec := server.terminals.specs[0]
	wantDir, err := filepath.EvalSymlinks(server.resolver.values[contracts.SessionIdentity{Agent: "claude", ID: sharedID}].Session.WorkingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command.Path != "claude" || !slicesContainPair(spec.Command.Args, "--resume", sharedID) || spec.Command.Dir != wantDir {
		t.Fatalf("unsafe or wrong command: %#v", spec.Command)
	}
	response = server.request(http.MethodPost, path, `{}`, true)
	if response.Code != http.StatusOK || len(server.terminals.specs) != 1 || !strings.Contains(response.Body.String(), `"reused":true`) {
		t.Fatalf("reuse = %d %s, creates=%d", response.Code, response.Body.String(), len(server.terminals.specs))
	}
}

func TestTerminalRejectsPromptLimitAndVanishedCWD(t *testing.T) {
	server := newTestServer(t, nil)
	path := "/api/canvas/sessions/codex/" + sharedID + "/terminal"
	response := server.request(http.MethodPost, path, `{"prompt":"`+strings.Repeat("x", 2001)+`"}`, true)
	if response.Code != http.StatusBadRequest || len(server.terminals.specs) != 0 {
		t.Fatalf("oversized prompt = %d", response.Code)
	}
	identity := contracts.SessionIdentity{Agent: "codex", ID: sharedID}
	value := server.resolver.values[identity]
	value.Session.WorkingDirectory = filepath.Join(t.TempDir(), "vanished-private-path")
	server.resolver.values[identity] = value
	response = server.request(http.MethodPost, path, `{}`, true)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "vanished-private-path") {
		t.Fatalf("vanished cwd = %d %s", response.Code, response.Body.String())
	}
}

func TestSameVendorForkUsesNativeArgv(t *testing.T) {
	server := newTestServer(t, nil)
	path := "/api/canvas/sessions/claude/" + sharedID + "/fork"
	prompt := `$(touch /tmp/coslash-should-not-exist); echo safe`
	response := server.request(http.MethodPost, path, `{"prompt":"`+prompt+`"}`, true)
	if response.Code != http.StatusOK || len(server.terminals.specs) != 1 || !strings.Contains(response.Body.String(), "22222222-2222-4222-8222-222222222222") {
		t.Fatalf("fork = %d %s", response.Code, response.Body.String())
	}
	args := server.terminals.specs[0].Command.Args
	if !slicesContainPair(args, "--resume", sharedID) || !slicesContain(args, "--fork-session") || slicesContain(args, "sh") || args[len(args)-1] != prompt {
		t.Fatalf("fork argv = %#v", args)
	}
}

func TestTurnAnalysisDisabledFailureAndCache(t *testing.T) {
	path := "/api/canvas/sessions/codex/" + sharedID + "/turns/1/analysis"
	server := newTestServer(t, nil)
	response := server.request(http.MethodPost, path, "", true)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "ANALYSIS_DISABLED") {
		t.Fatalf("disabled = %d %s", response.Code, response.Body.String())
	}
	analyzer := &fakeAnalyzer{result: TurnAnalysis{Intention: "Fix the bug", Status: "running", Findings: []string{}, Issues: []string{}}}
	server = newTestServer(t, analyzer)
	response = server.request(http.MethodPost, path, "", true)
	if response.Code != http.StatusOK || analyzer.calls != 1 || analyzer.input.Session.Agent != "codex" || analyzer.input.Turn.Prompt != "fix the bug" {
		t.Fatalf("analysis = %d %s calls=%d", response.Code, response.Body.String(), analyzer.calls)
	}
	response = server.request(http.MethodPost, path, "", true)
	if response.Code != http.StatusOK || analyzer.calls != 1 || !strings.Contains(response.Body.String(), `"cached":true`) {
		t.Fatalf("cached = %d %s calls=%d", response.Code, response.Body.String(), analyzer.calls)
	}
	analyzer = &fakeAnalyzer{err: errors.New("raw secret /Users/private stderr")}
	server = newTestServer(t, analyzer)
	response = server.request(http.MethodPost, path, "", true)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "raw secret") {
		t.Fatalf("failure = %d %s", response.Code, response.Body.String())
	}
}

func TestScopedFileReadTraversalSymlinkHTMLAndBounds(t *testing.T) {
	server := newTestServer(t, nil)
	identity := contracts.SessionIdentity{Agent: "codex", ID: sharedID}
	cwd := server.resolver.values[identity].Session.WorkingDirectory
	if err := os.WriteFile(filepath.Join(cwd, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "page.html"), []byte(`<script>top.location='https://evil.example'</script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "link.txt")); err != nil {
		t.Fatal(err)
	}
	base := "/api/canvas/sessions/codex/" + sharedID + "/files?path="
	if response := server.request(http.MethodGet, base+"note.txt", "", true); response.Code != http.StatusOK || response.Body.String() != "hello" {
		t.Fatalf("text = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"../secret.txt", "link.txt"} {
		response := server.request(http.MethodGet, base+path, "", true)
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("unsafe %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	response := server.request(http.MethodGet, base+"page.html", "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Security-Policy"), "sandbox") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("html policy = %d %#v", response.Code, response.Header())
	}
	server.handler.maxFileBytes = 3
	response = server.request(http.MethodGet, base+"note.txt", "", true)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large file = %d %s", response.Code, response.Body.String())
	}
}

func TestWorkspaceAndTerminalRoutesDelegate(t *testing.T) {
	server := newTestServer(t, nil)
	response := server.request(http.MethodGet, "/api/canvas/workspaces/codex/"+sharedID, "", true)
	if response.Code != http.StatusOK || !server.workspace.called {
		t.Fatalf("workspace = %d %s", response.Code, response.Body.String())
	}
	response = server.request(http.MethodGet, "/api/terminals/t-1", "", true)
	if response.Code != http.StatusOK || len(server.api.calls) != 1 || server.api.calls[0] != "status:t-1" {
		t.Fatalf("terminal route = %d %#v", response.Code, server.api.calls)
	}
}

func TestMalformedBodiesAndSafeManagerErrors(t *testing.T) {
	server := newTestServer(t, nil)
	path := "/api/canvas/sessions/codex/" + sharedID + "/terminal"
	response := server.request(http.MethodPost, path, `{"prompt":"ok","agent":"claude"}`, true)
	if response.Code != http.StatusBadRequest || len(server.terminals.specs) != 0 {
		t.Fatalf("duplicate identity body = %d %s", response.Code, response.Body.String())
	}
	server.terminals.statusErr = errors.New("private tmux output /Users/private")
	response = server.request(http.MethodPost, path, `{}`, true)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "/Users/private") {
		t.Fatalf("safe terminal error = %d %s", response.Code, response.Body.String())
	}
}

func TestNewRequiresCompleteDependencies(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New accepted missing dependencies")
	}
}

func TestRuntimeOwnsPersistenceAndLifecycle(t *testing.T) {
	runtime, err := Open(context.Background(), RuntimeOptions{
		CanvasHome: t.TempDir(),
		VendorHome: t.TempDir(),
		Sessions:   &fakeResolver{values: map[contracts.SessionIdentity]ResolvedSession{}},
		Settings: func() settings.SynthesisSettings {
			return settings.SynthesisSettings{Enabled: false}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	runtime.Register(mux)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/canvas/workspaces/codex/"+sharedID, nil)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revision":0`) {
		t.Fatalf("workspace = %d %s", response.Code, response.Body.String())
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func slicesContainPair(values []string, first, second string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == first && values[index+1] == second {
			return true
		}
	}
	return false
}
