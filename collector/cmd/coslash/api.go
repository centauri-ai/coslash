package main

import (
	"context"
	"encoding/json"
	"errors"
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

// decodeSettingsSave separates persisted settings from the optional helper
// ownership action, which never becomes part of settings.json.
func decodeSettingsSave(data []byte) (settings.Config, remote.OwnershipAction, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return settings.Config{}, "", err
	}
	rawSettings, ok := fields["settings"]
	if !ok {
		return settings.Config{}, "", errors.New("settings save requires settings")
	}
	for key := range fields {
		if key != "settings" && key != "remoteOwnershipAction" {
			return settings.Config{}, "", errors.New("settings save contains unknown fields")
		}
	}
	var action string
	if rawAction, ok := fields["remoteOwnershipAction"]; ok {
		if err := json.Unmarshal(rawAction, &action); err != nil {
			return settings.Config{}, "", errors.New("invalid remote ownership action")
		}
	}
	config, err := settings.Decode(rawSettings)
	if err != nil {
		return settings.Config{}, "", err
	}
	parsed := remote.OwnershipAction(action)
	if parsed != remote.OwnershipActionNone && parsed != remote.OwnershipActionRelease && parsed != remote.OwnershipActionUninstall {
		return settings.Config{}, "", errors.New("invalid remote ownership action")
	}
	return config, parsed, nil
}

