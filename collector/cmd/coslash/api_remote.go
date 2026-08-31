package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

type remoteTestRequest struct {
	SSHAlias string `json:"sshAlias"`
}

func handleRemoteTest(w http.ResponseWriter, request *http.Request, manager *remote.Manager) {
	var body remoteTestRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || !settings.ValidSSHAlias(body.SSHAlias) {
		remote.Observe("test", "phase", "reject", "reason", "invalid_request")
		http.Error(w, "invalid remote test request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), remote.DefaultConnectTimeout+5*time.Second)
	defer cancel()
	health, err := manager.TestAlias(ctx, body.SSHAlias)
	if err != nil {
		remote.Observe("test", "phase", "reject", "reason", "invalid_alias")
		http.Error(w, "invalid sshAlias", http.StatusBadRequest)
		return
	}
	writeJSON(w, machineFromHealth(health))
}

func handleRemoteRetry(w http.ResponseWriter, _ *http.Request, manager *remote.Manager) {
	health := manager.DiagnosticsHealth()
	if health.SourceID == "" {
		writeAPIError(w, http.StatusNotFound, errCodeRemoteNotConfigured, "remote not configured")
		return
	}
	if health.State == remote.StateDisabled {
		writeAPIError(w, http.StatusConflict, errCodeRemoteDisabled, "remote disabled")
		return
	}
	health = manager.Retry()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(machineFromHealth(health))
}
