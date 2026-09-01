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

type remoteHelperSetupRequest struct {
	Install bool `json:"install"`
	Upgrade bool `json:"upgrade"`
}

type helperSetupResponse struct {
	Machine machineFact `json:"machine"`
	Outcome string      `json:"outcome"`
	Error   string      `json:"error,omitempty"`
}

func handleRemoteTest(w http.ResponseWriter, request *http.Request, manager *remote.Manager) {
	var body remoteTestRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || !settings.ValidSSHAlias(body.SSHAlias) {
		http.Error(w, "invalid remote test request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), remote.DefaultConnectTimeout+5*time.Second)
	defer cancel()
	health, err := manager.TestAlias(ctx, body.SSHAlias)
	if err != nil {
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

func handleRemoteHelperSetup(w http.ResponseWriter, request *http.Request, manager *remote.Manager) {
	var body remoteHelperSetupRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "invalid helper setup request", http.StatusBadRequest)
		return
	}
	if body.Install == body.Upgrade {
		http.Error(w, "choose exactly one helper consent action", http.StatusBadRequest)
		return
	}
	health := manager.DiagnosticsHealth()
	if health.SourceID == "" {
		writeAPIError(w, http.StatusNotFound, errCodeRemoteNotConfigured, "remote not configured")
		return
	}
	if health.State == remote.StateDisabled {
		writeAPIError(w, http.StatusConflict, errCodeRemoteDisabled, "remote disabled")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
	defer cancel()
	setup := manager.SetupHelper(ctx, remote.Consent{Install: body.Install, Upgrade: body.Upgrade})
	if setup.Helper != nil && setup.Helper.Compatible {
		setup = manager.TestHelper(ctx)
	}
	response := helperSetupResponse{Machine: machineFromHealth(setup), Outcome: "sftp_fallback"}
	if setup.Helper != nil {
		response.Outcome = string(setup.Helper.State)
		if setup.Helper.Reason != nil {
			response.Error = genericHelperCopy(*setup.Helper.Reason)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if setup.Helper == nil || !setup.Helper.Compatible || setup.State == remote.StateError || setup.State == remote.StateLimited {
		w.WriteHeader(http.StatusConflict)
	}
	_ = json.NewEncoder(w).Encode(response)
}

func genericHelperCopy(reason remote.Reason) string {
	return remote.HelperErrorCopy(reason)
}

func handleRemoteStatus(w http.ResponseWriter, _ *http.Request, manager *remote.Manager) {
	health := manager.InspectHelper()
	if health.SourceID == "" {
		writeAPIError(w, http.StatusNotFound, errCodeRemoteNotConfigured, "remote not configured")
		return
	}
	writeJSON(w, machineFromHealth(health))
}

func handleRemoteHelperUninstall(w http.ResponseWriter, request *http.Request, manager *remote.Manager) {
	health := manager.DiagnosticsHealth()
	if health.SourceID == "" {
		writeAPIError(w, http.StatusNotFound, errCodeRemoteNotConfigured, "remote not configured")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
	defer cancel()
	if err := manager.UninstallHelper(ctx); err != nil {
		writeAPIError(w, http.StatusBadGateway, "remote_helper_uninstall_failed", "could not uninstall helper; host settings were kept")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleRemoteHelperReleaseOwnership(w http.ResponseWriter, _ *http.Request, manager *remote.Manager) {
	if err := manager.ReleaseHelperOwnership(); err != nil {
		writeAPIError(w, http.StatusConflict, "remote_helper_ownership_conflict", "could not release helper ownership")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
