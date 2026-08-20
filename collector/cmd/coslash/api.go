package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
	"github.com/centauri-ai/coslash/collector/internal/sessionpreview"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

// /api/sessions → source-aware sessions plus machine health. Never waits on SSH.
func handleList(w http.ResponseWriter, r *http.Request, mgr *synthesis.Manager, remoteMgr *remote.Manager) {
	since, err := parseSince(r.URL.Query().Get("since"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	remoteSince := since
	if raw := r.URL.Query().Get("remoteSince"); raw != "" {
		remoteSince, err = parseSince(raw)
		if err != nil {
			http.Error(w, "invalid 'remoteSince' parameter", http.StatusBadRequest)
			return
		}
	}

	response := sessionsResponse{
		Sessions: []boardSession{},
		Machines: []machineFact{localMachineFact()},
	}

	sessions, err := collector.List(since)
	if err != nil {
		log.Printf("list sessions: %v", err)
		http.Error(w, "could not list sessions", http.StatusInternalServerError)
		return
	}
	for _, item := range sessions {
		item.Synthesis = mgr.Lookup(item.ID, item.LastActivityTime)
		response.Sessions = append(response.Sessions, boardLocalSession(item))
	}

	remoteView := remoteMgr.ListView(remoteSince)
	if remoteView.Health.SourceID != "" {
		response.Machines = append(response.Machines, machineFromHealth(remoteView.Health))
		for _, item := range remoteView.Sessions {
			response.Sessions = append(response.Sessions, boardRemoteSession(item))
		}
	}

	writeJSON(w, response)
	log.Printf("list sessions: %d", len(response.Sessions))
}

func parseSince(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	since, err := strconv.ParseInt(value, 10, 64)
	if err != nil || since < 0 {
		return 0, fmt.Errorf("invalid 'since' parameter")
	}
	return since, nil
}

func handleDiff(
	w http.ResponseWriter,
	r *http.Request,
	getSession func(string) (*session.Session, error),
) {
	if rejectRemoteSource(w, r) {
		return
	}
	query := r.URL.Query()
	found, err := getSession(query.Get("id"))
	if err != nil {
		log.Printf("diff: %v", err)
		http.Error(w, "could not load diff", http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	path := query.Get("path")
	var selected *session.FileEdit
	for index := range found.FileEdits {
		edit := &found.FileEdits[index]
		if edit.Path == path {
			selected = edit
			break
		}
	}
	if selected == nil {
		http.Error(w, "file not found in session", http.StatusNotFound)
		return
	}
	writeJSON(w, struct {
		Changes []session.FileChange `json:"changes"`
	}{Changes: selected.Changes()})
}

func handleSharePreview(
	w http.ResponseWriter,
	r *http.Request,
	getSession func(string, int64) (*session.Session, error),
	collectorVersion string,
) {
	if rejectRemoteSource(w, r) {
		return
	}
	revision, err := parseRevision(r.URL.Query().Get("revision"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	found, err := getSession(r.URL.Query().Get("id"), revision)
	if err != nil {
		log.Printf("share preview: %v", err)
		http.Error(w, "could not load share preview", http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	preview := sessionpreview.Build(*found, sessionexport.BuildOptions{
		CollectorVersion: collectorVersion,
		RepositoryRoot:   session.RepositoryRoot(found.WorkingDirectory),
	}, revision)
	writeJSON(w, preview)
}

func parseRevision(value string) (int64, error) {
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision <= 0 {
		return 0, fmt.Errorf("invalid 'revision' parameter")
	}
	return revision, nil
}

// /api/synthesis?id=X → cached synthesis for one local session.
func handleSynthesis(w http.ResponseWriter, r *http.Request, mgr *synthesis.Manager) {
	if rejectRemoteSource(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	found, err := collector.GetSessionFacts(id)
	if err != nil {
		log.Printf("synthesis: %v", err)
		http.Error(w, "could not load synthesis", http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	response := struct {
		Synthesis        *session.SessionSynthesis `json:"synthesis"`
		SynthesisPending bool                      `json:"synthesisPending"`
		SynthesisError   string                    `json:"synthesisError,omitempty"`
	}{}
	revision := found.LastActivityTime
	if revision > 0 {
		response.Synthesis = mgr.Lookup(found.ID, revision)
		mgr.Ensure(found, revision)
		response.SynthesisPending = response.Synthesis == nil && synthesis.Eligible(found) &&
			!mgr.InCooldown(found.ID, revision)
		if response.Synthesis == nil {
			response.SynthesisError = mgr.Failure(found.ID, revision)
		}
	}
	writeJSON(w, response)
	log.Printf("synthesis: %s", id)
}

func cleanupHandoffs() {
	ticker := time.NewTicker(launch.HandoffSweepInterval)
	defer ticker.Stop()
	for {
		if err := launch.CleanupHandoffs(); err != nil {
			log.Printf("sweep handoffs: %v", err)
		}
		<-ticker.C
	}
}

const maxSettingsBytes = 64 * 1024

type availableBackend struct {
	settings.BackendOption
	Available bool `json:"available"`
}

type availableTerminal struct {
	settings.TerminalOption
	Available bool `json:"available"`
}

type settingsResponse struct {
	Settings  settings.Config `json:"settings"`
	Persisted bool            `json:"persisted"`
	Valid     bool            `json:"valid"`
	Error     string          `json:"error,omitempty"`
	Options   struct {
		SynthesisBackends           []availableBackend  `json:"synthesisBackends"`
		Terminals                   []availableTerminal `json:"terminals"`
		RemoteInstallationGuidePath string              `json:"remoteInstallationGuidePath"`
	} `json:"options"`
}

func writeSettings(w http.ResponseWriter, state settings.State) {
	response := settingsResponse{
		Settings:  state.Config,
		Persisted: state.Persisted,
		Valid:     state.Valid,
		Error:     state.Error,
	}
	response.Options.RemoteInstallationGuidePath = remoteInstallationGuidePath
	for _, option := range settings.BackendOptions() {
		_, err := exec.LookPath(settings.BackendExecutable(option.ID))
		available := err == nil
		if option.ID == settings.BackendOpenCode && available {
			option.Models = openCodeModels()
		}
		response.Options.SynthesisBackends = append(
			response.Options.SynthesisBackends,
			availableBackend{
				BackendOption: option,
				Available:     available,
			},
		)
	}
	for _, option := range settings.TerminalOptions() {
		response.Options.Terminals = append(response.Options.Terminals, availableTerminal{
			TerminalOption: option,
			Available:      launch.Available(option.ID),
		})
	}
	writeJSON(w, response)
}

// Paid providers are reached through OpenCodeDefaultModel rather than listed:
// OpenCode resolves them from ambient credentials, running to hundreds of ids.
func openCodeModels() []settings.ModelOption {
	models := []settings.ModelOption{{
		ID:    settings.OpenCodeDefaultModel,
		Label: "Whichever model OpenCode is set to use",
	}}
	preferred := false
	for _, id := range opencode.SynthesisModels() {
		if !settings.ValidSynthesisModel(id) {
			continue
		}
		option := settings.ModelOption{ID: id, Label: id}
		if id == settings.OpenCodeSynthesisModel {
			option.Default = true
			preferred = true
		}
		models = append(models, option)
	}
	// Fall back to OpenCode's own model when the preferred one is retired.
	models[0].Default = !preferred
	return models
}

func handleSaveSettings(
	w http.ResponseWriter,
	r *http.Request,
	store *settings.Store,
	mgr *synthesis.Manager,
	remoteMgr *remote.Manager,
) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSettingsBytes))
	if err != nil {
		http.Error(
			w,
			fmt.Sprintf("settings exceed %d bytes", maxSettingsBytes),
			http.StatusBadRequest,
		)
		return
	}
	config, err := settings.Decode(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	previous := store.State().Config.Remote
	config.Remote, err = normalizeRemoteIdentity(previous, config.Remote)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	runner, err := synthesis.NewRunner(config.Synthesis)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.Save(config); err != nil {
		log.Printf("save settings: %v", err)
		http.Error(
			w,
			"could not save settings.json; check ~/.coslash permissions",
			http.StatusInternalServerError,
		)
		return
	}
	mgr.SetRunner(runner)
	if err := remoteMgr.ApplySettings(config.Remote); err != nil {
		log.Printf("apply remote settings: %v", err)
		http.Error(w, "could not apply remote settings", http.StatusInternalServerError)
		return
	}
	writeSettings(w, store.State())
}

// normalizeRemoteIdentity assigns Mac-owned source IDs. Alias changes always get a new ID.
func normalizeRemoteIdentity(previous, incoming *settings.RemoteSettings) (*settings.RemoteSettings, error) {
	if incoming == nil {
		return nil, nil
	}
	out := *incoming
	if previous != nil && previous.SSHAlias == out.SSHAlias {
		out.ID = previous.ID
		return &out, nil
	}
	id, err := settings.NewRemoteID()
	if err != nil {
		return nil, err
	}
	out.ID = id
	return &out, nil
}

func readHandoff(w http.ResponseWriter, r *http.Request) (string, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, launch.MaxHandoffBytes))
	if err != nil {
		return "", fmt.Errorf("handoff context exceeds %d bytes", launch.MaxHandoffBytes)
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("handoff context is not valid UTF-8")
	}
	return string(body), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encoding response: %v", err)
	}
}
