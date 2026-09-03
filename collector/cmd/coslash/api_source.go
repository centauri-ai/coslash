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

	errCodeRemoteUnsupported    = "remote_action_unsupported"
	errCodeRemoteNotConfigured  = "remote_not_configured"
	errCodeRemoteDisabled       = "remote_disabled"
	errCodeRemoteRetryThrottled = "remote_retry_throttled"
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
	Launchable            bool    `json:"launchable"`
	session.Session
}

type machineFact struct {
	SourceID                    string                   `json:"sourceId"`
	Label                       string                   `json:"label"`
	State                       remote.State             `json:"state"`
	Complete                    bool                     `json:"complete"`
	Reason                      *remote.Reason           `json:"reason,omitempty"`
	LastSuccessAtMs             *int64                   `json:"lastSuccessAtMs,omitempty"`
	LastCheckedAtMs             *int64                   `json:"lastCheckedAtMs,omitempty"`
	SessionCount                int                      `json:"sessionCount"`
	CoverageSinceMs             *int64                   `json:"coverageSinceMs,omitempty"`
	RoundTripMs                 *int64                   `json:"roundTripMs,omitempty"`
	Coverage                    []remote.AgentCoverage   `json:"coverage,omitempty"`
	Error                       string                   `json:"error,omitempty"`
	Transport                   remote.Transport         `json:"transport,omitempty"`
	Helper                      *remote.HelperStatus     `json:"helper,omitempty"`
	Metrics                     remote.CollectionMetrics `json:"metrics"`
	HelperInstallationAvailable bool                     `json:"helperInstallationAvailable"`
	HelperProbeState            string                   `json:"helperProbeState,omitempty"`
	HelperOwnershipRecorded     bool                     `json:"helperOwnershipRecorded"`
	HelperOwnershipCorrupt      bool                     `json:"helperOwnershipCorrupt"`
	Refreshing                  bool                     `json:"refreshing,omitempty"`
}

func localMachineFact() machineFact {
	return machineFact{SourceID: localSourceID, Label: localSourceLabel, State: remote.StateOK, Complete: true}
}

func machineFromHealth(health remote.Health) machineFact {
	return machineFact{
		SourceID: health.SourceID, Label: health.Label, State: health.State,
		Complete: health.Complete, Reason: health.Reason,
		LastSuccessAtMs: health.LastSuccessAtMs, LastCheckedAtMs: health.LastCheckedAtMs,
		SessionCount: health.SessionCount, CoverageSinceMs: health.CoverageSinceMs,
		RoundTripMs: health.RoundTripMs, Coverage: health.Coverage, Error: health.Error,
		Transport: health.Transport, Helper: health.Helper, Metrics: health.Metrics,
		HelperInstallationAvailable: health.HelperInstallationAvailable,
		HelperProbeState:            health.HelperProbeState,
		HelperOwnershipRecorded:     health.HelperOwnershipRecorded,
		HelperOwnershipCorrupt:      health.HelperOwnershipCorrupt,
		Refreshing:                  health.Refreshing,
	}
}

func boardLocalSession(value *session.Session) boardSession {
	return boardSession{
		SourceID: localSourceID, SourceLabel: localSourceLabel,
		EligibleForAggregates: true, Session: sessionWithJSONCollections(*value),
	}
}

func boardRemoteSession(value remote.IndexedSession) boardSession {
	return boardSession{
		SourceID: value.Key.SourceID, SourceLabel: value.SourceLabel,
		EligibleForAggregates: value.EligibleForAggregates,
		DisplayStale:          value.DisplayStale, LastSeenStatus: value.LastSeenStatus,
		Launchable: value.Launchable,
		Session:    sessionWithJSONCollections(*value.Session),
	}
}

// sessionWithJSONCollections keeps the API's array/object contract stable for
// sparse normalized remote facts. Go encodes nil slices as null, but the board
// renders these fields as collections and must receive [] rather than null.
func sessionWithJSONCollections(value session.Session) session.Session {
	if value.Tokens == nil {
		value.Tokens = map[string]session.ModelTokens{}
	}
	if value.UnpricedModels == nil {
		value.UnpricedModels = []string{}
	}
	if value.Subagents == nil {
		value.Subagents = []session.Subagent{}
	}
	for index := range value.Subagents {
		if value.Subagents[index].Commands == nil {
			value.Subagents[index].Commands = []session.SubagentCommand{}
		}
		if value.Subagents[index].Tokens == nil {
			value.Subagents[index].Tokens = map[string]session.ModelTokens{}
		}
	}
	if value.Commands == nil {
		value.Commands = []string{}
	}
	if value.Commits == nil {
		value.Commits = []string{}
	}
	if value.Todos == nil {
		value.Todos = []session.Todo{}
	}
	if value.Digest == nil {
		value.Digest = []session.DigestEntry{}
	}
	if value.FileEdits == nil {
		value.FileEdits = []session.FileEdit{}
	}
	if value.Synthesis != nil {
		synthesis := *value.Synthesis
		if synthesis.Goals == nil {
			synthesis.Goals = []string{}
		}
		if synthesis.KeyDecisions == nil {
			synthesis.KeyDecisions = []string{}
		}
		value.Synthesis = &synthesis
	}
	return value
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	if source != localSourceID {
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
