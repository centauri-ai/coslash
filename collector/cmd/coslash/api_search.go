package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	searchpkg "github.com/centauri-ai/coslash/collector/internal/search"
)

func handleSearchStatus(w http.ResponseWriter, r *http.Request, index *searchpkg.Index) {
	q := r.URL.Query()
	var identity *searchpkg.Identity
	if q.Get("source") != "" || q.Get("agent") != "" || q.Get("session") != "" {
		if q.Get("source") == "" || q.Get("agent") == "" || q.Get("session") == "" {
			http.Error(w, "source, agent and session are required together", http.StatusBadRequest)
			return
		}
		identity = &searchpkg.Identity{SourceID: q.Get("source"), Agent: q.Get("agent"), SessionID: q.Get("session")}
	}
	w.Header().Set("Content-Type", "application/json")
	if index == nil {
		status := searchpkg.Status{State: "unavailable"}
		if identity != nil {
			status.Coverage = "absent"
		}
		_ = json.NewEncoder(w).Encode(status)
		return
	}
	status, err := index.Status(r.Context(), identity)
	if err != nil {
		log.Printf("search status: %v", err)
		http.Error(w, "could not read search status", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(status)
}

func handleSearch(w http.ResponseWriter, r *http.Request, index *searchpkg.Index) {
	q := r.URL.Query()
	if len(q.Get("q")) > 8*1024 {
		http.Error(w, "search query is too long", http.StatusBadRequest)
		return
	}
	since, err := parseSince(q.Get("since"))
	if err != nil {
		http.Error(w, "invalid since parameter", http.StatusBadRequest)
		return
	}
	limit := 0
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 2000 {
			http.Error(w, "invalid limit parameter", http.StatusBadRequest)
			return
		}
	}
	if index == nil {
		http.Error(w, searchpkg.ErrUnavailable.Error(), http.StatusServiceUnavailable)
		return
	}
	result, err := index.Search(r.Context(), q.Get("q"), searchpkg.Options{Repo: q.Get("repo"), Agent: q.Get("agent"), Since: since, Limit: limit})
	switch {
	case errors.Is(err, searchpkg.ErrEmptyQuery):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, searchpkg.ErrBuilding), errors.Is(err, searchpkg.ErrUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case err != nil:
		log.Printf("search query: %v", err)
		http.Error(w, "could not search sessions", http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}
