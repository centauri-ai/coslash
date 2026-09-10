package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const (
	localSourceID    = "local"
	localSourceLabel = "This Mac"
	// Remote aliases are configuration input and may contain a hostname or a
	// username. They are intentionally never part of the source-aware web
	// model. The opaque source ID remains available for stable selection and
	// routing, while this fixed label is safe to display.
	sshSourceLabel = "SSH workspace"

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
	SourceID              string                   `json:"sourceId"`
	SourceLabel           string                   `json:"sourceLabel"`
	SourceClass           string                   `json:"sourceClass"`
	LogicalSessionID      string                   `json:"logicalSessionId"`
	Revision              int64                    `json:"revision"`
	Completion            string                   `json:"completion"`
	Privacy               string                   `json:"privacy"`
	ShareEligibility      string                   `json:"shareEligibility"`
	EligibleForAggregates bool                     `json:"eligibleForAggregates"`
	DisplayStale          bool                     `json:"displayStale"`
	LastSeenStatus        *string                  `json:"lastSeenStatus,omitempty"`
	Launchable            bool                     `json:"launchable"`
	LaunchBlockReason     remote.LaunchBlockReason `json:"launchBlockReason,omitempty"`
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
		SourceID: health.SourceID, Label: safeSourceLabel(health.SourceID), State: health.State,
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
		SourceClass: "local", LogicalSessionID: logicalSessionID(localSourceID, value),
		Revision: value.LastActivityTime, Completion: completionFor(value, true),
		Privacy: privacyFor(value), ShareEligibility: eligibilityFor(value, true, false),
		EligibleForAggregates: true, Session: sessionWithJSONCollections(*value),
	}
}

func boardRemoteSession(value remote.IndexedSession) boardSession {
	safeSession := sessionWithJSONCollections(remoteLibrarySession(*value.Session))
	return boardSession{
		SourceID: value.Key.SourceID, SourceLabel: sshSourceLabel,
		SourceClass: "ssh", LogicalSessionID: logicalSessionID(value.Key.SourceID, value.Session),
		Revision:              value.Session.LastActivityTime,
		Completion:            completionFor(value.Session, value.EligibleForAggregates && !value.DisplayStale),
		Privacy:               privacyFor(value.Session),
		ShareEligibility:      eligibilityFor(value.Session, value.EligibleForAggregates, value.DisplayStale),
		EligibleForAggregates: value.EligibleForAggregates,
		DisplayStale:          value.DisplayStale, LastSeenStatus: value.LastSeenStatus,
		Launchable: value.Launchable, LaunchBlockReason: value.LaunchBlockReason,
		Session: safeSession,
	}
}

// remoteLibrarySession is the explicit browser boundary for SSH collection.
// Remote facts can contain enough local-only material to resume collection or
// launch an agent, but none of that makes a safe library card. Keep only the
// bounded display and numeric fields needed for discovery; details continue to
// belong to the remote helper/launch paths rather than the web list model.
func remoteLibrarySession(value session.Session) session.Session {
	repository := safeRepository(value.Repository)
	return session.Session{
		Agent: value.Agent, ID: value.ID, Name: value.Name, Status: value.Status,
		Branch: value.Branch, Repository: repository, RepositoryLocalOnly: value.RepositoryLocalOnly,
		EditedFileCount: value.EditedFileCount, DurationMs: value.DurationMs,
		Tokens: value.Tokens, Cost: value.Cost, UnpricedModels: value.UnpricedModels,
		StartedAt: value.StartedAt, LastActivityTime: value.LastActivityTime, Entrypoint: value.Entrypoint,
		SessionDetails: session.SessionDetails{
			Model: value.Model, ContextTokens: value.ContextTokens, ContextWindow: value.ContextWindow,
			Turns: value.Turns, ToolUses: value.ToolUses, Errors: value.Errors,
			Compactions: value.Compactions, PullRequests: value.PullRequests,
		},
	}
}

// A repository name is useful discovery metadata only when it is a relative,
// normalized identifier. Reject filesystem paths, URLs, shell-looking text,
// and whitespace rather than allowing a remote implementation detail into a
// browser response. The LB-00 canonical identity contract may later narrow
// this further without changing the response shape.
func safeRepository(value *string) *string {
	if value == nil {
		return nil
	}
	name := strings.TrimSpace(*value)
	if name == "" || len(name) > 280 || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~") ||
		strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return nil
	}
	for _, r := range name {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._/-", r)) {
			return nil
		}
	}
	return &name
}

func safeSourceLabel(sourceID string) string {
	if sourceID == localSourceID {
		return localSourceLabel
	}
	return sshSourceLabel
}

// logicalSessionID is deliberately based on the opaque persisted source ID,
// vendor, and vendor session ID. In particular it does not depend on an SSH
// alias, path, hostname, or username, so renaming a configured remote does
// not change a list row's identity.
func logicalSessionID(sourceID string, value *session.Session) string {
	return sourceID + ":" + value.Agent + ":" + value.ID
}

func completionFor(value *session.Session, sourceComplete bool) string {
	if !sourceComplete {
		return "incomplete"
	}
	if value.Status != nil {
		return "running"
	}
	return "complete"
}

func privacyFor(value *session.Session) string {
	if value.RepositoryLocalOnly {
		return "private"
	}
	return "shareable"
}

// Eligibility is intentionally stricter than aggregate eligibility. A
// current review may only start from a completed, non-private session whose
// source is healthy; later share work consumes this display-only signal but
// must still perform its own snapshot and consent checks.
func eligibilityFor(value *session.Session, sourceComplete, stale bool) string {
	if value.RepositoryLocalOnly {
		return "private"
	}
	if stale {
		return "stale"
	}
	if !sourceComplete {
		return "incomplete"
	}
	if value.Status != nil {
		return "running"
	}
	return "eligible"
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
