package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const (
	localSourceID    = "local"
	localSourceLabel = "This Mac"

	errCodeRemoteUnsupported   = "remote_action_unsupported"
	errCodeRemoteNotConfigured = "remote_not_configured"
	errCodeRemoteDisabled      = "remote_disabled"
)

type apiErrorBody struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

type sessionsResponse struct {
	Sessions []boardSession `json:"sessions"`
	Machines []machineFact  `json:"machines"`
}

type boardSession struct {
	SourceID              string  `json:"sourceId"`
	SourceLabel           string  `json:"sourceLabel"`
	EligibleForAggregates bool    `json:"eligibleForAggregates"`
	DisplayStale          bool    `json:"displayStale"`
	LastSeenStatus        *string `json:"lastSeenStatus,omitempty"`
	session.Session
}

type machineFact struct {
	SourceID        string                 `json:"sourceId"`
	Label           string                 `json:"label"`
	State           remote.State           `json:"state"`
	Complete        bool                   `json:"complete"`
	Reason          *remote.Reason         `json:"reason,omitempty"`
	LastSuccessAtMs *int64                 `json:"lastSuccessAtMs,omitempty"`
	CoverageSinceMs *int64                 `json:"coverageSinceMs,omitempty"`
	RoundTripMs     *int64                 `json:"roundTripMs,omitempty"`
	Coverage        []remote.AgentCoverage `json:"coverage,omitempty"`
	Error           string                 `json:"error,omitempty"`
}

func localMachineFact() machineFact {
	return machineFact{SourceID: localSourceID, Label: localSourceLabel, State: remote.StateOK, Complete: true}
}

func machineFromHealth(health remote.Health) machineFact {
	return machineFact{
		SourceID: health.SourceID, Label: health.Label, State: health.State,
		Complete: health.Complete, Reason: health.Reason,
		LastSuccessAtMs: health.LastSuccessAtMs, CoverageSinceMs: health.CoverageSinceMs,
		RoundTripMs: health.RoundTripMs, Coverage: health.Coverage, Error: health.Error,
	}
}

func boardLocalSession(value *session.Session) boardSession {
	return boardSession{
		SourceID: localSourceID, SourceLabel: localSourceLabel,
		EligibleForAggregates: true, Session: *value,
	}
}

func boardRemoteSession(value remote.IndexedSession) boardSession {
	return boardSession{
		SourceID: value.Key.SourceID, SourceLabel: value.SourceLabel,
		EligibleForAggregates: value.EligibleForAggregates,
		DisplayStale:          value.DisplayStale, LastSeenStatus: value.LastSeenStatus,
		Session: *value.Session,
	}
}

func parseSourceID(value string) (string, error) {
	if value == "" || value == localSourceID {
		return localSourceID, nil
	}
	if !settings.ValidRemoteID(value) {
		return "", fmt.Errorf("invalid source")
	}
	return value, nil
}

func rejectRemoteSource(w http.ResponseWriter, r *http.Request) bool {
	source, err := parseSourceID(r.URL.Query().Get("source"))
	if err != nil {
		issueAPI("remote_action", "invalid_source", http.StatusBadRequest)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	if source != localSourceID {
		issueAPI("remote_action", errCodeRemoteUnsupported, http.StatusConflict)
		writeAPIError(w, http.StatusConflict, errCodeRemoteUnsupported, "remote action unsupported")
		return true
	}
	return false
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiErrorBody{Code: code, Error: message})
}
