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

// helperSetupOutcome is separate from machine collection health. A host may
// have a healthy SFTP cache while a requested helper action has failed; the
// setup response must never present that operation as a green success.
func helperSetupOutcome(health remote.Health) (outcome, errorCopy string, succeeded bool) {
	helper := health.Helper
	if helper == nil || !helper.Compatible {
		if helper == nil || helper.Reason == nil {
			return "sftp_fallback", "helper setup did not complete; SFTP remains active", false
		}
		switch *helper.Reason {
		case remote.ReasonHelperConsent:
			return "consent_required", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperUnsupported:
			return "unsupported", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperBlocked:
			return "blocked", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperIncompatible:
			return "incompatible", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperUpgrade:
			return "incompatible", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperRevoked:
			return "revoked", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperVerification:
			return "verification_failed", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperInstallation:
			return "installation_failed", genericHelperCopy(*helper.Reason), false
		case remote.ReasonHelperRollback:
			return "rolled_back", genericHelperCopy(*helper.Reason), false
		default:
			return "sftp_fallback", genericHelperCopy(*helper.Reason), false
		}
	}
	if health.State != remote.StateOK || !health.Complete {
		return "helper_test_failed", "helper installed but its collection test did not complete", false
	}
	if helper.State == remote.LifecycleDeprecated {
		return "deprecated_helper_active", "", true
	}
	if helper.Reused {
		return "reused_and_tested", "", true
	}
	return "installed_and_tested", "", true
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
	outcome, errorCopy, succeeded := helperSetupOutcome(setup)
	machine := machineFromHealth(setup)
	response := helperSetupResponse{Machine: machine, Outcome: outcome, Error: errorCopy}
	if !succeeded {
		// This response represents the setup operation, not an ordinary refresh.
		// Preserve the safe lifecycle reason and SFTP status, but make the failure
		// unambiguous to every client even if its prior SFTP collection was OK.
		response.Machine.State = remote.StateLimited
		response.Machine.Complete = false
		response.Machine.Error = errorCopy
		if setup.Helper != nil && setup.Helper.Reason != nil {
			response.Machine.Reason = setup.Helper.Reason
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if !succeeded {
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
