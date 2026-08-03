package main

import (
	"context"
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

func main() {
	sessionCollector := collector.New()
	mgr := synthesis.NewManager(synthesis.NewCLIRunner())
	if err := synthesis.EnsureDirs(); err != nil {
		log.Printf("initialize synthesis cache: %v", err)
		mgr = synthesis.NewManager(nil)
	}
	go mgr.Run(context.Background(), func(since int64) ([]*session.Session, error) {
		return sessionCollector.List(collector.ListOptions{Since: since})
	})
	go cleanupHandoffs()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleList(w, r, mgr, sessionCollector)
	})
	mux.HandleFunc("GET /api/synthesis", func(w http.ResponseWriter, r *http.Request) {
		handleSynthesis(w, r.URL.Query().Get("id"), mgr, sessionCollector)
	})
	mux.HandleFunc("POST /api/launch", func(w http.ResponseWriter, r *http.Request) {
		handleLaunch(w, r, sessionCollector)
	})
	addr := "127.0.0.1:8787"
	log.Printf("listening on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// /api/sessions → complete session records, optionally restricted to roots
// active since an epoch-millisecond cutoff. Live sessions always remain visible.
func handleList(
	w http.ResponseWriter,
	r *http.Request,
	mgr *synthesis.Manager,
	sessionCollector *collector.Collector,
) {
	since, err := parseSince(r.URL.Query().Get("since"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessions, err := sessionCollector.List(collector.ListOptions{Since: since})
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
func handleSynthesis(
	w http.ResponseWriter,
	id string,
	mgr *synthesis.Manager,
	sessionCollector *collector.Collector,
) {
	found, err := sessionCollector.Get(id)
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

func handleLaunch(w http.ResponseWriter, r *http.Request, sessionCollector *collector.Collector) {
	query := r.URL.Query()
	found, err := sessionCollector.Get(query.Get("id"))
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
