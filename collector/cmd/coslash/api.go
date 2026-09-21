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
	handoffcontext "github.com/centauri-ai/coslash/collector/internal/handoff"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
	"github.com/centauri-ai/coslash/collector/internal/sessionpreview"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

var (
	stageRemoteHandoff   = remote.StageHandoff
	removeRemoteHandoff  = remote.RemoveHandoff
	launchRemoteTerminal = launch.RemoteTerminal
	listSessions         = collector.List
)

var errRemoteHandoffTransfer = errors.New("remote handoff transfer failed")

// decodeSettingsSave accepts the legacy bare settings document and the T05
// envelope used when a settings replacement also has an explicit helper
// ownership action. The action never becomes part of settings.json.
func decodeSettingsSave(data []byte) (settings.Config, remote.OwnershipAction, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return settings.Config{}, "", err
	}
	rawSettings, enveloped := fields["settings"]
	if !enveloped {
		config, err := settings.Decode(data)
		return config, remote.OwnershipActionNone, err
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
	reviewManager *reviewpkg.Manager,
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
	sessions, err := listSessions(r.Context(), since)
	if err != nil {
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			return
		}
		log.Printf("list sessions: %v", err)
		http.Error(w, "could not list sessions", http.StatusInternalServerError)
		return
	}
	for _, session := range sessions {
		if r.Context().Err() != nil {
			return
		}
		session.Synthesis = mgr.Lookup(session.Agent, session.ID, session.LastActivityTime)
		state := reviewManager.Status(reviewpkg.Key(session.Agent, session.ID))
		session.ReviewPending = state.Pending
		session.ReviewError = state.Error
	}
	if r.URL.Query().Get("sourceAware") != "1" {
		if r.Context().Err() != nil {
			return
		}
		writeJSON(w, sessions)
		log.Printf("list sessions: %d", len(sessions))
		return
	}
	response := sessionsResponse{
		Sessions: []boardSession{},
		Machines: []machineFact{localMachineFact()},
	}
	for _, value := range sessions {
		if r.Context().Err() != nil {
			return
		}
		response.Sessions = append(response.Sessions, boardLocalSession(value))
	}
	if r.Context().Err() != nil {
		return
	}
	remoteResult := remoteManager.ListView(remoteSince)
	if remoteResult.Health.SourceID != "" {
		response.Machines = append(response.Machines, machineFromHealth(remoteResult.Health))
		for _, value := range remoteResult.Sessions {
			if r.Context().Err() != nil {
				return
			}
			response.Sessions = append(response.Sessions, boardRemoteSession(value))
		}
	}
	if r.Context().Err() != nil {
		return
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
	remoteManager *remote.Manager,
	collectorVersion string,
) {
	revision, err := parseRevision(r.URL.Query().Get("revision"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceID, err := parseSourceID(r.URL.Query().Get("source"))
	if err != nil {
		http.Error(w, "invalid source", http.StatusBadRequest)
		return
	}
	var found *session.Session
	if sourceID == localSourceID {
		found, err = getSession(r.URL.Query().Get("id"), revision)
	} else {
		agent := r.URL.Query().Get("agent")
		if agent == "" {
			http.Error(w, "remote preview requires an agent", http.StatusBadRequest)
			return
		}
		found, err = remoteManager.PreviewSession(sourceID, agent, r.URL.Query().Get("id"), revision)
	}
	if err != nil {
		log.Printf("share preview unavailable")
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

// /api/synthesis?agent=A&id=X → cached synthesis for one session, triggering a run
// when eligible. Loads one session, never the whole machine; GetSessionFacts skips fork,
// subagents, and name/status resolution because BuildInput and Eligible read
// none of those.
func handleSynthesis(w http.ResponseWriter, agent, id string, mgr *synthesis.Manager) {
	if agent == "" {
		http.Error(w, "agent is required", http.StatusBadRequest)
		return
	}
	found, err := collector.GetSessionFactsByAgent(agent, id)
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
		response.Synthesis = mgr.Lookup(found.Agent, found.ID, revision)
		mgr.Ensure(found, revision)
		response.SynthesisPending = response.Synthesis == nil && synthesis.Eligible(found) &&
			!mgr.InCooldown(found.Agent, found.ID, revision)
		if response.Synthesis == nil {
			response.SynthesisError = mgr.Failure(found.Agent, found.ID, revision)
		}
	}
	writeJSON(w, response)
	log.Printf("synthesis: %s", id)
}

func cleanupHandoffs(settingsStore *settings.Store) {
	ticker := time.NewTicker(launch.HandoffSweepInterval)
	defer ticker.Stop()
	for {
		if err := launch.CleanupHandoffs(); err != nil {
			log.Printf("sweep handoffs: %v", err)
		}
		state := settingsStore.State()
		if state.Valid && state.Config.Remote != nil && state.Config.Remote.Enabled {
			ctx, cancel := context.WithTimeout(context.Background(), remote.DefaultCapabilityTimeout)
			err := remote.CleanupHandoffs(ctx, state.Config.Remote.SSHAlias)
			cancel()
			if err != nil {
				log.Printf("sweep remote handoffs: %v", err)
			}
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
		if errors.Is(err, remote.ErrRemoteSessionOversized) {
			http.Error(w, "remote session details exceed the collection size limit", http.StatusConflict)
			return
		}
		if errors.Is(err, remote.ErrRemoteSessionUnavailable) {
			http.Error(w, "remote session is missing its working directory", http.StatusConflict)
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
		err = openRemoteTerminalWithHandoff(
			r.Context(), state.Config.Launch.Terminal, alias, found.Agent,
			found.WorkingDirectory, found.ID, mode, handoff,
		)
	} else if err = validateCursorLaunch(found, mode); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if mode == launch.OpenWorkspace {
		err = launch.CursorWorkspace(found.WorkingDirectory)
	} else {
		err = launch.Terminal(r.Context(), state.Config.Launch.Terminal, found.Agent, found.WorkingDirectory, found.ID, mode, handoff)
	}
	if err != nil {
		log.Printf("launch: %v", err)
		if errors.Is(err, errRemoteHandoffTransfer) {
			writeAPIError(w, http.StatusBadGateway, "remote_handoff_transfer_failed", "Could not transfer handoff; check SSH and try again.")
			return
		}
		writeTerminalLaunchError(w, err)
		return
	}
	log.Printf("launch %s: %s %s", mode, found.Agent, found.ID)
	w.WriteHeader(http.StatusNoContent)
}

func handleHandoff(
	w http.ResponseWriter,
	r *http.Request,
	getSession func(string, string) (*session.Session, error),
) {
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		http.Error(w, "agent is required", http.StatusBadRequest)
		return
	}
	found, err := getSession(agent, r.URL.Query().Get("id"))
	if err != nil {
		log.Printf("handoff: %v", err)
		http.Error(w, "could not load session", http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, handoffcontext.Build(found))
}

func canonicalSession(
	agent, id string,
	mgr *synthesis.Manager,
	load func(string, string, int64) (*session.Session, error),
) (*session.Session, error) {
	if agent == "" || id == "" {
		return nil, nil
	}
	found, err := load(agent, id, 0)
	if err != nil {
		return nil, err
	}
	if found != nil {
		found.Synthesis = mgr.Lookup(found.Agent, found.ID, found.LastActivityTime)
	}
	return found, nil
}

type promptLauncher func(context.Context, string, string, string, string, string, string, string) error

func handleSend(
	w http.ResponseWriter,
	r *http.Request,
	settingsStore *settings.Store,
	getSession func(string, string) (*session.Session, error),
	targetAvailable func(string) bool,
	open promptLauncher,
) {
	target := r.URL.Query().Get("to")
	if target != vendors.AgentClaude && target != vendors.AgentCodex {
		http.Error(w, "target must be claude or codex", http.StatusBadRequest)
		return
	}
	if !targetAvailable(target) {
		http.Error(w, "target is not installed or supported", http.StatusBadRequest)
		return
	}
	state := settingsStore.State()
	if !state.Valid {
		log.Printf("send: invalid settings: %s", state.Error)
		http.Error(w, "settings are invalid; open Settings to repair them", http.StatusConflict)
		return
	}
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		http.Error(w, "agent is required", http.StatusBadRequest)
		return
	}
	found, err := getSession(agent, r.URL.Query().Get("id"))
	if err != nil {
		log.Printf("send: %v", err)
		http.Error(w, "could not load session", http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	message, err := readMessage(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	handoff := handoffcontext.Build(found)
	if len(handoff) > launch.MaxHandoffBytes {
		http.Error(w, "handoff context is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	if err := open(
		r.Context(),
		state.Config.Launch.Terminal,
		target,
		found.WorkingDirectory,
		found.ID,
		launch.NewSession,
		handoff,
		message,
	); err != nil {
		log.Printf("send: %v", err)
		writeTerminalLaunchError(w, err)
		return
	}
	log.Printf("send: %s to %s", found.ID, target)
	w.WriteHeader(http.StatusNoContent)
}

func writeTerminalLaunchError(w http.ResponseWriter, err error) {
	if errors.Is(err, launch.ErrWorkingDirectoryUnavailable) {
		http.Error(w, "session working directory is unavailable", http.StatusConflict)
		return
	}
	http.Error(w, "could not launch terminal", http.StatusInternalServerError)
}

func openRemoteTerminalWithHandoff(
	ctx context.Context,
	terminal, alias, agent, workingDirectory, sessionID, mode, handoff string,
) error {
	handoffName := ""
	if mode == launch.NewSession && handoff != "" {
		contents, err := launch.RemoteHandoffContents(agent, handoff)
		if err != nil {
			return err
		}
		handoffName, err = stageRemoteHandoff(ctx, alias, contents)
		if err != nil {
			return fmt.Errorf("%w: %v", errRemoteHandoffTransfer, err)
		}
	}
	if err := launchRemoteTerminal(
		ctx, terminal, alias, agent, workingDirectory, sessionID, mode, handoffName,
	); err != nil {
		if handoffName != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), remote.DefaultCapabilityTimeout)
			defer cancel()
			return errors.Join(err, removeRemoteHandoff(cleanupCtx, alias, handoffName))
		}
		return err
	}
	return nil
}

func validateCursorLaunch(found *session.Session, mode string) error {
	if found.Agent != vendors.AgentCursor {
		if mode == launch.OpenWorkspace {
			return errors.New("workspace launch is only available for Cursor IDE sessions")
		}
		return nil
	}
	if !launch.ValidWorkingDirectory(found.WorkingDirectory) {
		return errors.New("session has no usable working directory")
	}
	entrypoint := ""
	if found.Entrypoint != nil {
		entrypoint = *found.Entrypoint
	}
	switch entrypoint {
	case "cursor-ide":
		if mode != launch.OpenWorkspace {
			return errors.New("exact resume is only available for Cursor CLI sessions")
		}
	case "cursor-cli":
		if mode != launch.ResumeSession && mode != launch.NewSession {
			return errors.New("workspace launch is only available for Cursor IDE sessions")
		}
		if mode == launch.ResumeSession && found.Status != nil && (*found.Status == "busy" || *found.Status == "idle") {
			return errors.New("session is already active")
		}
	default:
		return fmt.Errorf("launch is not available for %s sessions", entrypoint)
	}
	return nil
}

type reviewStarter func(string, reviewpkg.Launch) bool

func handleReview(
	w http.ResponseWriter,
	r *http.Request,
	settingsStore *settings.Store,
	getSession func(string, string) (*session.Session, error),
	reviewerAvailable func(string) bool,
	startReview reviewStarter,
) {
	state := settingsStore.State()
	if !state.Valid {
		log.Printf("review settings: %s", state.Error)
		http.Error(w, "settings are invalid; open Settings to repair them", http.StatusConflict)
		return
	}
	query := r.URL.Query()
	if query.Get("source") != localSourceID {
		http.Error(w, "reviews require a local session", http.StatusBadRequest)
		return
	}
	reviewer := query.Get("reviewer")
	if !reviewerAvailable(reviewer) {
		http.Error(w, "reviewer is not installed or supported", http.StatusBadRequest)
		return
	}
	originAgent := query.Get("agent")
	if originAgent == "" {
		http.Error(w, "agent is required", http.StatusBadRequest)
		return
	}
	found, err := getSession(originAgent, query.Get("id"))
	if err != nil {
		log.Printf("review: %v", err)
		http.Error(w, "could not load session", http.StatusInternalServerError)
		return
	}
	if found == nil || originAgent != "" && found.Agent != originAgent {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	originName := ""
	if found.Name != nil {
		originName = *found.Name
	}
	name := reviewpkg.Name(originName, found.ID)
	prompt := reviewpkg.Prompt(found)
	if r.Context().Err() != nil {
		return
	}
	if err := launch.ValidateWorkingDirectory(found.WorkingDirectory); err != nil {
		writeTerminalLaunchError(w, err)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	if !startReview(reviewpkg.Key(found.Agent, found.ID), reviewpkg.Launch{
		Reviewer: reviewer, WorkingDirectory: found.WorkingDirectory, Name: name, Prompt: prompt,
	}) {
		http.Error(w, "review already running", http.StatusConflict)
		return
	}
	log.Printf("review: %s with %s", found.ID, reviewer)
	w.WriteHeader(http.StatusAccepted)
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

type availableReviewer struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
}

type settingsResponse struct {
	Settings  settings.Config `json:"settings"`
	Persisted bool            `json:"persisted"`
	Valid     bool            `json:"valid"`
	Error     string          `json:"error,omitempty"`
	Options   struct {
		SynthesisBackends []availableBackend  `json:"synthesisBackends"`
		Terminals         []availableTerminal `json:"terminals"`
		Reviewers         []availableReviewer `json:"reviewers"`
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
	for _, option := range launch.ReviewerOptions() {
		response.Options.Reviewers = append(response.Options.Reviewers, availableReviewer{
			ID: option.ID, Label: option.Label, Available: launch.ReviewerAvailable(option.ID),
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

func readMessage(w http.ResponseWriter, r *http.Request) (string, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, launch.MaxHandoffBytes))
	if err != nil {
		return "", fmt.Errorf("message exceeds %d bytes", launch.MaxHandoffBytes)
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("message is not valid UTF-8")
	}
	return string(body), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encoding response: %v", err)
	}
}
