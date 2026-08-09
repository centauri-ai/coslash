package sessioncanvas

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/agentexec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/persistence"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessiondetail"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const (
	defaultMaxFileBytes   int64 = 2 << 20
	defaultMaxPromptBytes       = 2_000
	defaultAnalysisCache        = 256
	maxJSONBody           int64 = 32 << 10
)

type Handler struct {
	sessions       SessionResolver
	projector      DetailProjector
	renamer        Renamer
	workspaces     WorkspaceRegistrar
	terminals      TerminalService
	terminalAPI    TerminalAPI
	analyzer       TurnAnalyzer
	newUUID        func() (string, error)
	maxFileBytes   int64
	maxPromptBytes int
	cache          *analysisCache
}

func New(options Options) (*Handler, error) {
	if options.Sessions == nil || options.Projector == nil || options.Renamer == nil ||
		options.Workspaces == nil || options.Terminals == nil || options.TerminalAPI == nil {
		return nil, errors.New("session canvas: incomplete dependencies")
	}
	if options.NewUUID == nil {
		options.NewUUID = randomUUID
	}
	if options.MaxFileBytes == 0 {
		options.MaxFileBytes = defaultMaxFileBytes
	}
	if options.MaxPromptBytes == 0 {
		options.MaxPromptBytes = defaultMaxPromptBytes
	}
	if options.AnalysisCache == 0 {
		options.AnalysisCache = defaultAnalysisCache
	}
	if options.MaxFileBytes < 1 || options.MaxPromptBytes < 1 || options.AnalysisCache < 1 {
		return nil, errors.New("session canvas: invalid bounds")
	}
	return &Handler{
		sessions: options.Sessions, projector: options.Projector, renamer: options.Renamer,
		workspaces: options.Workspaces, terminals: options.Terminals, terminalAPI: options.TerminalAPI,
		analyzer: options.Analyzer, newUUID: options.NewUUID, maxFileBytes: options.MaxFileBytes,
		maxPromptBytes: options.MaxPromptBytes, cache: newAnalysisCache(options.AnalysisCache),
	}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/canvas/sessions/{agent}/{id}", h.handleDetail)
	mux.HandleFunc("PUT /api/canvas/sessions/{agent}/{id}/name", h.handleRename)
	mux.HandleFunc("POST /api/canvas/sessions/{agent}/{id}/fork", h.handleFork)
	mux.HandleFunc("POST /api/canvas/sessions/{agent}/{id}/turns/{turn}/analysis", h.handleAnalysis)
	mux.HandleFunc("GET /api/canvas/sessions/{agent}/{id}/files", h.handleFile)
	mux.HandleFunc("POST /api/canvas/sessions/{agent}/{id}/terminal", h.handleTerminal)
	h.workspaces.Register(mux)
	mux.HandleFunc("GET /api/terminals/{terminalId}", func(w http.ResponseWriter, r *http.Request) {
		h.terminalAPI.Status(w, r, r.PathValue("terminalId"))
	})
	mux.HandleFunc("POST /api/terminals/{terminalId}/input", func(w http.ResponseWriter, r *http.Request) {
		h.terminalAPI.Input(w, r, r.PathValue("terminalId"))
	})
	mux.HandleFunc("POST /api/terminals/{terminalId}/stop", func(w http.ResponseWriter, r *http.Request) {
		h.terminalAPI.Stop(w, r, r.PathValue("terminalId"))
	})
	mux.HandleFunc("GET /api/terminals/{terminalId}/ws", func(w http.ResponseWriter, r *http.Request) {
		h.terminalAPI.WebSocket(w, r, r.PathValue("terminalId"))
	})
	for _, pattern := range []string{
		"/api/canvas/sessions/{agent}/{id}",
		"/api/canvas/sessions/{agent}/{id}/name",
		"/api/canvas/sessions/{agent}/{id}/fork",
		"/api/canvas/sessions/{agent}/{id}/turns/{turn}/analysis",
		"/api/canvas/sessions/{agent}/{id}/files",
		"/api/canvas/sessions/{agent}/{id}/terminal",
		"/api/terminals/{terminalId}",
		"/api/terminals/{terminalId}/input",
		"/api/terminals/{terminalId}/stop",
		"/api/terminals/{terminalId}/ws",
	} {
		mux.HandleFunc(pattern, methodNotAllowed)
	}
}

