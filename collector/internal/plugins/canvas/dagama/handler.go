package dagama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/publication"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

// routePrefix is the frozen route group this package owns.
const routePrefix = "/api/dagama/"

// maxRequestBytes bounds a decoded request body. A board is the largest thing a
// client sends; the headroom covers the envelope around it.
const maxRequestBytes = MaxBoardBytes + 64<<10

// HandlerOptions are the collaborators the route group needs. Every one is
// required except Background and Go, which have safe defaults.
type HandlerOptions struct {
	Projects   *ProjectStore
	Boards     *BoardStore
	Controller *Controller
	Terminals  *terminal.Manager
	Git        *revision.Git
	Publisher  *publication.Publisher

	// Background is the context detached pipeline work runs under. It must
	// outlive any single request and is cancelled when the plugin closes.
	Background context.Context
	// Go starts detached work. Tests substitute a synchronous implementation so
	// a pipeline runs to completion before the assertions.
	Go func(func())
}

// Handler serves the frozen DaGama route group.
//
// Two rules shape it. First, identity never comes from a request body: the
// project, board, and run a call operates on are read from the route and the
// query, so a crafted document cannot address another project's storage.
// Second, a control is checked synchronously and executed in the background:
// starting a run, retrying a seat, or approving a publication can each take
// minutes, and blocking the request would trade a refusal the operator can act
// on for a timeout they cannot. The check is what makes that safe — a control
// the controller would reject is rejected here, before anything is detached.
//
// Authentication, origin, host, and method guards remain the caller's
// responsibility; this handler is mounted behind the existing coSlash guards.
type Handler struct {
	projects   *ProjectStore
	boards     *BoardStore
	controller *Controller
	terminals  *terminal.Manager
	git        *revision.Git
	publisher  *publication.Publisher
	background context.Context
	launch     func(func())
}

// NewHandler builds the route group.
func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.Projects == nil || options.Boards == nil || options.Controller == nil ||
		options.Terminals == nil || options.Git == nil || options.Publisher == nil {
		return nil, newError(CodeInvalidState, "the DaGama handler dependencies are incomplete")
	}
	background := options.Background
	if background == nil {
		background = context.Background()
	}
	launch := options.Go
	if launch == nil {
		launch = func(work func()) { go work() }
	}
	return &Handler{
		projects: options.Projects, boards: options.Boards, controller: options.Controller,
		terminals: options.Terminals, git: options.Git, publisher: options.Publisher,
		background: background, launch: launch,
	}, nil
}

// Register mounts the route group on a shared mux. It registers only the prefix
// frozen for this package in CONTRACTS.md.
func (h *Handler) Register(mux *http.ServeMux) { mux.Handle(routePrefix, h.Handler()) }

