package dagama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/publication"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

// The route group is exercised against the real project, board, and run stores
// and the real controller. Only the two boundaries that reach outside the
// process — git and tmux — are faked, so what these tests pin is the wire
// contract the browser client depends on, not a mock of it.

type fakeGitRunner struct {
	outputs map[string]string
	fail    map[string]bool
}

func (r fakeGitRunner) Run(_ context.Context, command revision.Command) (revision.Result, error) {
	// Every invocation is hardened with a `-c key=value` prefix before it
	// reaches a runner, so the fake keys on the command itself.
	args := command.Args
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	key := strings.Join(args, " ")
	if r.fail[key] {
		return revision.Result{ExitCode: 1, Stderr: []byte("fatal: not a git repository")}, nil
	}
	if output, ok := r.outputs[key]; ok {
		return revision.Result{ExitCode: 0, Stdout: []byte(output)}, nil
	}
	return revision.Result{ExitCode: 0}, nil
}

func newFakeGit(t *testing.T, projectPath string) *revision.Git {
	t.Helper()
	head := strings.Repeat("a", 40)
	git, err := revision.NewGit(fakeGitRunner{outputs: map[string]string{
		"rev-parse --is-bare-repository":      "false\n",
		"rev-parse --show-toplevel":           projectPath + "\n",
		"rev-parse --git-dir":                 filepath.Join(projectPath, ".git") + "\n",
		"rev-parse --git-common-dir":          filepath.Join(projectPath, ".git") + "\n",
		"symbolic-ref --short HEAD":           "feature\n",
		"symbolic-ref --short origin/HEAD":    "origin/main\n",
		"rev-parse --verify feature^{commit}": head + "\n",
		"rev-parse --verify main^{commit}":    head + "\n",
		"remote get-url origin":               "https://github.com/example/demo.git\n",
	}}, t.TempDir())
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	return git
}

type fakeTmux struct{ sessions map[string]bool }

func (f fakeTmux) Run(_ context.Context, _ io.Reader, _ string, args []string, _ string, _ []string) error {
	if len(args) > 1 && args[0] == "has-session" {
		if f.sessions[strings.TrimPrefix(args[len(args)-1], "=")] {
			return nil
		}
		return fmt.Errorf("no session")
	}
	return nil
}

func (f fakeTmux) Output(_ context.Context, _ string, args []string, _ string, _ []string, _ int64) ([]byte, error) {
	if len(args) > 0 && args[0] == "list-panes" {
		return []byte("%1"), nil
	}
	return nil, nil
}

type handlerFixture struct {
	controllerFixture
	handler  *Handler
	server   *httptest.Server
	projects *ProjectStore
	project  Project
	tmux     fakeTmux
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	base := newControllerFixture(t)

	projectRoot := t.TempDir()
	projectScope, err := runfs.OpenScope(projectRoot, runfs.ScopeOptions{})
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}
	projects, err := NewProjectStore(projectScope)
	if err != nil {
		t.Fatalf("NewProjectStore: %v", err)
	}
	project, err := projects.Open(base.ctx, base.board.ProjectPath)
	if err != nil {
		t.Fatalf("Open project: %v", err)
	}

	// The controller fixture's board is stored under its own project id, so the
	// board is re-saved under the identifier the registry derived.
	board := *base.board
	board.ProjectID = project.ID
	board.ProjectPath = project.Path
	stored, err := base.controller.boards.Save(base.ctx, &board, 0)
	if err != nil {
		t.Fatalf("Save board: %v", err)
	}
	base.board = stored

	git := newFakeGit(t, project.Path)
	publisher, err := publication.NewPublisher(git, nil, func() time.Time { return time.Unix(1, 0).UTC() })
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	tmux := fakeTmux{sessions: map[string]bool{}}
	terminals := terminal.New(terminal.Options{Capacity: 8, Runner: tmux})
	t.Cleanup(func() { _ = terminals.Close(context.Background()) })

	handler, err := NewHandler(HandlerOptions{
		Projects: projects, Boards: base.controller.boards, Controller: base.controller,
		Terminals: terminals, Git: git, Publisher: publisher,
		Background: base.ctx,
		// Detached work runs inline, so a test observes the finished pipeline
		// rather than racing it.
		Go: func(work func()) { work() },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler.Handler())
	t.Cleanup(server.Close)

	return &handlerFixture{
		controllerFixture: base, handler: handler, server: server,
		projects: projects, project: project, tmux: tmux,
	}
}

