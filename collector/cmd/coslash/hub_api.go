package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionexport"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func hubClientFromEnvironment(collectorVersion string) (*hubclient.Client, error) {
	rawURL := strings.TrimSpace(os.Getenv("COSLASH_HUB_URL"))
	if rawURL == "" {
		stored, err := readStoredHubURL()
		if err != nil {
			return nil, err
		}
		rawURL = stored
	}
	if rawURL == "" {
		return nil, nil
	}
	return hubClientForURL(collectorVersion, rawURL)
}

func hubClientForURL(collectorVersion, rawURL string) (*hubclient.Client, error) {
	baseURL, err := hubclient.ValidateHubURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	deviceName, _ := os.Hostname()
	deviceName = strings.TrimSpace(deviceName)
	if deviceName == "" {
		deviceName = "coSlash Local"
	}
	return &hubclient.Client{
		BaseURL: baseURL,
		Credentials: hubclient.OSKeychain{
			Service: "ai.coslash.hub-device",
			Account: baseURL.Host,
		},
		DeviceName:       deviceName,
		CollectorVersion: collectorVersion,
		InstallChannel:   detectedInstallChannel(),
		LoadSession:      collector.GetSessionForPreview,
	}, nil
}

func localInstallChannel() string {
	executable, err := os.Executable()
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
	}
	return installChannelFor(runtime.GOOS, filepath.ToSlash(executable))
}

func installChannelFor(goos, executable string) string {
	if goos == "windows" {
		return "windows-script"
	}
	if goos == "darwin" && strings.Contains(executable, "/Cellar/coslash/") {
		return "brew"
	}
	if goos == "darwin" || goos == "linux" {
		return "script"
	}
	return "unknown"
}