func (h *Handler) handleDetail(w http.ResponseWriter, r *http.Request) {
	resolved, identity, ok := h.resolve(w, r)
	if !ok {
		return
	}
	detail, err := h.projector.ProjectKnown(r.Context(), resolved.Session, resolved.TranscriptPath)
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	if detail.Agent != identity.Agent || detail.ID != identity.ID {
		writeError(w, http.StatusConflict, "SESSION_IDENTITY_MISMATCH", "session identity changed while it was being read", "")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleRename(w http.ResponseWriter, r *http.Request) {
	_, identity, ok := h.resolve(w, r)
	if !ok {
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 200 || !utf8.ValidString(name) || strings.ContainsAny(name, "\r\n") || strings.ContainsRune(name, 0) {
		writeError(w, http.StatusBadRequest, "INVALID_NAME", "name must be 1-200 characters on one line", "name")
		return
	}
	if err := h.renamer.Rename(r.Context(), identity, name); err != nil {
		if errors.Is(err, ErrRenameUnsupported) {
			writeError(w, http.StatusConflict, "RENAME_UNSUPPORTED", "this session cannot be renamed", "")
		} else {
			writeError(w, http.StatusInternalServerError, "RENAME_FAILED", "session rename failed", "")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

func (h *Handler) handleTerminal(w http.ResponseWriter, r *http.Request) {
	resolved, identity, ok := h.resolve(w, r)
	if !ok {
		return
	}
	request, ok := h.actionRequest(w, r, false)
	if !ok {
		return
	}
	id := stableTerminalID(identity)
	name, err := terminalName("session", identity.Agent, identity.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TERMINAL_ERROR", "terminal identity could not be created", "")
		return
	}
	requestedWritable := writable(request.Writable)
	if status, err := h.terminals.Status(r.Context(), id); err == nil {
		if status.State != "exited" {
			writeJSON(w, http.StatusOK, terminalResponse{OK: true, Reused: true, Terminal: status})
			return
		}
		if err := h.terminals.Stop(r.Context(), id); err != nil && !errors.Is(err, terminal.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "TERMINAL_ERROR", "terminal could not be restarted", "")
			return
		}
	} else if !errors.Is(err, terminal.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "TERMINAL_ERROR", "terminal status is unavailable", "")
		return
	}
	command, err := agentexec.Build(agentexec.Request{
		Vendor: vendor(identity.Agent), Mode: agentexec.Resume, CWD: resolved.Session.WorkingDirectory,
		ParentSessionID: identity.ID, Model: request.Model, Effort: request.Effort,
		Permission: request.Permission, Prompt: request.Prompt,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_TERMINAL_REQUEST", "terminal request is invalid", "")
		return
	}
	if status, err := h.terminals.Adopt(
		r.Context(), id, name, command.Dir, requestedWritable, true,
	); err == nil {
		writeJSON(w, http.StatusOK, terminalResponse{OK: true, Reused: true, Terminal: status})
		return
	} else if !errors.Is(err, terminal.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "TERMINAL_ERROR", "terminal could not be reconnected", "")
		return
	}
	status, err := h.terminals.Create(r.Context(), terminal.Spec{
		ID: id, TmuxName: name, Command: command, Writable: requestedWritable, PreserveOnClose: true,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "TERMINAL_START_FAILED", "terminal could not be started", "")
		return
	}
	writeJSON(w, http.StatusOK, terminalResponse{OK: true, Terminal: status})
}

func (h *Handler) handleFork(w http.ResponseWriter, r *http.Request) {
	resolved, identity, ok := h.resolve(w, r)
	if !ok {
		return
	}
	request, ok := h.actionRequest(w, r, true)
	if !ok {
		return
	}
	uuid, err := h.newUUID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FORK_FAILED", "experiment could not be created", "")
		return
	}
	childID := ""
	if identity.Agent == vendors.AgentClaude {
		childID = uuid
	}
	command, err := agentexec.Build(agentexec.Request{
		Vendor: vendor(identity.Agent), Mode: agentexec.Fork, CWD: resolved.Session.WorkingDirectory,
		SessionID: childID, ParentVendor: vendor(identity.Agent), ParentSessionID: identity.ID,
		Model: request.Model, Effort: request.Effort, Permission: request.Permission, Prompt: request.Prompt,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_FORK_REQUEST", "experiment request is invalid", "")
		return
	}
	terminalID := "experiment-" + strings.ReplaceAll(uuid, "-", "")
	name, err := terminalName("experiment", identity.Agent, identity.ID, uuid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FORK_FAILED", "experiment identity could not be created", "")
		return
	}
	status, err := h.terminals.Create(r.Context(), terminal.Spec{
		ID: terminalID, TmuxName: name, Command: command, Writable: writable(request.Writable), PreserveOnClose: true,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "FORK_FAILED", "experiment could not be started", "")
		return
	}
	var responseChild *string
	if childID != "" {
		responseChild = &childID
	}
	writeJSON(w, http.StatusOK, terminalResponse{OK: true, Terminal: status, ChildSessionID: responseChild})
}

func (h *Handler) handleAnalysis(w http.ResponseWriter, r *http.Request) {
	resolved, identity, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if !emptyBody(w, r) {
		return
	}
	turnIndex, err := strconv.Atoi(r.PathValue("turn"))
	if err != nil || turnIndex < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_TURN", "turn is invalid", "turn")
		return
	}
	detail, err := h.projector.ProjectKnown(r.Context(), resolved.Session, resolved.TranscriptPath)
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	var selected *sessiondetail.Turn
	for index := range detail.TurnLog {
		if detail.TurnLog[index].Index == turnIndex {
			selected = &detail.TurnLog[index]
			break
		}
	}
	if selected == nil {
		writeError(w, http.StatusNotFound, "TURN_NOT_FOUND", "turn not found", "")
		return
	}
	key := turnCacheKey(identity, *selected)
	if result, found := h.cache.get(key); found {
		writeJSON(w, http.StatusOK, analysisResponse{OK: true, CacheKey: key, Cached: true, Analysis: result})
		return
	}
	if h.analyzer == nil {
		writeError(w, http.StatusConflict, "ANALYSIS_DISABLED", "turn analysis is disabled", "")
		return
	}
	result, err := h.analyzer.Analyze(r.Context(), TurnAnalysisInput{Session: identity, Turn: *selected, Detail: detail})
	if err != nil {
		if errors.Is(err, ErrAnalysisDisabled) {
			writeError(w, http.StatusConflict, "ANALYSIS_DISABLED", "turn analysis is disabled", "")
		} else {
			writeError(w, http.StatusBadGateway, "ANALYSIS_FAILED", "turn analysis failed", "")
		}
		return
	}
	if validateAnalysis(result) != nil {
		writeError(w, http.StatusBadGateway, "ANALYSIS_INVALID", "turn analysis returned an invalid result", "")
		return
	}
	h.cache.put(key, result)
	writeJSON(w, http.StatusOK, analysisResponse{OK: true, CacheKey: key, Analysis: result})
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) (ResolvedSession, contracts.SessionIdentity, bool) {
	identity := contracts.SessionIdentity{Agent: r.PathValue("agent"), ID: r.PathValue("id")}
	if (identity.Agent != vendors.AgentClaude && identity.Agent != vendors.AgentCodex) || persistence.ValidateSession(identity) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SESSION", "session identity is invalid", "session")
		return ResolvedSession{}, contracts.SessionIdentity{}, false
	}
	resolved, err := h.sessions.Resolve(r.Context(), identity)
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "session not found", "")
		case errors.Is(err, ErrSessionAmbiguous):
			writeError(w, http.StatusConflict, "SESSION_AMBIGUOUS", "session identity is ambiguous", "")
		default:
			writeError(w, http.StatusInternalServerError, "SESSION_LOOKUP_FAILED", "session lookup failed", "")
		}
		return ResolvedSession{}, contracts.SessionIdentity{}, false
	}
	if resolved.Session.Agent != identity.Agent || resolved.Session.ID != identity.ID {
		writeError(w, http.StatusConflict, "SESSION_IDENTITY_MISMATCH", "session identity changed while it was being resolved", "")
		return ResolvedSession{}, contracts.SessionIdentity{}, false
	}
	return resolved, identity, true
}