func (f *handlerFixture) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	decoded := map[string]any{}
	if len(contents) > 0 {
		if err := json.Unmarshal(contents, &decoded); err != nil {
			t.Fatalf("%s %s returned non-JSON %q", method, path, contents)
		}
	}
	return response.StatusCode, decoded
}

func (f *handlerFixture) query(suffix string) string {
	return "?projectId=" + f.project.ID + suffix
}

// startedRun drives a run to the publish gate through the API.
func (f *handlerFixture) startedRun(t *testing.T) string {
	t.Helper()
	status, body := f.do(t, http.MethodPost, "/api/dagama/runs"+f.query(""), map[string]any{
		"boardId": f.board.ID,
		"source":  map[string]any{"kind": "text", "title": "Task", "text": "Change README"},
	})
	if status != http.StatusCreated {
		t.Fatalf("start run = %d, %v", status, body)
	}
	run, _ := body["run"].(map[string]any)
	runID, _ := run["runId"].(string)
	if runID == "" {
		t.Fatalf("start run returned no run id: %v", body)
	}
	return runID
}

// ---------------------------------------------------------------------------

func TestHandlerOpensProjectWithAStableIdentity(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodPost, "/api/dagama/projects/open",
		map[string]any{"path": fixture.project.Path})
	if status != http.StatusOK {
		t.Fatalf("open = %d, %v", status, body)
	}
	project, _ := body["project"].(map[string]any)
	if project["id"] != fixture.project.ID || project["path"] != fixture.project.Path {
		t.Fatalf("identity drifted: %v", project)
	}
	// Reopening must not allocate a second identifier for the same folder.
	_, again := fixture.do(t, http.MethodPost, "/api/dagama/projects/open",
		map[string]any{"path": fixture.project.Path})
	if again["project"].(map[string]any)["id"] != fixture.project.ID {
		t.Fatalf("a reopen produced a different identity: %v", again)
	}
}

func TestHandlerRefusesAProjectThatIsNotADirectory(t *testing.T) {
	fixture := newHandlerFixture(t)
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body := fixture.do(t, http.MethodPost, "/api/dagama/projects/open", map[string]any{"path": file})
	if status != http.StatusBadRequest || body["code"] != CodePolicyViolation {
		t.Fatalf("expected a policy refusal, got %d %v", status, body)
	}
}

func TestHandlerReportsAnUnopenedProjectDistinctly(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodGet, "/api/dagama/boards?projectId=never-opened-000000000000", nil)
	if status != http.StatusConflict || body["code"] != CodeProjectNotOpen {
		t.Fatalf("expected PROJECT_NOT_OPEN, got %d %v", status, body)
	}
}

func TestHandlerRefusesAnInvalidProjectIdentifier(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodGet, "/api/dagama/boards?projectId=../../etc", nil)
	if status != http.StatusBadRequest || body["code"] != CodeInvalidProjectID {
		t.Fatalf("expected INVALID_PROJECT_ID, got %d %v", status, body)
	}
}

func TestHandlerBoardDocumentCarriesIdentityBesideTheBoard(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodGet, "/api/dagama/boards/"+fixture.board.ID+fixture.query(""), nil)
	if status != http.StatusOK {
		t.Fatalf("read = %d, %v", status, body)
	}
	document, _ := body["board"].(map[string]any)
	for _, key := range []string{"schemaVersion", "id", "name", "revision", "createdAt", "updatedAt", "board"} {
		if _, ok := document[key]; !ok {
			t.Fatalf("board document is missing %q: %v", key, document)
		}
	}
	stored, _ := document["board"].(map[string]any)
	if stored["projectId"] != fixture.project.ID {
		t.Fatalf("the stored board lost its project identity: %v", stored)
	}
	if _, ok := stored["components"].(map[string]any)["plan"]; !ok {
		t.Fatalf("the stored board lost its pipeline: %v", stored)
	}
}