// /api/sessions → complete session records, optionally limited by an
// epoch-millisecond activity cutoff before transcript parsing.
func handleList(
	w http.ResponseWriter,
	r *http.Request,
	mgr *synthesis.Manager,
	remoteManager *remote.Manager,
) {
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
	sessions, err := collector.List(since)
	if err != nil {
		log.Printf("list sessions: %v", err)
		http.Error(w, "could not list sessions", http.StatusInternalServerError)
		return
	}
	for _, session := range sessions {
		session.Synthesis = mgr.Lookup(session.ID, session.LastActivityTime)
	}
	response := sessionsResponse{
		Sessions: []boardSession{},
		Machines: []machineFact{localMachineFact()},
	}
	for _, value := range sessions {
		response.Sessions = append(response.Sessions, boardLocalSession(value))
	}
	remoteResult := remoteManager.ListView(remoteSince)
	if remoteResult.Health.SourceID != "" {
		response.Machines = append(response.Machines, machineFromHealth(remoteResult.Health))
		for _, value := range remoteResult.Sessions {
			response.Sessions = append(response.Sessions, boardRemoteSession(value))
		}
	}
	writeJSON(w, response)
	log.Printf("list sessions: %d local, %d remote", len(sessions), len(remoteResult.Sessions))
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

// /api/synthesis?id=X → cached synthesis for one session, triggering a run
// when eligible. Loads one session, never the whole machine; GetSessionFacts skips fork,
// subagents, and name/status resolution because BuildInput and Eligible read
// none of those.
func handleSynthesis(w http.ResponseWriter, id string, mgr *synthesis.Manager) {
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

func handleLaunch(w http.ResponseWriter, r *http.Request, settingsStore *settings.Store, remoteManager *remote.Manager) {
	state := settingsStore.State()
	if !state.Valid {
		http.Error(w, state.Error+"; open Settings to repair it", http.StatusConflict)
		return
	}
	query := r.URL.Query()
	mode := query.Get("mode")
	sourceID, err := parseSourceID(query.Get("source"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var found *session.Session
	alias := ""
	if sourceID == localSourceID {
		found, err = collector.GetSessionFacts(query.Get("id"))
		if err != nil {
			log.Printf("launch: %v", err)
			http.Error(w, "could not load session", http.StatusInternalServerError)
			return
		}
	} else {
		found, alias, err = remoteManager.LaunchSession(sourceID, query.Get("agent"), query.Get("id"), mode)
		if errors.Is(err, remote.ErrRemoteSessionActive) {
			http.Error(w, "remote session is already active", http.StatusConflict)
			return
		}
		if errors.Is(err, remote.ErrRemoteSessionUnavailable) {
			http.Error(w, "remote session details are too large to launch", http.StatusConflict)
			return
		}
	}
	if found == nil {
		if sourceID == localSourceID {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "remote host is offline or this session is no longer available", http.StatusConflict)
		return
	}
	handoff, err := readHandoff(w, r)
	if err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if sourceID != localSourceID {
		health, testErr := remoteManager.TestAlias(r.Context(), alias)
		if testErr != nil || health.State != remote.StateOK {
			http.Error(w, "remote host is offline; wait for it to reconnect", http.StatusConflict)
			return
		}
		err = launch.RemoteTerminal(state.Config.Launch.Terminal, alias, found.Agent, found.WorkingDirectory, found.ID, mode, handoff)
	} else {
		err = launch.Terminal(state.Config.Launch.Terminal, found.Agent, found.WorkingDirectory, found.ID, mode, handoff)
	}
	if err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, "could not launch terminal", http.StatusInternalServerError)
		return
	}
	log.Printf("launch %s: %s %s", mode, found.Agent, found.ID)
	w.WriteHeader(http.StatusNoContent)
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
		SynthesisBackends []availableBackend  `json:"synthesisBackends"`
		Terminals         []availableTerminal `json:"terminals"`
	} `json:"options"`
}

func writeSettings(w http.ResponseWriter, state settings.State) {
	response := settingsResponse{
		Settings:  state.Config,
		Persisted: state.Persisted,
		Valid:     state.Valid,
		Error:     state.Error,
	}
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
	remoteManager *remote.Manager,
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
	config, ownershipAction, err := decodeSettingsSave(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	runner, err := synthesis.NewRunner(config.Synthesis)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ownershipAction == remote.OwnershipActionNone {
		err = remoteManager.ValidateSettingsChange(config.Remote)
	} else {
		err = remoteManager.ValidateSettingsChangeWithOwnershipAction(config.Remote, ownershipAction)
	}
	if err != nil {
		if errors.Is(err, remote.ErrHelperSetupInProgress) {
			writeAPIError(w, http.StatusConflict, "remote_helper_setup_in_progress", "wait for helper setup to finish before changing this host")
			return
		}
		if errors.Is(err, remote.ErrHelperOwnershipConflict) {
			writeAPIError(w, http.StatusConflict, "remote_helper_ownership_conflict", "uninstall or explicitly leave the helper before changing this host")
			return
		}
		if errors.Is(err, remote.ErrHelperOwnershipCorrupt) {
			writeAPIError(w, http.StatusConflict, "remote_helper_ownership_corrupt", "helper ownership needs explicit recovery before changing this host")
			return
		}
		http.Error(w, "could not validate remote settings", http.StatusInternalServerError)
		return
	}
	previous := store.State().Config
	if err := store.Save(config); err != nil {
		log.Printf("save settings: %v", err)
		http.Error(
			w,
			"could not save settings.json; check ~/.coslash permissions",
			http.StatusInternalServerError,
		)
		return
	}
	if ownershipAction != remote.OwnershipActionNone {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		err := remoteManager.ApplyOwnershipAction(ctx, ownershipAction)
		cancel()
		if err != nil {
			// No settings draft is committed if its requested ownership action
			// cannot complete. The remote manager is still on the old config.
			if restoreErr := store.Save(previous); restoreErr != nil {
				log.Printf("restore settings after helper action failure: %v", restoreErr)
			}
			writeAPIError(w, http.StatusBadGateway, "remote_helper_action_failed", "could not complete helper action; settings were kept")
			return
		}
	}
	mgr.SetRunner(runner)
	if err := remoteManager.ApplySettings(config.Remote); err != nil {
		log.Printf("apply remote settings: %v", err)
		if restoreErr := store.Save(previous); restoreErr != nil {
			log.Printf("restore settings after remote apply failure: %v", restoreErr)
		}
		if errors.Is(err, remote.ErrHelperSetupInProgress) {
			writeAPIError(w, http.StatusConflict, "remote_helper_setup_in_progress", "wait for helper setup to finish before changing this host")
			return
		}
		http.Error(w, "could not apply remote settings", http.StatusInternalServerError)
		return
	}
	writeSettings(w, store.State())
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
