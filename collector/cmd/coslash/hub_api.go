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

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
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

func registerHubRoutes(api *http.ServeMux, client *hubclient.Client) {
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
		if rejectRemoteSource(w, request) {
			return
		}
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
