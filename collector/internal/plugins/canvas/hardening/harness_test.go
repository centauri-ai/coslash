package hardening

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessioncanvas"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessiondetail"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

// The token every test authenticates with. A second, wrong token is used to
// prove the comparison is against the real value rather than "non-empty".
const (
	validToken = "test-token-0123456789"
	wrongToken = "test-token-9876543210"
)

// fakeSessions resolves one known session pointing at a temporary working
// directory. The directory is real, because the file-preview scoping tests are
// only meaningful against a real filesystem.
type fakeSessions struct {
	workingDirectory string
	transcript       string
}

func (f fakeSessions) Resolve(_ context.Context, identity contracts.SessionIdentity) (sessioncanvas.ResolvedSession, error) {
	if identity.Agent != "claude" || identity.ID != "session-1" {
		return sessioncanvas.ResolvedSession{}, os.ErrNotExist
	}
	return sessioncanvas.ResolvedSession{
		Session: session.Session{
			ID:               identity.ID,
			Agent:            identity.Agent,
			WorkingDirectory: f.workingDirectory,
		},
		TranscriptPath: f.transcript,
	}, nil
}

type fakeProjector struct{}

func (fakeProjector) ProjectKnown(context.Context, session.Session, string) (*sessiondetail.Detail, error) {
	return &sessiondetail.Detail{}, nil
}

type fakeRenamer struct{ calls atomic.Int64 }

func (r *fakeRenamer) Rename(context.Context, contracts.SessionIdentity, string) error {
	r.calls.Add(1)
	return nil
}

// fakeTmux stands in for the one boundary that leaves the process. It reports
// no live sessions unless a test declares one, so an adopt of an unknown pane
// fails exactly as it would in production.
type fakeTmux struct{ live map[string]bool }

func (f fakeTmux) Run(_ context.Context, _ io.Reader, _ string, args []string, _ string, _ []string) error {
	if len(args) > 1 && args[0] == "has-session" {
		if f.live[strings.TrimPrefix(args[len(args)-1], "=")] {
			return nil
		}
		return fmt.Errorf("no such session")
	}
	return nil
}

func (f fakeTmux) Output(_ context.Context, _ string, args []string, _ string, _ []string, _ int64) ([]byte, error) {
	if len(args) > 0 && args[0] == "list-panes" {
		return []byte("%1"), nil
	}
	return nil, nil
}

// suite is the assembled plugin behind the real guard.
type suite struct {
	server *httptest.Server
	// address is the Host the guard was configured to accept.
	address          string
	workingDirectory string
	canvasHome       string
	renamer          *fakeRenamer
	// dagamaReached records whether a request survived the guard and arrived at
	// the DaGama prefix. The route group's own semantics are covered by its
	// package; what this suite proves is that the guard stands in front of it.
	dagamaReached atomic.Bool
	runtime       *sessioncanvas.Runtime
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	ctx := context.Background()

	workingDirectory := t.TempDir()
	transcript := filepath.Join(workingDirectory, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	renamer := &fakeRenamer{}
	canvasHome := t.TempDir()
	runtime, err := sessioncanvas.Open(ctx, sessioncanvas.RuntimeOptions{
		CanvasHome: canvasHome,
		VendorHome: t.TempDir(),
		Sessions:   fakeSessions{workingDirectory: workingDirectory, transcript: transcript},
		Projector:  fakeProjector{},
		Renamer:    renamer,
		TerminalOptions: terminal.Options{
			Capacity: 8,
			Runner:   fakeTmux{live: map[string]bool{}},
		},
	})
	if err != nil {
		t.Fatalf("open session canvas runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(ctx) })

	assembled := serve(t, runtime, workingDirectory, canvasHome, renamer)
	t.Cleanup(assembled.server.Close)
	return assembled
}

// serve mounts a runtime behind the real guard on a test listener.
func serve(t *testing.T, runtime *sessioncanvas.Runtime, details ...any) *suite {
	t.Helper()
	assembled := &suite{runtime: runtime}
	if len(details) == 3 {
		assembled.workingDirectory, _ = details[0].(string)
		assembled.canvasHome, _ = details[1].(string)
		assembled.renamer, _ = details[2].(*fakeRenamer)
	}

	mux := http.NewServeMux()
	runtime.Register(mux)
	mux.HandleFunc("/api/dagama/", func(w http.ResponseWriter, _ *http.Request) {
		assembled.dagamaReached.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	// The guard's address is only known once the listener exists, so the
	// handler reads a value the server assignment below fills in.
	var guard httpsec.Guard
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guard.Wrap(mux).ServeHTTP(w, r)
	}))
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	guard = httpsec.Guard{Addr: parsed.Host, Token: validToken}
	assembled.server = server
	assembled.address = parsed.Host
	return assembled
}

type call struct {
	method  string
	path    string
	body    string
	token   string
	host    string
	origin  string
	site    string
	mode    string
	dest    string
	headers map[string]string
	// noToken sends no credential at all.
	noToken bool
}

func (s *suite) do(t *testing.T, request call) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if request.body != "" {
		body = strings.NewReader(request.body)
	}
	method := request.method
	if method == "" {
		method = http.MethodGet
	}
	built, err := http.NewRequest(method, s.server.URL+request.path, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if !request.noToken {
		token := request.token
		if token == "" {
			token = validToken
		}
		built.Header.Set("X-Coslash-Token", token)
	}
	if request.body != "" {
		built.Header.Set("Content-Type", "application/json")
	}
	if request.host != "" {
		built.Host = request.host
	}
	for name, value := range map[string]string{
		"Origin":         request.origin,
		"Sec-Fetch-Site": request.site,
		"Sec-Fetch-Mode": request.mode,
		"Sec-Fetch-Dest": request.dest,
	} {
		if value != "" {
			built.Header.Set(name, value)
		}
	}
	for name, value := range request.headers {
		built.Header.Set(name, value)
	}

	response, err := http.DefaultClient.Do(built)
	if err != nil {
		t.Fatalf("%s %s: %v", method, request.path, err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response, contents
}

// apiRoutes are the authenticated surfaces the plugin registers. The guard must
// treat every one of them identically; a route group that is only guarded
// because nobody thought to call it is not guarded.
func (s *suite) apiRoutes() []call {
	return []call{
		{path: "/api/canvas/sessions/claude/session-1"},
		{path: "/api/canvas/sessions/claude/session-1/files?path=notes.md"},
		{path: "/api/canvas/workspaces/claude/session-1"},
		{path: "/api/terminals/terminal-1"},
		{path: "/api/dagama/boards?projectId=demo"},
		{method: http.MethodPut, path: "/api/canvas/sessions/claude/session-1/name", body: `{"name":"x"}`},
		{method: http.MethodPost, path: "/api/canvas/sessions/claude/session-1/terminal", body: `{}`},
	}
}

// writeWorkspace stores one workspace so revision and traversal tests have
// something real to act against.
func (s *suite) writeWorkspace(t *testing.T, agent, id string, revision int) *http.Response {
	t.Helper()
	payload := fmt.Sprintf(`{"schemaVersion":1,"expectedRevision":%d,"state":{"note":"x"}}`, revision)
	response, _ := s.do(t, call{
		method: http.MethodPut,
		path:   fmt.Sprintf("/api/canvas/workspaces/%s/%s", agent, id),
		body:   payload,
	})
	return response
}
