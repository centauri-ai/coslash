package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
)

// workspaceRoutePrefix is the frozen route group owned by this package.
const workspaceRoutePrefix = "/api/canvas/workspaces/"

// maxRequestBytes bounds a decoded request body independently of the state
// bound so an oversized upload is refused before it is buffered.
const requestHeadroom int64 = 64 << 10

// Handler serves the frozen workspace persistence routes:
//
//	GET /api/canvas/workspaces/{agent}/{id}
//	PUT /api/canvas/workspaces/{agent}/{id}
//
// Authentication, origin, host, and method-level guards remain the caller's
// responsibility; this handler is mounted behind the existing coSlash guards.
func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+workspaceRoutePrefix+"{agent}/{id}", s.handleLoad)
	mux.HandleFunc("PUT "+workspaceRoutePrefix+"{agent}/{id}", s.handleSave)
	mux.HandleFunc(workspaceRoutePrefix+"{agent}/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, contracts.ErrorResponse{
			Code:  CodeMethodNotAllowed,
			Error: "Only GET and PUT are supported for a workspace.",
		})
	})
	return mux
}

// Register mounts the workspace routes on a shared mux. It registers only the
// route group frozen for this package in CONTRACTS.md.
func (s *Store) Register(mux *http.ServeMux) {
	mux.Handle(workspaceRoutePrefix, s.Handler())
}

func (s *Store) handleLoad(w http.ResponseWriter, r *http.Request) {
	session, ok := routeSession(w, r)
	if !ok {
		return
	}
	document, err := s.Load(r.Context(), session)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (s *Store) handleSave(w http.ResponseWriter, r *http.Request) {
	session, ok := routeSession(w, r)
	if !ok {
		return
	}
	if !checkContentType(w, r) {
		return
	}

	limit := s.maxStateBytes + requestHeadroom
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		writeError(w, http.StatusBadRequest, contracts.ErrorResponse{
			Code:  CodeMalformedRequest,
			Error: "The request body could not be read.",
		})
		return
	}
	if int64(len(body)) > limit {
		writeError(w, http.StatusRequestEntityTooLarge, contracts.ErrorResponse{
			Code:  CodeRequestTooLarge,
			Error: "The request body is too large.",
		})
		return
	}

	var write contracts.WorkspaceWrite
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&write); err != nil || decoder.More() {
		writeError(w, http.StatusBadRequest, contracts.ErrorResponse{
			Code:  CodeMalformedRequest,
			Error: "The request body is not a valid workspace write.",
		})
		return
	}

	document, err := s.Save(r.Context(), session, write)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func routeSession(w http.ResponseWriter, r *http.Request) (contracts.SessionIdentity, bool) {
	// PathValue is already percent-decoded by net/http.
	session := contracts.SessionIdentity{
		Agent: r.PathValue("agent"),
		ID:    r.PathValue("id"),
	}
	if err := ValidateSession(session); err != nil {
		field := "agent"
		if strings.Contains(err.Error(), "id ") {
			field = "id"
		}
		writeError(w, http.StatusBadRequest, contracts.ErrorResponse{
			Code:  CodeInvalidSession,
			Error: Message(err),
			Field: field,
		})
		return contracts.SessionIdentity{}, false
	}
	return session, true
}

func checkContentType(w http.ResponseWriter, r *http.Request) bool {
	header := r.Header.Get("Content-Type")
	if header == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, contracts.ErrorResponse{
			Code:  CodeUnsupportedContent,
			Error: "The request must use application/json.",
		})
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	response := contracts.ErrorResponse{Code: Code(err), Error: Message(err)}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrInvalidSession):
		status = http.StatusBadRequest
		response.Field = "session"
	case errors.Is(err, ErrInvalidState):
		status = http.StatusBadRequest
		response.Field = "state"
	case errors.Is(err, ErrSchemaUnsupported):
		status = http.StatusBadRequest
		response.Field = "schemaVersion"
	case errors.Is(err, ErrStateTooLarge):
		status = http.StatusRequestEntityTooLarge
		response.Field = "state"
	case errors.Is(err, ErrQuotaExceeded):
		status = http.StatusInsufficientStorage
	case errors.Is(err, ErrCorrupt):
		status = http.StatusConflict
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
		response.Field = "expectedRevision"
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			actual := conflict.Actual
			response.ActualRevision = &actual
		}
	}
	writeError(w, status, response)
}

func writeError(w http.ResponseWriter, status int, response contracts.ErrorResponse) {
	response.OK = false
	writeJSON(w, status, response)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}