func TestHandlerTakesBoardIdentityFromTheRouteNotTheBody(t *testing.T) {
	fixture := newHandlerFixture(t)
	_, read := fixture.do(t, http.MethodGet, "/api/dagama/boards/"+fixture.board.ID+fixture.query(""), nil)
	stored := read["board"].(map[string]any)["board"].(map[string]any)

	// A document claiming another project and another board must be stored
	// under the identity the route names, never the one it asserts.
	stored["projectId"] = "someone-elses-project"
	stored["id"] = "someone-elses-board"
	stored["projectPath"] = "/etc"

	status, body := fixture.do(t, http.MethodPut, "/api/dagama/boards/"+fixture.board.ID+fixture.query(""),
		map[string]any{"name": "Renamed", "board": stored, "expectedRevision": fixture.board.Revision})
	if status != http.StatusOK {
		t.Fatalf("write = %d, %v", status, body)
	}
	saved := body["board"].(map[string]any)["board"].(map[string]any)
	if saved["projectId"] != fixture.project.ID || saved["id"] != fixture.board.ID {
		t.Fatalf("body identity was trusted: %v", saved)
	}
	if saved["projectPath"] != fixture.project.Path {
		t.Fatalf("body project path was trusted: %v", saved)
	}
}

func TestHandlerReportsARevisionConflictWithTheActualRevision(t *testing.T) {
	fixture := newHandlerFixture(t)
	_, read := fixture.do(t, http.MethodGet, "/api/dagama/boards/"+fixture.board.ID+fixture.query(""), nil)
	stored := read["board"].(map[string]any)["board"]

	status, body := fixture.do(t, http.MethodPut, "/api/dagama/boards/"+fixture.board.ID+fixture.query(""),
		map[string]any{"name": "Stale", "board": stored, "expectedRevision": fixture.board.Revision + 99})
	if status != http.StatusConflict || body["code"] != CodeRevisionConflict {
		t.Fatalf("expected a revision conflict, got %d %v", status, body)
	}
	if body["actualRevision"] != float64(fixture.board.Revision) {
		t.Fatalf("conflict did not carry the actual revision: %v", body)
	}
}

func TestHandlerPersistsBoardSteering(t *testing.T) {
	fixture := newHandlerFixture(t)
	_, read := fixture.do(t, http.MethodGet, "/api/dagama/boards/"+fixture.board.ID+fixture.query(""), nil)
	stored := read["board"].(map[string]any)["board"].(map[string]any)
	stored["instructions"] = "Never edit generated files."
	stored["components"].(map[string]any)["build"].(map[string]any)["prompt"] = "Prefer the smallest diff."

	status, body := fixture.do(t, http.MethodPut, "/api/dagama/boards/"+fixture.board.ID+fixture.query(""),
		map[string]any{"name": fixture.board.Name, "board": stored, "expectedRevision": fixture.board.Revision})
	if status != http.StatusOK {
		t.Fatalf("write = %d, %v", status, body)
	}
	saved := body["board"].(map[string]any)["board"].(map[string]any)
	if saved["instructions"] != "Never edit generated files." {
		t.Fatalf("instructions did not persist: %v", saved)
	}
	build := saved["components"].(map[string]any)["build"].(map[string]any)
	if build["prompt"] != "Prefer the smallest diff." {
		t.Fatalf("prompt card did not persist: %v", build)
	}
}