func registerHubRoutes(api *http.ServeMux, client *hubclient.Client, remoteManager *remote.Manager, backupManager *sessionbackupproducer.Manager, onboardings *onboardingManager) {
	bindClient := func(client *hubclient.Client) {
		if client == nil {
			return
		}
		client.RequireLocalSynthesis = true
		if remoteManager != nil {
			client.LoadSourceSession = func(sourceID, agent, sessionID string, revision int64) (*session.Session, error) {
				if sourceID == localSourceID {
					found, err := collector.GetSessionForPreview(sessionID, revision)
					if found == nil || err != nil || found.Agent != agent {
						return nil, err
					}
					return found, nil
				}
				return remoteManager.PreviewSession(sourceID, agent, sessionID, revision)
			}
			client.LoadFullSession = func(sourceID, agent, sessionID, revisionID string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
				if sourceID == localSourceID {
					return nil, fullsessionexport.Repository{}, nil
				}
				if agent != vendors.AgentCodex {
					return nil, fullsessionexport.Repository{}, nil
				}
				record, canonical, localOnly, err := remoteManager.ReadFullSessionForShare(sourceID, agent, sessionID, revisionID)
				return record, fullsessionexport.Repository{Canonical: canonical, LocalOnly: localOnly}, err
			}
		}
		client.Backup = backupManager
	}
	if onboardings != nil {
		onboardings.setHubClientBinder(bindClient)
		onboardings.setHubClient(client)
	} else {
		bindClient(client)
	}
	currentClient := func() *hubclient.Client {
		if onboardings != nil {
			return onboardings.currentHubClient()
		}
		return client
	}
	api.HandleFunc("GET /api/hub/destination", func(w http.ResponseWriter, request *http.Request) {
		client := currentClient()
		if client == nil {
			writeJSON(w, hubclient.DestinationResult{ContractVersion: hubclient.ContractVersion, State: "signed_out", Configured: false})
			return
		}
		result, err := client.Destination(request.Context())
		if err != nil {
			http.Error(w, "could not load Hub destination", http.StatusBadGateway)
			return
		}
		writeJSON(w, result)
	})
	api.HandleFunc("POST /api/hub/pairings", func(w http.ResponseWriter, request *http.Request) {
		client := currentClient()
		if client == nil {
			http.Error(w, "Hub server is not configured", http.StatusConflict)
			return
		}
		result, err := client.BeginPairing(request.Context())
		if err != nil {
			http.Error(w, "could not begin Hub pairing", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(result)
	})
	api.HandleFunc("POST /api/hub/pairings/{id}/poll", func(w http.ResponseWriter, request *http.Request) {
		client := currentClient()
		if client == nil {
			http.Error(w, "Hub server is not configured", http.StatusConflict)
			return
		}
		result, err := client.PollPairing(request.Context(), request.PathValue("id"))
		if err != nil {
			http.Error(w, "could not finish Hub pairing", http.StatusBadGateway)
			return
		}
		if result.State == "paired" && onboardings != nil {
			onboardings.ensureSync(client)
		}
		writeJSON(w, result)
	})
	api.HandleFunc("POST /api/hub/onboarding/activate", func(w http.ResponseWriter, request *http.Request) {
		if onboardings == nil {
			http.Error(w, "Hub onboarding is unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			HubURL       string `json:"hubUrl"`
			AttemptID    string `json:"attemptId"`
			LaunchIntent string `json:"launchIntent"`
		}
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid Hub onboarding activation", http.StatusBadRequest)
			return
		}
		if err := onboardings.StartPairing(input.HubURL, input.AttemptID, input.LaunchIntent); err != nil {
			http.Error(w, "could not start Hub pairing", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{"state": "starting"})
	})
	api.HandleFunc("POST /api/hub/onboarding/connect", func(w http.ResponseWriter, request *http.Request) {
		if onboardings == nil {
			http.Error(w, "unreachable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			HubURL      string `json:"hubUrl"`
			ConnectCode string `json:"connectCode"`
		}
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid connect request", http.StatusBadRequest)
			return
		}
		id, err := onboardings.StartConnectCode(input.HubURL, input.ConnectCode)
		if errors.Is(err, hubclient.ErrConnectCodeInvalid) {
			http.Error(w, hubclient.ErrConnectCodeInvalid.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, hubclient.ErrConnectUnsupported) {
			http.Error(w, hubclient.ErrConnectUnsupported.Error(), http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "unreachable", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{"id": id, "state": "claimed"})
	})
	api.HandleFunc("GET /api/hub/onboarding/connect/{id}", func(w http.ResponseWriter, request *http.Request) {
		if onboardings == nil {
			http.Error(w, "connect status is unavailable", http.StatusServiceUnavailable)
			return
		}
		state, ok := onboardings.ConnectJobState(request.PathValue("id"))
		if !ok {
			http.Error(w, "connect status is unavailable", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"state": state})
	})
	api.HandleFunc("POST /api/hub/wake", func(w http.ResponseWriter, request *http.Request) {
		if onboardings == nil {
			http.Error(w, "Local sync is unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			HubURL string `json:"hubUrl"`
		}
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid wake request", http.StatusBadRequest)
			return
		}
		if err := onboardings.RequestWake(input.HubURL); errors.Is(err, errForeignWakeHub) {
			w.WriteHeader(http.StatusNoContent)
			return
		} else if errors.Is(err, errWakeRateLimited) {
			http.Error(w, "wake rate limited", http.StatusTooManyRequests)
			return
		} else if err != nil {
			http.Error(w, "Local sync is unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	api.HandleFunc("POST /api/hub/onboarding/check-in", func(w http.ResponseWriter, request *http.Request) {
		if onboardings == nil {
			http.Error(w, "Hub check-in is unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			HubURL string `json:"hubUrl"`
		}
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid Hub check-in request", http.StatusBadRequest)
			return
		}
		if err := onboardings.RetryCheckIn(input.HubURL); err != nil {
			http.Error(w, "Local could not check in with Hub", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("POST /api/hub/shares", func(w http.ResponseWriter, request *http.Request) {
		client := currentClient()
		if client == nil {
			http.Error(w, "Hub server is not configured", http.StatusConflict)
			return
		}
		var input hubclient.BackupShareRequest
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid hub-share/v2 request", http.StatusBadRequest)
			return
		}
		result, err := client.ShareBackups(request.Context(), input)
		if err != nil {
			log.Printf("complete-backup share request failed")
			http.Error(w, "could not share to Hub", http.StatusBadGateway)
			return
		}
		writeJSON(w, result)
	})
	api.HandleFunc("POST /api/hub/backup-previews", func(w http.ResponseWriter, request *http.Request) {
		client := currentClient()
		if client == nil || backupManager == nil {
			writeJSON(w, hubclient.BackupPreview{AdapterVersion: hubclient.BackupPreviewVersion, State: "incompatible_server",
				Coverage: sessionbackupproducer.Coverage{Problems: []sessionbackupv1.CaptureProblem{}}, Problem: &hubclient.BackupPreviewProblem{
					Code: "incompatible_server", Message: "Complete backup sharing is not configured.",
					Action: "Configure and pair a compatible Hub.", Retryable: false,
				}})
			return
		}
		var selection sessionbackupproducer.Selection
		if err := decodeHubJSON(request.Body, &selection); err != nil {
			http.Error(w, "invalid backup-preview/v1 request", http.StatusBadRequest)
			return
		}
		result, err := client.PrepareBackup(request.Context(), selection)
		if err != nil {
			code := "temporary_unavailable"
			if result.Problem != nil {
				code = result.Problem.Code
			}
			log.Printf("complete-backup preview failed: code=%s", code)
		}
		writeJSON(w, result)
	})
	api.HandleFunc("GET /api/hub/full-session-preview", func(w http.ResponseWriter, request *http.Request) {
		client := currentClient()
		if client == nil {
			writeJSON(w, hubclient.FullSessionPreview{
				AdapterVersion: fullsessionexport.PreviewVersion, State: "incompatible_server",
				Problem: &hubclient.FullSessionPreviewProblem{Code: "incompatible_server", Message: "Hub sharing is not configured.", Action: "Configure and pair a Hub before previewing."},
			})
			return
		}
		selection := hubclient.FullSessionSelection{
			SourceID: request.URL.Query().Get("source"), Agent: request.URL.Query().Get("agent"),
			SessionID: request.URL.Query().Get("id"), RevisionID: request.URL.Query().Get("revision"),
		}
		result, err := client.PreviewFullSession(request.Context(), selection)
		if err != nil {
			log.Printf("full-session preview: %v", err)
		}
		writeJSON(w, result)
	})
	api.HandleFunc("POST /api/hub/full-session-shares", func(w http.ResponseWriter, request *http.Request) {
		client := currentClient()
		if client == nil {
			http.Error(w, "Hub server is not configured", http.StatusConflict)
			return
		}
		var input hubclient.FullSessionShareRequest
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid full-session-share/v1 request", http.StatusBadRequest)
			return
		}
		result, err := client.ShareFullSession(request.Context(), input)
		if err != nil {
			log.Printf("full-session share: %v", err)
		}
		writeJSON(w, result)
	})
}

func decodeHubJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
