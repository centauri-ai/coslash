package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	"github.com/centauri-ai/coslash/collector/internal/vendors/cursor"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

var deleteUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var deleteOpenCodePattern = regexp.MustCompile(`^ses_[0-9A-Za-z]+$`)

func validDeleteSessionID(agent, id string) bool {
	switch agent {
	case vendors.AgentClaude:
		return deleteUUIDPattern.MatchString(id)
	case vendors.AgentCodex:
		return deleteUUIDPattern.MatchString(id) && id == strings.ToLower(id)
	case vendors.AgentCursor:
		return deleteUUIDPattern.MatchString(id) && id[14] >= '1' && id[14] <= '5' && strings.ContainsRune("89abAB", rune(id[19]))
	case vendors.AgentOpenCode:
		return len(id) <= 128 && deleteOpenCodePattern.MatchString(id)
	default:
		return false
	}
}

func newDeleteSessionHandler(deleteSession func(context.Context, string, string) error) http.HandlerFunc {
	// Vendor operations rewrite shared history and databases, even for different IDs.
	gate := make(chan struct{}, 1)
	return func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query["source"]) != 1 || len(query["agent"]) != 1 || len(query["id"]) != 1 || query.Get("source") == "" || !validDeleteSessionID(query.Get("agent"), query.Get("id")) {
			writeAPIError(w, http.StatusBadRequest, "invalid_session", "Invalid session identity.")
			return
		}
		source, err := parseSourceID(query.Get("source"))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_session", "Invalid session identity.")
			return
		}
		if source != localSourceID {
			writeAPIError(w, http.StatusConflict, errCodeRemoteUnsupported, "remote action unsupported")
			return
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-r.Context().Done():
			writeDeleteSessionError(w, r.Context().Err())
			return
		}
		if err := r.Context().Err(); err != nil {
			writeDeleteSessionError(w, err)
			return
		}
		if err := deleteSession(r.Context(), query.Get("agent"), query.Get("id")); err != nil {
			writeDeleteSessionError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteLocalSession(ctx context.Context, agent, id string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch agent {
	case vendors.AgentClaude:
		return claude.DeleteSession(ctx, home, id)
	case vendors.AgentCodex:
		return codex.DeleteSession(ctx, home, id)
	case vendors.AgentCursor:
		return cursor.DeleteSession(ctx, home, id)
	case vendors.AgentOpenCode:
		return opencode.DeleteSession(ctx, home, id)
	default:
		return errors.New("invalid deletion agent")
	}
}

func writeDeleteSessionError(w http.ResponseWriter, err error) {
	log.Printf("delete session: %.2048q", err.Error())
	status, code, message := http.StatusInternalServerError, "session_delete_failed", "Could not delete session. Retry to complete cleanup."
	switch {
	case errors.Is(err, claude.ErrSessionFailed), errors.Is(err, codex.ErrSessionDeleteFailed), errors.Is(err, cursor.ErrDeleteFailed), errors.Is(err, opencode.ErrSessionDeleteFailed), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
	case errors.Is(err, claude.ErrSessionInvalid), errors.Is(err, codex.ErrSessionInvalid), errors.Is(err, cursor.ErrDeleteInvalid), errors.Is(err, opencode.ErrInvalidSession):
		status, code, message = http.StatusBadRequest, "invalid_session", "Invalid session identity."
	case errors.Is(err, claude.ErrSessionActive), errors.Is(err, codex.ErrSessionActive), errors.Is(err, cursor.ErrDeleteActive), errors.Is(err, opencode.ErrSessionActive):
		status, code, message = http.StatusConflict, "session_active", "Session is active. Close it before deleting."
	case errors.Is(err, claude.ErrSessionUnverified), errors.Is(err, codex.ErrSessionUnverified), errors.Is(err, cursor.ErrDeleteUnverified), errors.Is(err, opencode.ErrSessionUnverified):
		status, code, message = http.StatusConflict, "session_unverified", "Could not verify that the session can be safely deleted."
	case errors.Is(err, claude.ErrSessionMissing), errors.Is(err, codex.ErrSessionMissing), errors.Is(err, cursor.ErrDeleteMissing), errors.Is(err, opencode.ErrSessionMissing):
		status, code, message = http.StatusNotFound, "session_missing", "Session was not found."
	}
	writeAPIError(w, status, code, message)
}