func TestHandlerRequiresARevisionToDeleteABoard(t *testing.T) {
	fixture := newHandlerFixture(t)
	path := "/api/dagama/boards/" + fixture.board.ID + fixture.query("")
	status, body := fixture.do(t, http.MethodDelete, path, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("a delete without a revision must be refused, got %d %v", status, body)
	}
	status, body = fixture.do(t, http.MethodDelete, path+"&expectedRevision=999", nil)
	if status != http.StatusConflict || body["code"] != CodeRevisionConflict {
		t.Fatalf("a stale delete must conflict, got %d %v", status, body)
	}
	status, _ = fixture.do(t, http.MethodDelete,
		path+"&expectedRevision="+fmt.Sprint(fixture.board.Revision), nil)
	if status != http.StatusOK {
		t.Fatalf("delete = %d", status)
	}
	status, listed := fixture.do(t, http.MethodGet, "/api/dagama/boards"+fixture.query(""), nil)
	if status != http.StatusOK || len(listed["boards"].([]any)) != 0 {
		t.Fatalf("the board survived deletion: %v", listed)
	}
}

func TestHandlerPreviewsARunFromASavedBoardOrADraft(t *testing.T) {
	fixture := newHandlerFixture(t)
	for _, body := range []map[string]any{
		{"boardId": fixture.board.ID},
		{"board": map[string]any{"schemaVersion": 1, "components": map[string]any{}}},
	} {
		status, decoded := fixture.do(t, http.MethodPost, "/api/dagama/runs/preview"+fixture.query(""), body)
		if status != http.StatusOK {
			t.Fatalf("preview = %d, %v", status, decoded)
		}
		preview, _ := decoded["preview"].(map[string]any)
		for _, key := range []string{
			"projectPath", "baseBranch", "baseSha", "defaultBranch",
			"checkoutBranch", "isLinkedWorktree", "remoteUrl", "runRootParent",
		} {
			if _, ok := preview[key]; !ok {
				t.Fatalf("preview is missing %q: %v", key, preview)
			}
		}
		if preview["runRootParent"] != fixture.controller.RunRootsDirectory() {
			t.Fatalf("preview promised a run root the controller would not use: %v", preview)
		}
	}
}

func TestHandlerRefusesAPreviewWithNoWorkflow(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs/preview"+fixture.query(""), map[string]any{})
	if status != http.StatusBadRequest || body["code"] != CodePolicyViolation {
		t.Fatalf("expected a policy refusal, got %d %v", status, body)
	}
}

func TestHandlerStartsARunAndAnswersBeforeThePipelineFinishes(t *testing.T) {
	fixture := newHandlerFixture(t)
	// Detached work is deferred so the response can be inspected before the
	// pipeline has advanced at all.
	var deferred []func()
	fixture.handler.launch = func(work func()) { deferred = append(deferred, work) }

	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs"+fixture.query(""), map[string]any{
		"boardId": fixture.board.ID,
		"source":  map[string]any{"kind": "text", "title": "Task", "text": "Change README"},
	})
	if status != http.StatusCreated {
		t.Fatalf("start = %d, %v", status, body)
	}
	run, _ := body["run"].(map[string]any)
	if run["status"] != string(RunPreparing) {
		t.Fatalf("a created run must be reported as preparing: %v", run)
	}
	if len(deferred) != 1 {
		t.Fatalf("the pipeline was not detached: %d", len(deferred))
	}
	deferred[0]()

	status, body = fixture.do(t, http.MethodGet, "/api/dagama/runs/"+run["runId"].(string)+fixture.query(""), nil)
	if status != http.StatusOK || body["run"].(map[string]any)["status"] != string(RunAwaitingApproval) {
		t.Fatalf("the detached pipeline did not reach the gate: %v", body)
	}
}

func TestHandlerRefusesAStartItCannotAccept(t *testing.T) {
	fixture := newHandlerFixture(t)
	var deferred int
	fixture.handler.launch = func(func()) { deferred++ }

	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs"+fixture.query(""), map[string]any{
		"boardId": "missing-board",
		"source":  map[string]any{"kind": "text", "title": "Task", "text": "x"},
	})
	if status < 400 {
		t.Fatalf("a start against a missing board must be refused, got %d %v", status, body)
	}
	if deferred != 0 {
		t.Fatal("a refused start must not detach any work")
	}
}

