package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const (
	errCodeDetailStale     = "session_detail_stale"
	errCodeDetailMissing   = "session_detail_missing"
	errCodeDetailCorrupt   = "session_detail_corrupt"
	errCodeChangeMissing   = "session_change_missing"
	errCodeChangeDuplicate = "session_change_duplicate"
	errCodeDiffTooLarge    = "session_diff_too_large"

	maxExactDiffChanges   = 256
	maxExactDiffIDBytes   = 16 << 10
	maxExactDiffTextBytes = 8 << 20
	maxExactDiffBytes     = fullsessionv1.MaxRecordBytes
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

type exactDiffResponse struct {
	Changes []session.FileChange `json:"changes"`
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
			logExactReadError("read local session detail", identity, err)
			writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
			return
		}
		if found == nil {
			writeDetailError(w, errCodeDetailMissing, http.StatusNotFound)
			return
		}
		revision, err := localDetailRevision(*found)
		if err != nil {
			logExactReadError("fingerprint local session detail", identity, err)
			writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
			return
		}
		if identity.Revision != revision {
			writeDetailError(w, errCodeDetailStale, http.StatusConflict)
			return
		}
		copy := withLocalChangeIDs(*found)
		value = &copy
	} else {
		record, err := remoteManager.ReadFullSession(identity.SourceID, identity.Agent, identity.SessionID, identity.Revision)
		if err != nil {
			if !errors.Is(err, remote.ErrRemoteRevisionNotFound) {
				logExactReadError("read remote session detail", identity, err)
			}
			writeRemoteDetailError(w, err)
			return
		}
		if record == nil {
			writeDetailError(w, errCodeDetailMissing, http.StatusNotFound)
			return
		}
		value, err = fullsessionrecord.ToSession(*record)
		if err != nil {
			logExactReadError("decode remote session detail", identity, err)
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
	if len(changeIDs) > maxExactDiffChanges {
		writeDetailError(w, errCodeDiffTooLarge, http.StatusRequestEntityTooLarge)
		return
	}
	seen := make(map[string]struct{}, len(changeIDs))
	totalIDBytes := 0
	for _, changeID := range changeIDs {
		if !validOpaqueIdentifier(changeID) {
			http.Error(w, "invalid change", http.StatusBadRequest)
			return
		}
		if _, duplicate := seen[changeID]; duplicate {
			writeDetailError(w, errCodeChangeDuplicate, http.StatusBadRequest)
			return
		}
		totalIDBytes += len(changeID)
		if totalIDBytes > maxExactDiffIDBytes {
			writeDetailError(w, errCodeDiffTooLarge, http.StatusRequestEntityTooLarge)
			return
		}
		seen[changeID] = struct{}{}
	}

	changes := make([]session.FileChange, 0, len(changeIDs))
	if identity.SourceID == localSourceID {
		found, err := getLocal(identity.Agent, identity.SessionID)
		if err != nil {
			logExactReadError("read local session diff", identity, err)
			writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
			return
		}
		if found == nil {
			writeDetailError(w, errCodeDetailMissing, http.StatusNotFound)
			return
		}
		revision, err := localDetailRevision(*found)
		if err != nil {
			logExactReadError("fingerprint local session diff", identity, err)
			writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
			return
		}
		if identity.Revision != revision {
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
			if !errors.Is(err, remote.ErrRemoteRevisionNotFound) {
				logExactReadError("read remote session diff", identity, err)
			}
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

	writeExactDiffResponse(w, changes, maxExactDiffTextBytes, maxExactDiffBytes)
}

func writeExactDiffResponse(w http.ResponseWriter, changes []session.FileChange, maxTextBytes, maxBytes int) {
	totalTextBytes := 0
	for _, change := range changes {
		if len(change.Text) > maxTextBytes-totalTextBytes {
			writeDetailError(w, errCodeDiffTooLarge, http.StatusRequestEntityTooLarge)
			return
		}
		totalTextBytes += len(change.Text)
	}
	body, err := json.Marshal(exactDiffResponse{Changes: changes})
	if err != nil {
		writeDetailError(w, errCodeDetailCorrupt, http.StatusInternalServerError)
		return
	}
	if len(body) > maxBytes {
		writeDetailError(w, errCodeDiffTooLarge, http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
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
	occurrences := map[string]int{}
	for editIndex := range value.FileEdits {
		changes := value.FileEdits[editIndex].Changes()
		value.FileEdits[editIndex].ChangeIDs = make([]string, len(changes))
		for changeIndex, change := range changes {
			identity, _ := json.Marshal(struct {
				Path      string `json:"path"`
				Kind      string `json:"kind"`
				Text      string `json:"text"`
				Operation string `json:"operation"`
				Additions int    `json:"additions"`
				Deletions int    `json:"deletions"`
			}{
				Path: value.FileEdits[editIndex].Path, Kind: change.Kind, Text: change.Text,
				Operation: change.Operation, Additions: change.Additions, Deletions: change.Deletions,
			})
			digest := sha256.Sum256(identity)
			base := "change-" + base64.RawURLEncoding.EncodeToString(digest[:])
			occurrence := occurrences[base]
			occurrences[base]++
			if occurrence > 0 {
				base = fmt.Sprintf("%s-%06d", base, occurrence)
			}
			value.FileEdits[editIndex].ChangeIDs[changeIndex] = base
		}
	}
	return value
}

// localDetailRevision fingerprints parsed content while excluding fields
// refreshed from process, repository, and synthesis state. Those live facts
// are overlaid from the session list in the inspector and must not make the
// exact transcript/diff identity depend on which environment probes a reader
// performs.
func localDetailRevision(value session.Session) (string, error) {
	stable := session.Clone(&value)
	stable.Status = nil
	stable.Branch = nil
	stable.Repository = nil
	stable.RepositoryLocalOnly = false
	stable.Commits = nil
	stable.CommitSHAs = nil
	stable.Git = nil
	stable.GitProbed = false
	stable.LastEditAt = nil
	stable.ReviewPending = false
	stable.ReviewError = ""
	stable.Synthesis = nil
	stable.SynthesisPending = false
	for index := range stable.Subagents {
		stable.Subagents[index].Status = ""
	}

	payload, err := json.Marshal(struct {
		Session   session.Session             `json:"session"`
		CommitLog []session.CommitObservation `json:"commitLog"`
	}{
		Session:   sessionWithJSONCollections(withLocalChangeIDs(*stable)),
		CommitLog: stable.CommitLog,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
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

func logExactReadError(operation string, identity exactSessionIdentity, err error) {
	log.Printf("%s source=%q agent=%q session=%q revision=%q: %v",
		operation, identity.SourceID, identity.Agent, identity.SessionID, identity.Revision, err)
}

func writeDetailError(w http.ResponseWriter, code string, status int) {
	message := map[string]string{
		errCodeDetailStale:     "the selected session revision changed; refresh sessions and try again",
		errCodeDetailMissing:   "complete session details are unavailable",
		errCodeDetailCorrupt:   "complete session details could not be read",
		errCodeChangeMissing:   "the selected change does not belong to this session revision",
		errCodeChangeDuplicate: "duplicate change IDs are not allowed",
		errCodeDiffTooLarge:    "the requested file changes exceed the exact-diff response limit",
	}[code]
	writeAPIError(w, status, code, message)
}
