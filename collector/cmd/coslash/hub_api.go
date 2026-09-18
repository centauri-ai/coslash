package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionexport"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

func hubClientFromEnvironment(collectorVersion string) (*hubclient.Client, error) {
	rawURL := strings.TrimSpace(os.Getenv("COSLASH_HUB_URL"))
	if rawURL == "" {
		return nil, nil
	}
	baseURL, err := url.Parse(rawURL)
	if err != nil || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" ||
		(baseURL.Scheme != "https" && baseURL.Scheme != "http") {
		return nil, errors.New("COSLASH_HUB_URL must be an absolute HTTP(S) URL without credentials")
	}
	if baseURL.Scheme == "http" {
		host := baseURL.Hostname()
		if host != "localhost" && net.ParseIP(host) == nil {
			return nil, errors.New("COSLASH_HUB_URL requires HTTPS except for loopback development")
		}
		if parsed := net.ParseIP(host); parsed != nil && !parsed.IsLoopback() {
			return nil, errors.New("COSLASH_HUB_URL requires HTTPS except for loopback development")
		}
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
		LoadSession:      collector.GetSessionForPreview,
	}, nil
}

func registerHubRoutes(api *http.ServeMux, client *hubclient.Client, remoteManager *remote.Manager) {
	if client != nil && remoteManager != nil {
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
			record, canonical, localOnly, err := remoteManager.ReadFullSessionForShare(sourceID, agent, sessionID, revisionID)
			return record, fullsessionexport.Repository{Canonical: canonical, LocalOnly: localOnly}, err
		}
	}
	api.HandleFunc("GET /api/hub/destination", func(w http.ResponseWriter, request *http.Request) {
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
		if client == nil {
			http.Error(w, "Hub server is not configured", http.StatusConflict)
			return
		}
		result, err := client.PollPairing(request.Context(), request.PathValue("id"))
		if err != nil {
			http.Error(w, "could not finish Hub pairing", http.StatusBadGateway)
			return
		}
		writeJSON(w, result)
	})
	api.HandleFunc("POST /api/hub/shares", func(w http.ResponseWriter, request *http.Request) {
		if client == nil {
			http.Error(w, "Hub server is not configured", http.StatusConflict)
			return
		}
		var input hubclient.ShareRequest
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid hub-share/v1 request", http.StatusBadRequest)
			return
		}
		result, err := client.Share(request.Context(), input)
		if err != nil {
			http.Error(w, "could not share to Hub", http.StatusBadGateway)
			return
		}
		writeJSON(w, result)
	})
	api.HandleFunc("GET /api/hub/full-session-preview", func(w http.ResponseWriter, request *http.Request) {
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
		writeJSON(w, client.PreviewFullSession(request.Context(), selection))
	})
	api.HandleFunc("POST /api/hub/full-session-shares", func(w http.ResponseWriter, request *http.Request) {
		if client == nil {
			http.Error(w, "Hub server is not configured", http.StatusConflict)
			return
		}
		var input hubclient.FullSessionShareRequest
		if err := decodeHubJSON(request.Body, &input); err != nil {
			http.Error(w, "invalid full-session-share/v1 request", http.StatusBadRequest)
			return
		}
		writeJSON(w, client.ShareFullSession(request.Context(), input))
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
