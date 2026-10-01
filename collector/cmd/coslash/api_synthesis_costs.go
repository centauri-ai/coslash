package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func handleSynthesisCosts(w http.ResponseWriter, r *http.Request, manager *synthesis.Manager) {
	values := r.URL.Query()
	for key, entries := range values {
		switch key {
		case "source", "since", "until", "agent", "id", "limit", "cursor":
		default:
			http.Error(w, "invalid cost query", http.StatusBadRequest)
			return
		}
		if len(entries) != 1 {
			http.Error(w, "invalid cost query", http.StatusBadRequest)
			return
		}
	}
	if values.Get("source") != localSourceID || manager == nil {
		http.Error(w, "invalid cost query", http.StatusBadRequest)
		return
	}
	query := synthesis.CostQuery{SourceID: localSourceID}
	if values.Has("agent") || values.Has("id") {
		query.Agent, query.SessionID = values.Get("agent"), values.Get("id")
		if !validAgent(query.Agent) || !validOpaqueIdentifier(query.SessionID) || values.Has("since") || values.Has("until") {
			http.Error(w, "invalid cost query", http.StatusBadRequest)
			return
		}
		if values.Has("limit") {
			limit, err := strconv.Atoi(values.Get("limit"))
			if err != nil || limit < 1 || limit > 50 {
				http.Error(w, "invalid cost query", http.StatusBadRequest)
				return
			}
			query.Limit = limit
		}
		query.Cursor = values.Get("cursor")
		if values.Has("cursor") && query.Cursor == "" {
			http.Error(w, "invalid cost query", http.StatusBadRequest)
			return
		}
	} else {
		if !values.Has("since") || !values.Has("until") || values.Has("limit") || values.Has("cursor") {
			http.Error(w, "invalid cost query", http.StatusBadRequest)
			return
		}
		since, err1 := strconv.ParseInt(values.Get("since"), 10, 64)
		until, err2 := strconv.ParseInt(values.Get("until"), 10, 64)
		if err1 != nil || err2 != nil || since < 0 || since >= until {
			http.Error(w, "invalid cost query", http.StatusBadRequest)
			return
		}
		query.SinceMs, query.UntilMs = &since, &until
	}
	if manager.AccountingUnavailable() {
		http.Error(w, "synthesis accounting unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := manager.AccountingStore().ReadCosts(r.Context(), query)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if err.Error() == "invalid cost query" || err.Error() == "invalid time range" || err.Error() == "invalid cursor" {
			http.Error(w, "invalid cost query", http.StatusBadRequest)
			return
		}
		log.Printf("read synthesis accounting: %v", err)
		http.Error(w, "synthesis accounting unavailable", http.StatusServiceUnavailable)
		return
	}
	if query.Agent != "" {
		record, err := manager.LoadRecord(query.Agent, query.SessionID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("read synthesis summary metadata: %v", err)
			http.Error(w, "synthesis accounting unavailable", http.StatusServiceUnavailable)
			return
		}
		if err == nil && record.GeneratedAt < result.TrackingStartedAtMs {
			result.HistoricalUnknown = true
		}
	}
	writeJSON(w, result)
}
