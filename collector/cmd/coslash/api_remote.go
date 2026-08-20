package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

type remoteTestRequest struct {
	SSHAlias string `json:"sshAlias"`
}

func handleRemoteTest(w http.ResponseWriter, r *http.Request, mgr *remote.Manager) {
	var body remoteTestRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid remote test request", http.StatusBadRequest)
		return
	}
	if !settings.ValidSSHAlias(body.SSHAlias) {
		http.Error(w, "invalid sshAlias", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), remote.SnapshotDeadline)
	defer cancel()
	health, err := mgr.TestAlias(ctx, body.SSHAlias)
	if err != nil {
		http.Error(w, "invalid sshAlias", http.StatusBadRequest)
		return
	}
	writeJSON(w, machineFromHealth(health))
}

func handleRemoteRetry(w http.ResponseWriter, _ *http.Request, mgr *remote.Manager) {
	health := mgr.DiagnosticsHealth()
	if health.SourceID == "" {
		writeAPIError(w, http.StatusNotFound, errCodeRemoteNotConfigured, "remote not configured")
		return
	}
	if health.State == remote.StateDisabled {
		writeAPIError(w, http.StatusConflict, errCodeRemoteDisabled, "remote disabled")
		return
	}
	health = mgr.Retry()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(machineFromHealth(health))
}

func handleLaunchSourceAware(
	w http.ResponseWriter,
	r *http.Request,
	settingsStore *settings.Store,
	remoteMgr *remote.Manager,
) {
	state := settingsStore.State()
	if !state.Valid {
		http.Error(w, state.Error+"; open Settings to repair it", http.StatusConflict)
		return
	}
	query := r.URL.Query()
	source, err := parseSourceID(query.Get("source"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mode := query.Get("mode")
	id := query.Get("id")
	agent := query.Get("agent")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	if source == localSourceID {
		handleLaunchLocal(w, r, state, id, agent, mode)
		return
	}
	handleLaunchRemote(w, r, state, remoteMgr, source, agent, id, mode)
}

func handleLaunchLocal(
	w http.ResponseWriter,
	r *http.Request,
	state settings.State,
	id, agent, mode string,
) {
	found, err := collector.GetSessionFacts(id)
	if err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, "could not load session", http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if agent != "" && agent != found.Agent {
		http.Error(w, "agent does not match session", http.StatusBadRequest)
		return
	}
	handoff, err := readHandoff(w, r)
	if err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := launch.Terminal(state.Config.Launch.Terminal, found.Agent, found.WorkingDirectory, found.ID, mode, handoff); err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, "could not launch terminal", http.StatusInternalServerError)
		return
	}
	log.Printf("launch %s: %s %s", mode, found.Agent, found.ID)
	w.WriteHeader(http.StatusNoContent)
}

func handleLaunchRemote(
	w http.ResponseWriter,
	r *http.Request,
	state settings.State,
	remoteMgr *remote.Manager,
	source, agent, id, mode string,
) {
	if agent == "" {
		http.Error(w, "missing agent", http.StatusBadRequest)
		return
	}
	if err := launch.ValidateRemoteAgent(agent); err != nil {
		http.Error(w, "invalid agent", http.StatusBadRequest)
		return
	}
	if err := launch.ValidateUUIDSessionID(id); err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	if mode != "resume" && mode != "new" {
		http.Error(w, "invalid mode", http.StatusBadRequest)
		return
	}

	health := remoteMgr.DiagnosticsHealth()
	if health.SourceID == "" || health.SourceID != source {
		writeAPIError(w, http.StatusNotFound, errCodeRemoteNotConfigured, "remote not configured")
		return
	}
	if health.State == remote.StateDisabled {
		writeAPIError(w, http.StatusConflict, errCodeRemoteDisabled, "remote disabled")
		return
	}
	if !slices.Contains(health.Capabilities, remoteviewv1.CapabilityRemoteLaunch) {
		writeAPIError(w, http.StatusConflict, errCodeRemoteUpgradeRequired, "remote upgrade required")
		return
	}
	if !slices.Contains(health.LaunchableAgents, agent) {
		writeAPIError(w, http.StatusConflict, errCodeRemoteAgentUnavailable, "remote agent unavailable")
		return
	}

	found := false
	for _, item := range remoteMgr.ListView(0).Sessions {
		if item.Key.SourceID == source && item.Key.Agent == agent && item.Key.SourceSessionID == id {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	cfg := state.Config.Remote
	if cfg == nil || cfg.ID != source || !cfg.Enabled {
		writeAPIError(w, http.StatusNotFound, errCodeRemoteNotConfigured, "remote not configured")
		return
	}

	var remoteCommand string
	switch mode {
	case "resume":
		command, err := launch.LaunchResumeRemoteCommand(agent, id)
		if err != nil {
			http.Error(w, "invalid launch request", http.StatusBadRequest)
			return
		}
		remoteCommand = command
	case "new":
		handoffText, err := readHandoff(w, r)
		if err != nil {
			log.Printf("launch: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		handoffID, err := stageRemoteHandoff(r.Context(), remoteMgr, agent, id, handoffText)
		if err != nil {
			log.Printf("launch: stage handoff: %v", err)
			http.Error(w, "could not stage remote handoff", http.StatusBadGateway)
			return
		}
		command, err := launch.LaunchNewRemoteCommand(agent, id, handoffID)
		if err != nil {
			http.Error(w, "invalid launch request", http.StatusBadRequest)
			return
		}
		remoteCommand = command
	}

	if err := launch.RemoteTerminal(state.Config.Launch.Terminal, cfg.SSHAlias, remoteCommand); err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, "could not launch terminal", http.StatusInternalServerError)
		return
	}
	log.Printf("launch %s: remote %s %s %s", mode, source, agent, id)
	w.WriteHeader(http.StatusNoContent)
}

func stageRemoteHandoff(ctx context.Context, mgr *remote.Manager, agent, sessionID, handoffText string) (string, error) {
	command, err := launch.HandoffPutRemoteCommand(agent, sessionID)
	if err != nil {
		return "", err
	}
	payload, _, err := mgr.RunFramedCommand(ctx, command, []byte(handoffText), 0)
	if err != nil {
		return "", err
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", fmt.Errorf("decode handoff response: %w", err)
	}
	if err := launch.ValidateHandoffID(response.ID); err != nil {
		return "", fmt.Errorf("invalid handoff id")
	}
	return response.ID, nil
}
