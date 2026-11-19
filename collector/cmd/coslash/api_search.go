package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	searchpkg "github.com/centauri-ai/coslash/collector/internal/search"
)

func handleSearchStatus(w http.ResponseWriter, r *http.Request, service *searchService) {
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
	if service == nil || service.index == nil {
		status := searchpkg.Status{State: "unavailable"}
		if identity != nil {
			status.Coverage = "absent"
		}
		_ = json.NewEncoder(w).Encode(status)
		return
	}
	status, err := service.index.Status(r.Context(), identity)
	if err != nil {
		log.Printf("search status: %v", err)
		http.Error(w, "could not read search status", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(status)
}

func handleSearch(w http.ResponseWriter, r *http.Request, service *searchService) {
	q := r.URL.Query()
	if len(q.Get("q")) > 8*1024 {
		http.Error(w, "search query is too long", http.StatusBadRequest)
		return
	}
	parsed := searchpkg.ParseQuery(q.Get("q"))
	if len(parsed.Terms) == 0 {
		http.Error(w, "search requires content terms", http.StatusBadRequest)
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
	if service == nil || service.index == nil {
		http.Error(w, "search index unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := service.index.Status(r.Context(), nil)
	if err != nil {
		log.Printf("search status: %v", err)
		http.Error(w, "search index unavailable", http.StatusServiceUnavailable)
		return
	}
	if status.State != "ready" {
		message := "search index unavailable"
		if status.State == "building" {
			message = "search index building"
		}
		http.Error(w, message, http.StatusServiceUnavailable)
		return
	}
	result, err := service.index.Search(r.Context(), q.Get("q"), searchpkg.Options{Repo: q.Get("repo"), Agent: q.Get("agent"), Since: since, Limit: limit})
	if err != nil {
		log.Printf("search query: %v", err)
		http.Error(w, "could not search sessions", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
