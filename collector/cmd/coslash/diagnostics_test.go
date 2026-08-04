package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
)

func TestDiagnosticsHandlerCachesSnapshot(t *testing.T) {
	calls := 0
	handler := newDiagnosticsHandlerWithCollect(func(context.Context) *diagnostics.Snapshot {
		calls++
		return &diagnostics.Snapshot{Checks: []diagnostics.Check{{ID: "storage", Status: diagnostics.StatusOK}}}
	}, time.Minute)

	for range 2 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		if got := response.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q", got)
		}
		if !strings.Contains(response.Body.String(), `"checks":[`) {
			t.Fatalf("response has no checks: %s", response.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("collect calls = %d, want 1", calls)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/diagnostics?refresh=1", nil))
	if calls != 2 {
		t.Fatalf("collect calls after refresh = %d, want 2", calls)
	}
}
