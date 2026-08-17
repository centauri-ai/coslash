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
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
	"github.com/centauri-ai/coslash/collector/internal/sessionpreview"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

// /api/sessions → complete session records, optionally limited by an
// epoch-millisecond activity cutoff before transcript parsing.
func handleList(w http.ResponseWriter, r *http.Request, mgr *synthesis.Manager) {
	since, err := parseSince(r.URL.Query().Get("since"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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
	writeJSON(w, sessions)
	log.Printf("list sessions: %d", len(sessions))
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

func handleLaunch(w http.ResponseWriter, r *http.Request, settingsStore *settings.Store) {
	state := settingsStore.State()
	if !state.Valid {
		http.Error(w, state.Error+"; open Settings to repair it", http.StatusConflict)
		return
	}
	query := r.URL.Query()
	found, err := collector.GetSessionFacts(query.Get("id"))
	if err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, "could not load session", http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	handoff, err := readHandoff(w, r)
	if err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mode := query.Get("mode")
	if err := launch.Terminal(state.Config.Launch.Terminal, found.Agent, found.WorkingDirectory, found.ID, mode, handoff); err != nil {
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

// openCodeModels offers the user's own OpenCode model plus the free Zen
// models. Paid providers are reachable through the default rather than listed,
// since OpenCode resolves them from ambient credentials and the list runs to
// hundreds. Every id here passes settings.Validate, so the picker cannot
// produce a 400 on save.
func openCodeModels() []settings.ModelOption {
	models := []settings.ModelOption{{
		ID:    settings.OpenCodeDefaultModel,
		Label: "Whichever model OpenCode is set to use",
	}}
	preferred := false
	for _, id := range opencode.SynthesisModels() {
		if !settings.ValidSynthesisModel(settings.BackendOpenCode, id) {
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
