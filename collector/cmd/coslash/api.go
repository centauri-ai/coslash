package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, session := range sessions {
		mtime, err := synthesis.TranscriptMtime(session.LogPath)
		if err != nil || mtime <= 0 {
			continue
		}
		session.Synthesis = mgr.Lookup(session.ID, mtime)
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

// /api/synthesis?id=X → cached synthesis for one session, triggering a run
// when eligible. Parses one file, never the whole machine — Get skips fork,
// subagents, and name/status resolution because BuildInput and Eligible read
// none of those.
func handleSynthesis(w http.ResponseWriter, id string, mgr *synthesis.Manager) {
	found, err := collector.Get(id)
	if err != nil {
		log.Printf("synthesis: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	response := struct {
		Synthesis        *session.SessionSynthesis `json:"synthesis"`
		SynthesisPending bool                      `json:"synthesisPending"`
	}{}
	mtime, err := synthesis.TranscriptMtime(found.LogPath)
	if err == nil && mtime > 0 {
		response.Synthesis = mgr.Lookup(found.ID, mtime)
		mgr.Ensure(found, mtime)
		response.SynthesisPending = response.Synthesis == nil && synthesis.Eligible(found) &&
			!mgr.InCooldown(found.ID, mtime)
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

func handleLaunch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	found, err := collector.Get(query.Get("id"))
	if err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	if err := launch.Terminal(found.Agent, found.WorkingDirectory, found.ID, mode, handoff); err != nil {
		log.Printf("launch: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("launch %s: %s %s", mode, found.Agent, found.ID)
	w.WriteHeader(http.StatusNoContent)
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