func (h *Handler) actionRequest(w http.ResponseWriter, r *http.Request, promptRequired bool) (actionRequest, bool) {
	var request actionRequest
	if !decodeRequest(w, r, &request) {
		return actionRequest{}, false
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if (promptRequired && request.Prompt == "") || len(request.Prompt) > h.maxPromptBytes || strings.ContainsRune(request.Prompt, 0) {
		writeError(w, http.StatusBadRequest, "INVALID_PROMPT", fmt.Sprintf("prompt must be at most %d bytes", h.maxPromptBytes), "prompt")
		return actionRequest{}, false
	}
	return request, true
}

func vendor(agent string) agentexec.Vendor { return agentexec.Vendor(agent) }
func writable(value *bool) bool            { return value == nil || *value }

func stableTerminalID(identity contracts.SessionIdentity) string {
	digest := sha256.Sum256([]byte(identity.Agent + "\x00" + identity.ID))
	return "session-" + hex.EncodeToString(digest[:12])
}

func terminalName(namespace string, identity ...string) (string, error) {
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return terminal.Name(namespace, string(encoded))
}

func randomUUID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	value := hex.EncodeToString(data)
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:], nil
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if header := r.Header.Get("Content-Type"); header != "" {
		mediaType, _, err := mime.ParseMediaType(header)
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_CONTENT_TYPE", "request must use application/json", "")
			return false
		}
	}
	reader := http.MaxBytesReader(w, r.Body, maxJSONBody)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "MALFORMED_REQUEST", "request body is invalid", "")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "MALFORMED_REQUEST", "request body is invalid", "")
		return false
	}
	return true
}

func emptyBody(w http.ResponseWriter, r *http.Request) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if err != nil || len(data) != 0 {
		writeError(w, http.StatusBadRequest, "BODY_NOT_ALLOWED", "request body is not allowed", "")
		return false
	}
	return true
}

func writeProjectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sessiondetail.ErrNotFound):
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "session transcript not found", "")
	case errors.Is(err, sessiondetail.ErrUnknownAgent), errors.Is(err, sessiondetail.ErrIdentityMismatch):
		writeError(w, http.StatusConflict, "SESSION_IDENTITY_MISMATCH", "session transcript identity is invalid", "")
	case errors.Is(err, sessiondetail.ErrTranscriptTooLarge), errors.Is(err, sessiondetail.ErrProjectionTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "SESSION_TOO_LARGE", "session detail exceeds the configured limit", "")
	default:
		writeError(w, http.StatusUnprocessableEntity, "SESSION_DETAIL_FAILED", "session detail could not be produced", "")
	}
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", "")
}

func writeError(w http.ResponseWriter, status int, code, message, field string) {
	writeJSON(w, status, contracts.ErrorResponse{OK: false, Code: code, Error: message, Field: field})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
