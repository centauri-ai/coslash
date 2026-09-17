package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const (
	errCodeDetailStale   = "session_detail_stale"
	errCodeDetailMissing = "session_detail_missing"
	errCodeDetailCorrupt = "session_detail_corrupt"
	errCodeChangeMissing = "session_change_missing"
)

type localDetailReader func(agent, sessionID string) (*session.Session, error)

type exactSessionIdentity struct {
	SourceID  string
	Agent     string
	SessionID string
	Revision  string
}

type sessionDetailResponse struct {
	SourceID      string          `json:"sourceId"`
	Agent         string          `json:"agent"`
	SessionID     string          `json:"sessionId"`
	Revision      string          `json:"revision"`
	CachedOffline bool            `json:"cachedOffline"`
	Session       session.Session `json:"session"`
}

func handleSessionDetail(w http.ResponseWriter, r *http.Request, getLocal localDetailReader, remoteManager *remote.Manager) {
	identity, ok := parseExactSessionIdentity(w, r)
	if !ok {
		return
	}

	var value *session.Session
	cachedOffline := false
	if identity.SourceID == localSourceID {
		found, err := getLocal(identity.Agent, identity.SessionID)
		if err != nil {
			writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
			return
		}
		if found == nil {
			writeDetailError(w, errCodeDetailMissing, http.StatusNotFound)
			return
		}
		if identity.Revision != strconv.FormatInt(found.LastActivityTime, 10) {
			writeDetailError(w, errCodeDetailStale, http.StatusConflict)
			return
		}
		copy := withLocalChangeIDs(*found)
		value = &copy
	} else {
		record, err := remoteManager.ReadFullSession(identity.SourceID, identity.Agent, identity.SessionID, identity.Revision)
		if err != nil {
			writeRemoteDetailError(w, err)
			return
		}
		if record == nil {
			writeDetailError(w, errCodeDetailMissing, http.StatusNotFound)
			return
		}
		value, err = fullsessionrecord.ToSession(*record)
		if err != nil {
			writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
			return
		}
		health := remoteManager.DiagnosticsHealth()
		cachedOffline = health.State != remote.StateOK && health.State != remote.StateLimited
	}

	writeJSON(w, sessionDetailResponse{
		SourceID: identity.SourceID, Agent: identity.Agent, SessionID: identity.SessionID,
		Revision: identity.Revision, CachedOffline: cachedOffline,
		Session: sessionWithJSONCollections(*value),
	})
}

func handleExactDiff(w http.ResponseWriter, r *http.Request, getLocal localDetailReader, remoteManager *remote.Manager) {
	identity, ok := parseExactSessionIdentity(w, r)
	if !ok {
		return
	}
	changeIDs := r.URL.Query()["change"]
	if len(changeIDs) > fullsessionv1.MaxItems {
		http.Error(w, "too many changes", http.StatusBadRequest)
		return
	}
	for _, changeID := range changeIDs {
		if !validOpaqueIdentifier(changeID) {
			http.Error(w, "invalid change", http.StatusBadRequest)
			return
		}
	}

	changes := make([]session.FileChange, 0, len(changeIDs))
	if identity.SourceID == localSourceID {
		found, err := getLocal(identity.Agent, identity.SessionID)
		if err != nil {
			writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
			return
		}
		if found == nil {
			writeDetailError(w, errCodeDetailMissing, http.StatusNotFound)
			return
		}
		if identity.Revision != strconv.FormatInt(found.LastActivityTime, 10) {
			writeDetailError(w, errCodeDetailStale, http.StatusConflict)
			return
		}
		available := localChangesByID(withLocalChangeIDs(*found))
		for _, changeID := range changeIDs {
			change, exists := available[changeID]
			if !exists {
				writeDetailError(w, errCodeChangeMissing, http.StatusNotFound)
				return
			}
			changes = append(changes, change)
		}
	} else {
		record, err := remoteManager.ReadFullSession(identity.SourceID, identity.Agent, identity.SessionID, identity.Revision)
		if err != nil {
			writeRemoteDetailError(w, err)
			return
		}
		if record == nil {
			writeDetailError(w, errCodeDetailMissing, http.StatusNotFound)
			return
		}
		available := remoteChangesByID(*record)
		for _, changeID := range changeIDs {
			change, exists := available[changeID]
			if !exists {
				writeDetailError(w, errCodeChangeMissing, http.StatusNotFound)
				return
			}
			changes = append(changes, change)
		}
	}

	writeJSON(w, struct {
		Changes []session.FileChange `json:"changes"`
	}{Changes: changes})
}

func parseExactSessionIdentity(w http.ResponseWriter, r *http.Request) (exactSessionIdentity, bool) {
	query := r.URL.Query()
	sourceID, err := parseSourceID(query.Get("source"))
	if err != nil {
		http.Error(w, "invalid source", http.StatusBadRequest)
		return exactSessionIdentity{}, false
	}
	identity := exactSessionIdentity{
		SourceID: sourceID, Agent: query.Get("agent"), SessionID: query.Get("session"), Revision: query.Get("revision"),
	}
	if !validAgent(identity.Agent) || !validOpaqueIdentifier(identity.SessionID) || !validOpaqueIdentifier(identity.Revision) {
		http.Error(w, "invalid session identity", http.StatusBadRequest)
		return exactSessionIdentity{}, false
	}
	return identity, true
}

func validAgent(agent string) bool {
	return agent == vendors.AgentClaude || agent == vendors.AgentCodex || agent == vendors.AgentOpenCode
}

func validOpaqueIdentifier(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 512 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func withLocalChangeIDs(value session.Session) session.Session {
	value.FileEdits = append([]session.FileEdit(nil), value.FileEdits...)
	for editIndex := range value.FileEdits {
		changes := value.FileEdits[editIndex].Changes()
		value.FileEdits[editIndex].ChangeIDs = make([]string, len(changes))
		for changeIndex := range changes {
			value.FileEdits[editIndex].ChangeIDs[changeIndex] = fmt.Sprintf("change-%06d-%06d", editIndex, changeIndex)
		}
	}
	return value
}

func localChangesByID(value session.Session) map[string]session.FileChange {
	changes := map[string]session.FileChange{}
	for _, edit := range value.FileEdits {
		for index, change := range edit.Changes() {
			if index < len(edit.ChangeIDs) {
				changes[edit.ChangeIDs[index]] = change
			}
		}
	}
	return changes
}

func remoteChangesByID(record fullsessionv1.Record) map[string]session.FileChange {
	changes := map[string]session.FileChange{}
	for _, edit := range record.Session.FileEdits {
		for _, change := range edit.Changes {
			changes[change.ID] = session.FileChange{
				Kind: change.Kind, Text: change.Text, Operation: change.Operation,
				Additions: change.Additions, Deletions: change.Deletions,
			}
		}
	}
	return changes
}

func writeRemoteDetailError(w http.ResponseWriter, err error) {
	if errors.Is(err, remote.ErrRemoteRevisionNotFound) {
		writeDetailError(w, errCodeDetailStale, http.StatusConflict)
		return
	}
	if errors.Is(err, remote.ErrRemoteRecordCorrupt) {
		writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
		return
	}
	writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
}

func writeDetailError(w http.ResponseWriter, code string, status int) {
	message := map[string]string{
		errCodeDetailStale:   "the selected session revision changed; refresh sessions and try again",
		errCodeDetailMissing: "complete session details are unavailable",
		errCodeDetailCorrupt: "complete session details could not be read",
		errCodeChangeMissing: "the selected change does not belong to this session revision",
	}[code]
	writeAPIError(w, status, code, message)
}