func TestHandlerRunProjectionCarriesTheFieldsTheClientReads(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	_, body := fixture.do(t, http.MethodGet, "/api/dagama/runs/"+runID+fixture.query(""), nil)
	run, _ := body["run"].(map[string]any)
	for _, key := range []string{
		"schemaVersion", "runId", "projectId", "boardId", "boardRevision", "title", "status",
		"createdAt", "updatedAt", "finishedAt", "source", "runRoot", "branch", "baseBranch",
		"baseSha", "remoteUrl", "components", "artifacts", "change", "gate", "publication",
		"failure", "lastSeq",
	} {
		if _, ok := run[key]; !ok {
			t.Fatalf("the run projection is missing %q", key)
		}
	}
	components, _ := run["components"].(map[string]any)
	for _, id := range ComponentIDs {
		component, ok := components[string(id)].(map[string]any)
		if !ok {
			t.Fatalf("the run projection is missing component %q", id)
		}
		for _, key := range []string{"id", "status", "instance", "reason", "message", "outputs", "attempt"} {
			if _, ok := component[key]; !ok {
				t.Fatalf("component %q is missing %q", id, key)
			}
		}
	}
}

func TestHandlerListsRunsWithTheSummaryShape(t *testing.T) {
	fixture := newHandlerFixture(t)
	fixture.startedRun(t)
	status, body := fixture.do(t, http.MethodGet, "/api/dagama/runs"+fixture.query(""), nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d, %v", status, body)
	}
	runs, _ := body["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %v", runs)
	}
	summary, _ := runs[0].(map[string]any)
	for _, key := range []string{"runId", "projectId", "boardId", "title", "status", "createdAt", "updatedAt", "finishedAt"} {
		if _, ok := summary[key]; !ok {
			t.Fatalf("run summary is missing %q: %v", key, summary)
		}
	}
	if _, ok := body["errors"].([]any); !ok {
		t.Fatalf("the listing must always carry an errors array: %v", body)
	}
}

func TestHandlerReadsOnlyPromotedArtifacts(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)

	status, body := fixture.do(t, http.MethodGet,
		"/api/dagama/runs/"+runID+"/artifacts/PLAN.md"+fixture.query(""), nil)
	if status != http.StatusOK {
		t.Fatalf("artifact = %d, %v", status, body)
	}
	if _, ok := body["contents"].(string); !ok {
		t.Fatalf("artifact contents must be a string: %v", body)
	}

	// A name the run never promoted is not a path to try; it is a 404.
	for _, name := range []string{"..%2F..%2Fetc%2Fpasswd", "secrets.env"} {
		status, body := fixture.do(t, http.MethodGet,
			"/api/dagama/runs/"+runID+"/artifacts/"+name+fixture.query(""), nil)
		if status != http.StatusNotFound {
			t.Fatalf("reading %q returned %d %v", name, status, body)
		}
	}
}

func TestHandlerRecomposesTheAssembledPromptWithSteering(t *testing.T) {
	fixture := newHandlerFixture(t)
	board := *fixture.board
	board.Instructions = "Never edit generated files."
	board.Components.Build.Prompt = "Prefer the smallest diff."
	if _, err := fixture.controller.boards.Save(fixture.ctx, &board, board.Revision); err != nil {
		t.Fatalf("Save: %v", err)
	}
	runID := fixture.startedRun(t)

	status, body := fixture.do(t, http.MethodGet,
		"/api/dagama/runs/"+runID+"/prompt"+fixture.query("&componentId=build"), nil)
	if status != http.StatusOK {
		t.Fatalf("prompt = %d, %v", status, body)
	}
	contents, _ := body["contents"].(string)
	for _, want := range []string{"Never edit generated files.", "Prefer the smallest diff.", "Required output:"} {
		if !strings.Contains(contents, want) {
			t.Fatalf("the assembled prompt is missing %q:\n%s", want, contents)
		}
	}
}

