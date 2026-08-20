package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

const (
	localSourceID    = "local"
	localSourceLabel = "This Mac"

	errCodeRemoteUnsupported      = "remote_action_unsupported"
	errCodeRemoteNotConfigured    = "remote_not_configured"
	errCodeRemoteDisabled         = "remote_disabled"
	errCodeRemoteUpgradeRequired  = "remote_upgrade_required"
	errCodeRemoteAgentUnavailable = "remote_agent_unavailable"
)

// Keep settings options aligned with the diagnostics/setup guide constant.
var remoteInstallationGuidePath = diagnostics.RemoteInstallationGuidePath

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
	SourceID         string         `json:"sourceId"`
	Label            string         `json:"label"`
	State            remote.State   `json:"state"`
	Complete         bool           `json:"complete"`
	Reason           *remote.Reason `json:"reason,omitempty"`
	CollectorVersion string         `json:"collectorVersion,omitempty"`
	SchemaVersion    string         `json:"schemaVersion,omitempty"`
	Capabilities     []string       `json:"capabilities,omitempty"`
	LaunchableAgents []string       `json:"launchableAgents,omitempty"`
	HostOS           string         `json:"hostOs,omitempty"`
	HostArch         string         `json:"hostArch,omitempty"`
	LastSuccessAtMs  *int64         `json:"lastSuccessAtMs,omitempty"`
	CoverageSinceMs  *int64         `json:"coverageSinceMs,omitempty"`
	ClockOffsetMs    *int64         `json:"clockOffsetMs,omitempty"`
	RoundTripMs      *int64         `json:"roundTripMs,omitempty"`
	Error            string         `json:"error,omitempty"`
}

func localMachineFact() machineFact {
	return machineFact{
		SourceID: localSourceID,
		Label:    localSourceLabel,
		State:    remote.StateOK,
		Complete: true,
	}
}

func machineFromHealth(health remote.Health) machineFact {
	return machineFact{
		SourceID:         health.SourceID,
		Label:            health.Label,
		State:            health.State,
		Complete:         health.Complete,
		Reason:           health.Reason,
		CollectorVersion: health.CollectorVersion,
		SchemaVersion:    health.SchemaVersion,
		Capabilities:     health.Capabilities,
		LaunchableAgents: health.LaunchableAgents,
		HostOS:           health.HostOS,
		HostArch:         health.HostArch,
		LastSuccessAtMs:  health.LastSuccessAtMs,
		CoverageSinceMs:  health.CoverageSinceMs,
		ClockOffsetMs:    health.ClockOffsetMs,
		RoundTripMs:      health.RoundTripMs,
		Error:            health.Error,
	}
}

func boardLocalSession(s *session.Session) boardSession {
	return boardSession{
		SourceID:              localSourceID,
		SourceLabel:           localSourceLabel,
		EligibleForAggregates: true,
		DisplayStale:          false,
		Session:               *s,
	}
}

func boardRemoteSession(item remote.IndexedSession) boardSession {
	mapped := mapRemoteViewSession(item.Session)
	return boardSession{
		SourceID:              item.Key.SourceID,
		SourceLabel:           item.SourceLabel,
		EligibleForAggregates: item.EligibleForAggregates,
		DisplayStale:          item.DisplayStale,
		LastSeenStatus:        item.LastSeenStatus,
		Session:               mapped,
	}
}