// Handler returns the route group as a standalone handler.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+routePrefix+"projects/open", h.openProject)

	mux.HandleFunc("GET "+routePrefix+"boards", h.listBoards)
	mux.HandleFunc("GET "+routePrefix+"boards/{boardId}", h.readBoard)
	mux.HandleFunc("PUT "+routePrefix+"boards/{boardId}", h.writeBoard)
	mux.HandleFunc("DELETE "+routePrefix+"boards/{boardId}", h.deleteBoard)

	mux.HandleFunc("POST "+routePrefix+"runs/preview", h.previewRun)
	mux.HandleFunc("POST "+routePrefix+"runs", h.startRun)
	mux.HandleFunc("GET "+routePrefix+"runs", h.listRuns)
	mux.HandleFunc("GET "+routePrefix+"runs/{runId}", h.readRun)
	mux.HandleFunc("GET "+routePrefix+"runs/{runId}/artifacts/{name}", h.readArtifact)
	mux.HandleFunc("GET "+routePrefix+"runs/{runId}/prompt", h.readPrompt)
	mux.HandleFunc("GET "+routePrefix+"runs/{runId}/publish-preflight", h.publishPreflight)
	mux.HandleFunc("POST "+routePrefix+"runs/{runId}/retry", h.retrySeat)
	mux.HandleFunc("POST "+routePrefix+"runs/{runId}/terminal", h.attachTerminal)
	mux.HandleFunc("POST "+routePrefix+"runs/{runId}/cancel", h.cancelRun)
	mux.HandleFunc("POST "+routePrefix+"runs/{runId}/takeover", h.takeoverSeat)
	mux.HandleFunc("POST "+routePrefix+"runs/{runId}/handback", h.handbackSeat)
	mux.HandleFunc("POST "+routePrefix+"runs/{runId}/gate", h.decideGate)

	mux.HandleFunc(routePrefix, func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, http.StatusNotFound, newError(CodeNotFound, "the DaGama route does not exist"))
	})
	return mux
}

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, newError(CodeStorageFailed, "the response could not be encoded"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

// statusFor maps a stable code to its HTTP status. The mapping is exhaustive on
// purpose: an unmapped code becoming a 500 would turn a client-fixable refusal
// into an apparent server fault.
func statusFor(code string) int {
	switch code {
	case CodeNotFound:
		return http.StatusNotFound
	case CodeRevisionConflict, CodeInvalidState, CodeProjectNotOpen:
		return http.StatusConflict
	case CodePolicyViolation, CodeInvalidRunID, CodeInvalidBoardID, CodeInvalidProjectID,
		CodeSchemaVersion, CodeCorruptDocument, CodeUnsafePath:
		return http.StatusBadRequest
	case CodeLogFull:
		return http.StatusInsufficientStorage
	default:
		return http.StatusInternalServerError
	}
}

// writeError translates any failure into the frozen error envelope. Only the
// stable code, the safe message, and the field reach the client — a detail, a
// wrapped cause, or a private path never does.
func writeError(w http.ResponseWriter, err error) {
	var failure *Error
	if errors.As(err, &failure) {
		writeFailure(w, statusFor(failure.Code), failure)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	writeFailure(w, http.StatusInternalServerError, newError(CodeStorageFailed, "the request could not be completed"))
}

func writeFailure(w http.ResponseWriter, status int, failure *Error) {
	writeJSON(w, status, contracts.ErrorResponse{
		OK: false, Code: failure.Code, Error: failure.Message,
		Field: failure.Field, ActualRevision: failure.ActualRevision,
	})
}

// decodeBody reads a bounded JSON body. An unknown field is refused rather than
// ignored, so a client that believes it is setting something always finds out.
func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			writeFailure(w, http.StatusUnsupportedMediaType,
				newError(CodePolicyViolation, "the request body must be application/json"))
			return false
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		writeFailure(w, http.StatusBadRequest, newError(CodePolicyViolation, "the request body could not be read"))
		return false
	}
	if int64(len(body)) > maxRequestBytes {
		writeFailure(w, http.StatusRequestEntityTooLarge, newError(CodePolicyViolation, "the request body is too large"))
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil || decoder.More() {
		writeFailure(w, http.StatusBadRequest, newError(CodePolicyViolation, "the request body is not valid JSON"))
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Route scoping
// ---------------------------------------------------------------------------

// project resolves the project every scoped route operates in. The identifier
// comes from the query and is validated before it can reach storage.
func (h *Handler) project(w http.ResponseWriter, r *http.Request) (Project, bool) {
	projectID := r.URL.Query().Get("projectId")
	if !ValidProjectID(projectID) {
		writeFailure(w, http.StatusBadRequest, &Error{
			Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId",
		})
		return Project{}, false
	}
	project, err := h.projects.Get(r.Context(), projectID)
	if err != nil {
		writeError(w, err)
		return Project{}, false
	}
	return project, true
}

func (h *Handler) runScope(w http.ResponseWriter, r *http.Request) (Project, string, bool) {
	project, ok := h.project(w, r)
	if !ok {
		return Project{}, "", false
	}
	runID := r.PathValue("runId")
	if !ValidRunID(runID) {
		writeFailure(w, http.StatusBadRequest, &Error{
			Code: CodeInvalidRunID, Message: "the run identifier is not valid", Field: "runId",
		})
		return Project{}, "", false
	}
	return project, runID, true
}

// seatComponent reads the component a seat control names. Only a component that
// runs a model can be named: the deterministic stages have nothing to retry,
// take over, or hand back.
func seatComponent(value string) (ComponentID, error) {
	component := ComponentID(strings.TrimSpace(value))
	if !HasSeat(component) {
		return "", policyError("componentId", "the component is not an agent seat")
	}
	return component, nil
}

// ---------------------------------------------------------------------------
// Projects and boards
// ---------------------------------------------------------------------------

func (h *Handler) openProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	project, err := h.projects.Open(r.Context(), body.Path)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project": project})
}

// boardDocument is the frozen envelope: identity beside the stored board.
func boardDocument(board *Board) (contracts.BoardDocument, error) {
	encoded, err := json.Marshal(board)
	if err != nil {
		return contracts.BoardDocument{}, newError(CodeStorageFailed, "the board could not be encoded").withCause(err)
	}
	return contracts.BoardDocument{
		SchemaVersion: board.SchemaVersion,
		ID:            board.ID,
		Name:          board.Name,
		Revision:      board.Revision,
		CreatedAt:     board.CreatedAt,
		UpdatedAt:     board.UpdatedAt,
		Board:         encoded,
	}, nil
}

type boardSummary struct {
	SchemaVersion uint64 `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Revision      uint64 `json:"revision"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type boardLoadError struct {
	File    string `json:"file"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *Handler) listBoards(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	readable, unreadable, err := h.boards.List(r.Context(), project.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	// One unreadable file must not make a project's other boards unreachable,
	// so a load failure is reported beside the boards that did load.
	summaries := make([]boardSummary, 0, len(readable))
	for _, id := range readable {
		board, loadErr := h.boards.Load(r.Context(), project.ID, id)
		if loadErr != nil {
			unreadable = append(unreadable, id)
			continue
		}
		summaries = append(summaries, boardSummary{
			SchemaVersion: board.SchemaVersion, ID: board.ID, Name: board.Name, Revision: board.Revision,
			CreatedAt: board.CreatedAt.UTC().Format(timeLayout),
			UpdatedAt: board.UpdatedAt.UTC().Format(timeLayout),
		})
	}
	failures := make([]boardLoadError, 0, len(unreadable))
	for _, id := range unreadable {
		failures = append(failures, boardLoadError{
			File: id, Code: CodeCorruptDocument, Message: "this workflow file could not be read",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "boards": summaries, "errors": failures})
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

func (h *Handler) readBoard(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	board, err := h.boards.Load(r.Context(), project.ID, r.PathValue("boardId"))
	if err != nil {
		writeError(w, err)
		return
	}
	document, err := boardDocument(board)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "board": document})
}

func (h *Handler) writeBoard(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Name             string          `json:"name"`
		Board            json.RawMessage `json:"board"`
		ExpectedRevision uint64          `json:"expectedRevision"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var board Board
	if len(body.Board) > 0 {
		if err := json.Unmarshal(body.Board, &board); err != nil {
			writeFailure(w, http.StatusBadRequest, newError(CodeCorruptDocument, "the board document is not valid JSON"))
			return
		}
	}
	// Identity is taken from the route and the opened project, never from the
	// document: a board that named another project would otherwise be able to
	// write into that project's storage.
	board.ID = r.PathValue("boardId")
	board.ProjectID = project.ID
	board.ProjectPath = project.Path
	board.Name = body.Name

	saved, err := h.boards.Save(r.Context(), &board, body.ExpectedRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	document, err := boardDocument(saved)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "board": document})
}

func (h *Handler) deleteBoard(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	revisionValue, err := strconv.ParseUint(r.URL.Query().Get("expectedRevision"), 10, 64)
	if err != nil {
		writeFailure(w, http.StatusBadRequest,
			policyError("expectedRevision", "the expected revision is required to delete a workflow"))
		return
	}
	if err := h.boards.Delete(r.Context(), project.ID, r.PathValue("boardId"), revisionValue); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

type runPreview struct {
	ProjectPath      string `json:"projectPath"`
	BaseBranch       string `json:"baseBranch"`
	BaseSha          string `json:"baseSha"`
	DefaultBranch    string `json:"defaultBranch"`
	CheckoutBranch   string `json:"checkoutBranch"`
	IsLinkedWorktree bool   `json:"isLinkedWorktree"`
	RemoteURL        string `json:"remoteUrl"`
	RunRootParent    string `json:"runRootParent"`
}

func (h *Handler) previewRun(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	var body struct {
		BoardID string          `json:"boardId"`
		Board   json.RawMessage `json:"board"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	// A saved board is preferred. An inline draft is allowed so the run dialog
	// can preview before the workflow has been named and saved — starting still
	// requires a stored revision.
	var base string
	switch {
	case strings.TrimSpace(body.BoardID) != "":
		board, err := h.boards.Load(r.Context(), project.ID, body.BoardID)
		if err != nil {
			writeError(w, err)
			return
		}
		base = board.Components.Publish.Publish.Base
	case len(body.Board) > 0:
		var draft Board
		if err := json.Unmarshal(body.Board, &draft); err != nil {
			writeFailure(w, http.StatusBadRequest, newError(CodeCorruptDocument, "the board document is not valid JSON"))
			return
		}
		Normalize(&draft)
		base = draft.Components.Publish.Publish.Base
	default:
		writeFailure(w, http.StatusBadRequest, policyError("boardId", "a preview needs a workflow"))
		return
	}

	preflight, err := h.git.RunPreflight(r.Context(), revision.PreflightOptions{
		ProjectPath: project.Path,
		BaseBranch:  base,
		// DaGama publishes, so it requires a repository: a plain folder has no
		// base commit to authorize a pull request against.
		AllowPlainFolder: false,
	})
	if err != nil {
		writeError(w, translatePreflightError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preview": runPreview{
		ProjectPath:      preflight.Toplevel,
		BaseBranch:       preflight.BaseBranch,
		BaseSha:          preflight.BaseSha,
		DefaultBranch:    preflight.DefaultBranch,
		CheckoutBranch:   preflight.CheckoutBranch,
		IsLinkedWorktree: preflight.IsLinkedWorktree,
		RemoteURL:        preflight.RemoteURL,
		RunRootParent:    h.controller.RunRootsDirectory(),
	}})
}

// translatePreflightError keeps a git failure inside the frozen envelope.
//
// safeMessage withholds anything that is not already a client-safe message, so
// a git invocation's stderr — which can quote private paths — never reaches the
// browser.
func translatePreflightError(err error) error {
	var failure *Error
	if errors.As(err, &failure) {
		return err
	}
	return (&Error{Code: CodePolicyViolation, Message: safeMessage(err), Field: "projectPath"}).withCause(err)
}

func (h *Handler) startRun(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	var body struct {
		BoardID string `json:"boardId"`
		Source  struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
			Text  string `json:"text"`
			Path  string `json:"path"`
		} `json:"source"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	// StartAsync refuses everything refusable before the run exists, so a bad
	// request is a 4xx here rather than a run that fails on a goroutine.
	state, advance, err := h.controller.StartAsync(r.Context(), StartRequest{
		ProjectID: project.ID,
		BoardID:   body.BoardID,
		Source: SourceInput{
			Kind: body.Source.Kind, Title: body.Source.Title,
			Text: body.Source.Text, Path: body.Source.Path,
		},
	})
	if err != nil {
		writeError(w, err)
		return
	}
	h.launch(func() { _, _ = advance(h.background) })
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "run": state})
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	summaries, err := h.controller.ListRuns(r.Context(), project.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if summaries == nil {
		summaries = []RunSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runs": summaries, "errors": []any{}})
}

func (h *Handler) readRun(w http.ResponseWriter, r *http.Request) {
	project, runID, ok := h.runScope(w, r)
	if !ok {
		return
	}
	state, err := h.controller.ReadRun(r.Context(), project.ID, runID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": state})
}

func (h *Handler) readArtifact(w http.ResponseWriter, r *http.Request) {
	project, runID, ok := h.runScope(w, r)
	if !ok {
		return
	}
	contents, err := h.controller.ReadArtifact(r.Context(), project.ID, runID, r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	// The contents are returned as a JSON string and rendered as text by the
	// client. An artifact can quote the run's untrusted source verbatim, so it
	// is never served as markup.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "contents": string(contents)})
}

func (h *Handler) readPrompt(w http.ResponseWriter, r *http.Request) {
	project, runID, ok := h.runScope(w, r)
	if !ok {
		return
	}
	component, err := seatComponent(r.URL.Query().Get("componentId"))
	if err != nil {
		writeError(w, err)
		return
	}
	prompt, err := h.controller.AssembledPrompt(r.Context(), project.ID, runID, component)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "contents": prompt})
}

func (h *Handler) publishPreflight(w http.ResponseWriter, r *http.Request) {
	project, runID, ok := h.runScope(w, r)
	if !ok {
		return
	}
	request, err := h.controller.PublishRequest(r.Context(), project.ID, runID)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := h.publisher.Preflight(r.Context(), request)
	if err != nil {
		writeError(w, err)
		return
	}
	if result.Checklist == nil {
		result.Checklist = []publication.ChecklistItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preflight": result})
}

// ---------------------------------------------------------------------------
// Controls
// ---------------------------------------------------------------------------

// control checks a transition against the current run and, if the controller
// would accept it, detaches the work and answers with the run as it stands.
//
// The client polls a live run, so it converges on the result within a tick. The
// point of returning early is that the operator learns immediately whether the
// control was accepted, which is the part a timeout would destroy.
func (h *Handler) control(
	w http.ResponseWriter,
	r *http.Request,
	check func(*RunState) error,
	run func(context.Context, string, string),
) {
	project, runID, ok := h.runScope(w, r)
	if !ok {
		return
	}
	state, err := h.controller.ReadRun(r.Context(), project.ID, runID)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := check(state); err != nil {
		writeError(w, err)
		return
	}
	projectID := project.ID
	h.launch(func() { run(h.background, projectID, runID) })
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": state})
}

// seatControl is `control` for the transitions that name a seat.
func (h *Handler) seatControl(
	w http.ResponseWriter,
	r *http.Request,
	check func(*RunState, ComponentID) error,
	run func(context.Context, string, string, ComponentID),
) {
	var body struct {
		ComponentID string `json:"componentId"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	component, err := seatComponent(body.ComponentID)
	if err != nil {
		writeError(w, err)
		return
	}
	h.control(w, r,
		func(state *RunState) error { return check(state, component) },
		func(ctx context.Context, projectID, runID string) { run(ctx, projectID, runID, component) },
	)
}

func (h *Handler) retrySeat(w http.ResponseWriter, r *http.Request) {
	h.seatControl(w, r, CanRetry, func(ctx context.Context, projectID, runID string, component ComponentID) {
		_, _ = h.controller.Retry(ctx, projectID, runID, component)
	})
}

func (h *Handler) takeoverSeat(w http.ResponseWriter, r *http.Request) {
	h.seatControl(w, r, CanTakeover, func(ctx context.Context, projectID, runID string, component ComponentID) {
		_, _ = h.controller.Takeover(ctx, projectID, runID, component)
	})
}

func (h *Handler) handbackSeat(w http.ResponseWriter, r *http.Request) {
	h.seatControl(w, r, CanHandback, func(ctx context.Context, projectID, runID string, component ComponentID) {
		_, _ = h.controller.Handback(ctx, projectID, runID, component)
	})
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	// A component may be named for symmetry with the seat controls, but cancel
	// is a run-level operation in the controller and is applied as one.
	var body struct {
		ComponentID string `json:"componentId"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	h.control(w, r, CanCancel, func(ctx context.Context, projectID, runID string) {
		_, _ = h.controller.Cancel(ctx, projectID, runID)
	})
}

func (h *Handler) decideGate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Publish  *bool  `json:"publish"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var decision GateDecision
	switch body.Decision {
	case string(GateApproved):
		decision = GateApproved
	case string(GateRejected):
		decision = GateRejected
	default:
		writeError(w, policyError("decision", "the decision must be approved or rejected"))
		return
	}
	// Approving without publishing is a distinct operator intent, not a variant
	// of approval: it marks the run done without commit, push, or pull request.
	skip := decision == GateApproved && body.Publish != nil && !*body.Publish
	message := "the operator decided the gate"
	if skip {
		message = PublishSkippedMessage
	}
	h.control(w, r, CanDecideGate, func(ctx context.Context, projectID, runID string) {
		_, _ = h.controller.DecideGateWithOptions(
			ctx, projectID, runID, decision, message, GateDecisionOptions{SkipPublication: skip},
		)
	})
}

// ---------------------------------------------------------------------------
// Terminals
// ---------------------------------------------------------------------------

func (h *Handler) attachTerminal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ComponentID string `json:"componentId"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	component, err := seatComponent(body.ComponentID)
	if err != nil {
		writeError(w, err)
		return
	}
	project, runID, ok := h.runScope(w, r)
	if !ok {
		return
	}
	state, err := h.controller.ReadRun(r.Context(), project.ID, runID)
	if err != nil {
		writeError(w, err)
		return
	}
	current := state.Components[component]
	if current == nil || current.Attempt == nil {
		writeError(w, newError(CodeInvalidState, "this seat has no attempt to attach to"))
		return
	}
	attempt := *current.Attempt

	// The write policy is the server's, not the client's: an automated turn is
	// attached read-only, so watching a seat can never inject keystrokes into a
	// turn the controller is driving. Taking control is the transition that
	// makes a terminal writable, and it is a separate, checked control.
	writable := attempt.Ownership == OwnershipHumanControlled && attempt.Status != AttemptExitedStatus
	// The pane is preserved on detach: the run owns its lifetime, and closing a
	// viewer must not end an agent's turn.
	status, err := h.terminals.Adopt(r.Context(), attempt.AttemptID, attempt.TmuxName, state.RunRoot, writable, true)
	if err != nil {
		if errors.Is(err, terminal.ErrNotFound) {
			writeError(w, newError(CodeNotFound, "this seat terminal is no longer running"))
			return
		}
		writeError(w, newError(CodeInvalidState, "this seat terminal could not be attached"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"terminalId": status.TerminalID,
		"attemptId":  attempt.AttemptID,
		"writable":   status.Writable,
	})
}