func TestHandlerRefusesAPromptForAStageThatRunsNoModel(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	status, body := fixture.do(t, http.MethodGet,
		"/api/dagama/runs/"+runID+"/prompt"+fixture.query("&componentId=verify"), nil)
	if status != http.StatusBadRequest || body["code"] != CodePolicyViolation {
		t.Fatalf("expected a policy refusal, got %d %v", status, body)
	}
}

func TestHandlerRefusesAControlTheControllerWouldReject(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	var deferred int
	fixture.handler.launch = func(func()) { deferred++ }

	// The run is at the publish gate: no seat is failed, running, or human
	// controlled, so every seat control must be refused before anything runs.
	for _, control := range []string{"retry", "takeover", "handback"} {
		status, body := fixture.do(t, http.MethodPost,
			"/api/dagama/runs/"+runID+"/"+control+fixture.query(""),
			map[string]any{"componentId": "build"})
		if status != http.StatusConflict || body["code"] != CodeInvalidState {
			t.Fatalf("%s returned %d %v, want a conflict", control, status, body)
		}
	}
	if deferred != 0 {
		t.Fatalf("a refused control detached %d operations", deferred)
	}
}

func TestHandlerRefusesASeatControlThatNamesADeterministicStage(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs/"+runID+"/retry"+fixture.query(""),
		map[string]any{"componentId": "publish"})
	if status != http.StatusBadRequest || body["code"] != CodePolicyViolation {
		t.Fatalf("expected a policy refusal, got %d %v", status, body)
	}
}

func TestHandlerRunsPublishPreflightAgainstTheRunsOwnFacts(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	status, body := fixture.do(t, http.MethodGet,
		"/api/dagama/runs/"+runID+"/publish-preflight"+fixture.query(""), nil)
	if status != http.StatusOK {
		t.Fatalf("preflight = %d, %v", status, body)
	}
	preflight, _ := body["preflight"].(map[string]any)
	for _, key := range []string{
		"ok", "changeRevision", "treeOid", "patchSha256", "branch",
		"baseBranch", "baseSha", "remoteUrl", "draft", "checklist",
	} {
		if _, ok := preflight[key]; !ok {
			t.Fatalf("preflight is missing %q: %v", key, preflight)
		}
	}
	if _, ok := preflight["checklist"].([]any); !ok {
		t.Fatalf("the checklist must always be an array: %v", preflight)
	}
}

func TestHandlerApprovesAGateAndPublishes(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs/"+runID+"/gate"+fixture.query(""),
		map[string]any{"decision": "approved"})
	if status != http.StatusOK {
		t.Fatalf("gate = %d, %v", status, body)
	}
	state, err := fixture.controller.ReadRun(fixture.ctx, fixture.project.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunSucceeded || state.Publication == nil {
		t.Fatalf("approval did not publish: %+v", state)
	}
}

func TestHandlerApprovesWithoutPublishing(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	before := fixture.runtime.publishes

	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs/"+runID+"/gate"+fixture.query(""),
		map[string]any{"decision": "approved", "publish": false})
	if status != http.StatusOK {
		t.Fatalf("gate = %d, %v", status, body)
	}
	state, err := fixture.controller.ReadRun(fixture.ctx, fixture.project.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	// The operator accepted the change and declined the outward-facing action.
	// Publishing anyway would be the worst possible reading of that.
	if fixture.runtime.publishes != before {
		t.Fatalf("approve-without-publish still published: %d -> %d", before, fixture.runtime.publishes)
	}
	if state.Status != RunSucceeded {
		t.Fatalf("the run did not complete: %+v", state)
	}
	if state.Publication != nil {
		t.Fatalf("a skipped publication recorded a publication: %+v", state.Publication)
	}
	if state.Components[ComponentPublish].Status != ComponentSucceededStatus {
		t.Fatalf("publish stayed unresolved: %+v", state.Components[ComponentPublish])
	}
}