func mapRemoteViewSession(in remoteviewv1.Session) session.Session {
	out := session.Session{
		Agent:               in.Agent,
		ID:                  in.SourceSessionID,
		Name:                in.Name,
		Summary:             in.Summary,
		Status:              in.Status,
		RepositoryLocalOnly: in.RepositoryLocalOnly,
		EditedFileCount:     in.Counts.EditedFiles,
		DurationMs:          in.DurationMs,
		Cost:                float64(in.Usage.EstimatedCostMicroUSD) / 1_000_000,
		UnpricedModels:      append([]string(nil), in.Usage.UnpricedModels...),
		StartedAt:           in.SessionStartedAtMs,
		LastActivityTime:    in.LastActivityAtMs,
		Entrypoint:          in.Entrypoint,
		SessionDetails: session.SessionDetails{
			Model:         in.Model,
			ContextTokens: in.ContextTokens,
			ContextWindow: in.ContextWindow,
			Turns:         in.Counts.Turns,
			ToolUses:      in.Counts.ToolUses,
			Errors:        in.Counts.Errors,
			Compactions:   in.Counts.Compactions,
			FirstPrompt:   in.FirstPrompt,
			Commands:      []string{},
			Commits:       append([]string(nil), in.Commits...),
			PullRequests:  in.Counts.PullRequests,
			DeclaredGoal:  in.DeclaredGoal,
			LastEditAt:    in.LastEditAtMs,
			Synthesis:     nil,
		},
	}
	if in.WorkingDirectory != nil {
		out.WorkingDirectory = *in.WorkingDirectory
	}
	if in.Repository != nil {
		out.Repository = in.Repository
	}
	if in.Branch != nil {
		out.Branch = in.Branch
	}
	out.Tokens = map[string]session.ModelTokens{}
	for _, usage := range in.Usage.Models {
		out.Tokens[usage.Model] = session.ModelTokens{
			InputTokens:                usage.InputTokens,
			OutputTokens:               usage.OutputTokens,
			CacheCreationInputTokens:   usage.CacheCreationInputTokens,
			CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens,
			CacheReadInputTokens:       usage.CacheReadInputTokens,
			Cost:                       float64(usage.EstimatedCostMicroUSD) / 1_000_000,
		}
	}
	out.Todos = make([]session.Todo, 0, len(in.Todos))
	for _, todo := range in.Todos {
		out.Todos = append(out.Todos, session.Todo{Text: todo.Text, Done: todo.Done})
	}
	out.Digest = make([]session.DigestEntry, 0, len(in.Digest))
	for _, entry := range in.Digest {
		mapped := session.DigestEntry{
			Turn:        entry.Turn,
			Category:    entry.Category,
			Description: entry.Description,
		}
		if entry.Answer != nil {
			mapped.Answer = *entry.Answer
		}
		if entry.SubagentID != nil {
			mapped.SubagentID = *entry.SubagentID
		}
		out.Digest = append(out.Digest, mapped)
	}
	out.FileEdits = make([]session.FileEdit, 0, len(in.FileEdits))
	for _, edit := range in.FileEdits {
		out.FileEdits = append(out.FileEdits, session.FileEdit{
			Path:      edit.Path,
			Additions: edit.Additions,
			Deletions: edit.Deletions,
			Edits:     edit.Edits,
			IsNew:     edit.IsNew,
		})
	}
	if in.Git != nil {
		out.Git = &session.GitDrift{
			BaseBranch: in.Git.BaseBranch,
			Ahead:      in.Git.Ahead,
			Behind:     in.Git.Behind,
		}
	}
	out.Subagents = make([]session.Subagent, 0, len(in.Subagents))
	for _, sub := range in.Subagents {
		mapped := session.Subagent{
			ID:            sub.ID,
			Name:          sub.Name,
			Model:         sub.Model,
			Status:        sub.Status,
			Task:          sub.Task,
			Result:        sub.Result,
			DurationMs:    sub.DurationMs,
			SpawnedAtTurn: sub.SpawnedAtTurn,
			ToolUses:      sub.ToolUses,
			Cost:          float64(sub.EstimatedCostMicroUSD) / 1_000_000,
			Tokens:        map[string]session.ModelTokens{},
		}
		for _, label := range sub.CommandLabels {
			mapped.Commands = append(mapped.Commands, session.SubagentCommand{Label: label})
		}
		for _, usage := range sub.Usage {
			mapped.Tokens[usage.Model] = session.ModelTokens{
				InputTokens:                usage.InputTokens,
				OutputTokens:               usage.OutputTokens,
				CacheCreationInputTokens:   usage.CacheCreationInputTokens,
				CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens,
				CacheReadInputTokens:       usage.CacheReadInputTokens,
				Cost:                       float64(usage.EstimatedCostMicroUSD) / 1_000_000,
			}
		}
		out.Subagents = append(out.Subagents, mapped)
	}
	return out
}

func parseSourceID(value string) (string, error) {
	if value == "" {
		return localSourceID, nil
	}
	if value == localSourceID {
		return localSourceID, nil
	}
	if !strings.HasPrefix(value, "r_") {
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
