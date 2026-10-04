package main

import (
	"net/http"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

type shareSynthesisRequest struct {
	SourceID         string `json:"sourceId"`
	Agent            string `json:"agent"`
	SessionID        string `json:"sessionId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

type shareSynthesisStatus struct {
	State       string                    `json:"state"`
	Revision    int64                     `json:"revision"`
	Backend     string                    `json:"backend,omitempty"`
	Model       string                    `json:"model,omitempty"`
	GeneratedAt int64                     `json:"generatedAt,omitempty"`
	Synthesis   *session.SessionSynthesis `json:"synthesis,omitempty"`
}

func handleShareSynthesis(w http.ResponseWriter, r *http.Request, mgr *synthesis.Manager, store *settings.Store) {
	var request shareSynthesisRequest
	if err := decodeHubJSON(r.Body, &request); err != nil || request.SourceID != localSourceID ||
		request.Agent == "" || request.SessionID == "" || request.ExpectedRevision <= 0 {
		http.Error(w, "invalid share synthesis request", http.StatusBadRequest)
		return
	}
	found, err := collector.GetSessionFactsByAgent(request.Agent, request.SessionID)
	if err != nil {
		writeJSON(w, shareSynthesisStatus{State: "unavailable"})
		return
	}
	if found == nil {
		writeJSON(w, shareSynthesisStatus{State: "missing"})
		return
	}
	writeJSON(w, shareSynthesisReadiness(found, request.ExpectedRevision, mgr, store.State()))
}

func shareSynthesisReadiness(found *session.Session, expectedRevision int64, mgr *synthesis.Manager, state settings.State) shareSynthesisStatus {
	revision := found.LastActivityTime
	status := shareSynthesisStatus{Revision: revision, Backend: state.Config.Synthesis.Backend, Model: state.Config.Synthesis.Model}
	if revision != expectedRevision {
		status.State = "revision_changed"
		return status
	}
	if mgr != nil && mgr.Lookup(found.Agent, found.ID, revision) != nil {
		record, err := mgr.LoadRecord(found.Agent, found.ID)
		if err == nil && record.Revision == revision {
			status.State = "ready"
			status.Model = record.Model
			status.GeneratedAt = record.GeneratedAt
			status.Synthesis = &record.Synthesis
			return status
		}
		status.State = "failed"
		return status
	}
	if !synthesis.Eligible(found) {
		status.State = "ineligible"
	} else if !state.Valid || !state.Persisted {
		status.State = "consent_required"
	} else if !state.Config.Synthesis.Enabled {
		status.State = "disabled"
	} else if mgr.BackendUnavailable() {
		status.State = "unavailable"
	} else if mgr.Failure(found.Agent, found.ID, revision) != "" {
		status.State = "failed"
	} else if mgr.Running(found.Agent, found.ID) {
		status.State = "pending"
	} else if mgr.InCooldown(found.Agent, found.ID, revision) {
		status.State = "unavailable"
	} else {
		mgr.Ensure(found, revision)
		status.State = "pending"
	}
	return status
}
