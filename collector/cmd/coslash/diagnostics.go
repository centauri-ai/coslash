package main

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
)

type diagnosticsCache struct {
	mu       sync.Mutex
	snapshot *diagnostics.Snapshot
	takenAt  time.Time
}

func newDiagnosticsHandler(version string, ttl time.Duration) http.Handler {
	return newDiagnosticsHandlerWithCollect(func(ctx context.Context) *diagnostics.Snapshot {
		return diagnostics.Collect(ctx, diagnostics.Default(version))
	}, ttl)
}

func newDiagnosticsHandlerWithCollect(
	collect func(context.Context) *diagnostics.Snapshot,
	ttl time.Duration,
) http.Handler {
	cache := &diagnosticsCache{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.snapshot == nil || time.Since(cache.takenAt) >= ttl || r.URL.Query().Get("refresh") == "1" {
			cache.snapshot = collect(context.WithoutCancel(r.Context()))
			cache.takenAt = time.Now()
			log.Printf("diagnostics: %d checks", len(cache.snapshot.Checks))
		}
		writeJSON(w, cache.snapshot)
	})
}