func TestHandlerRejectsAGate(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	status, _ := fixture.do(t, http.MethodPost, "/api/dagama/runs/"+runID+"/gate"+fixture.query(""),
		map[string]any{"decision": "rejected"})
	if status != http.StatusOK {
		t.Fatalf("gate = %d", status)
	}
	state, err := fixture.controller.ReadRun(fixture.ctx, fixture.project.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != RunFailed || state.Failure == nil || state.Failure.Reason != "gate_rejected" {
		t.Fatalf("rejection did not end the run: %+v", state)
	}
}

func TestHandlerRefusesAnUnknownGateDecision(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs/"+runID+"/gate"+fixture.query(""),
		map[string]any{"decision": "maybe"})
	if status != http.StatusBadRequest || body["code"] != CodePolicyViolation {
		t.Fatalf("expected a policy refusal, got %d %v", status, body)
	}
}

func TestHandlerAttachesASeatTerminalReadOnlyForAnAutomatedTurn(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	state, err := fixture.controller.ReadRun(fixture.ctx, fixture.project.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := state.Components[ComponentBuild].Attempt
	if attempt == nil {
		t.Fatal("the fixture produced no build attempt")
	}
	fixture.tmux.sessions[attempt.TmuxName] = true

	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs/"+runID+"/terminal"+fixture.query(""),
		map[string]any{"componentId": "build"})
	if status != http.StatusOK {
		t.Fatalf("attach = %d, %v", status, body)
	}
	if body["terminalId"] != attempt.AttemptID || body["attemptId"] != attempt.AttemptID {
		t.Fatalf("attach returned the wrong identity: %v", body)
	}
	// The controller owns this turn, so watching it must not be able to type
	// into it. Writability is a server decision, not a client request.
	if body["writable"] != false {
		t.Fatalf("an automated turn was attached writable: %v", body)
	}
	if _, present := body["url"]; present {
		t.Fatalf("a terminal URL leaked into the response: %v", body)
	}
}

func TestHandlerReportsAMissingSeatTerminal(t *testing.T) {
	fixture := newHandlerFixture(t)
	runID := fixture.startedRun(t)
	status, body := fixture.do(t, http.MethodPost, "/api/dagama/runs/"+runID+"/terminal"+fixture.query(""),
		map[string]any{"componentId": "review"})
	if status != http.StatusNotFound && status != http.StatusConflict {
		t.Fatalf("expected a refusal for a seat with no live pane, got %d %v", status, body)
	}
	if body["ok"] != false {
		t.Fatalf("a refusal must use the error envelope: %v", body)
	}
}

func TestHandlerRefusesAnInvalidRunIdentifier(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodGet, "/api/dagama/runs/not-a-run-id"+fixture.query(""), nil)
	if status != http.StatusBadRequest || body["code"] != CodeInvalidRunID {
		t.Fatalf("expected INVALID_RUN_ID, got %d %v", status, body)
	}
}

func TestHandlerRefusesAnUnknownRouteAndAnUnknownField(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodGet, "/api/dagama/nope"+fixture.query(""), nil)
	if status != http.StatusNotFound || body["code"] != CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %d %v", status, body)
	}
	status, body = fixture.do(t, http.MethodPost, "/api/dagama/projects/open",
		map[string]any{"path": fixture.project.Path, "sudo": true})
	if status != http.StatusBadRequest {
		t.Fatalf("an unknown field must be refused, got %d %v", status, body)
	}
}

func TestHandlerErrorEnvelopeNeverLeaksDetail(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, body := fixture.do(t, http.MethodGet,
		"/api/dagama/boards/missing-board"+fixture.query(""), nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %v", status, body)
	}
	allowed := map[string]bool{"ok": true, "code": true, "error": true, "field": true, "actualRevision": true}
	for key := range body {
		if !allowed[key] {
			t.Fatalf("the error envelope carried an unexpected field %q: %v", key, body)
		}
	}
	message, _ := body["error"].(string)
	if strings.Contains(message, fixture.project.Path) || strings.Contains(message, "/private") {
		t.Fatalf("a private path reached the client: %q", message)
	}
}
