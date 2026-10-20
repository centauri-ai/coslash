package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func waitSettingsRefresh(t *testing.T, cache *settingsOptionsCache) {
	t.Helper()
	cache.mu.Lock()
	done := cache.running
	cache.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("settings discovery did not finish")
		}
	}
}

func TestSettingsRequestsDoNotWaitForDiscovery(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	cache := newSettingsOptionsCache()
	defer cache.close()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	cache.discover = func(ctx context.Context) settingsOptions {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		options := defaultSettingsOptions()
		options.SynthesisBackends[0].Available = true
		return options
	}
	store := settings.Open()
	server := newServer(httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}, synthesis.NewManager(nil, nil), review.NewManager(nil), store, remote.NewManager(remote.Options{}), nil, serverServices{settingsOptions: cache})
	request := func(method string, canceled bool) settingsResponse {
		t.Helper()
		config := settings.Defaults()
		config.SyncPaused = true
		data, _ := json.Marshal(config)
		req := httptest.NewRequest(method, "http://127.0.0.1:8787/api/settings", bytes.NewReader(data))
		req.Header.Set("X-Coslash-Token", "secret")
		if canceled {
			ctx, cancel := context.WithCancel(req.Context())
			cancel()
			req = req.WithContext(ctx)
		}
		result := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { server.Handler.ServeHTTP(result, req); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("settings request waited for blocked discovery")
		}
		if result.Code != http.StatusOK {
			t.Fatalf("%s status=%d: %s", method, result.Code, result.Body.String())
		}
		var response settingsResponse
		if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	if response := request(http.MethodPut, false); !response.Persisted || !response.Settings.SyncPaused || !response.Options.Refreshing {
		t.Fatalf("cold PUT lost saved state or pending discovery: %+v", response)
	}
	<-started
	for i := 0; i < 4; i++ {
		if !request(http.MethodGet, i == 0).Options.Refreshing {
			t.Fatal("blocked discovery was reported complete")
		}
		request(http.MethodPut, false)
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent requests started %d discoveries", calls.Load())
	}
	close(release)
	waitSettingsRefresh(t, cache)
	if response := request(http.MethodGet, false); response.Options.Refreshing || !response.Options.SynthesisBackends[0].Available || !response.Settings.SyncPaused {
		t.Fatal("refreshed capabilities or persisted settings were lost")
	}
	request(http.MethodPut, false)
	if calls.Load() != 1 {
		t.Fatal("warm PUT repeated discovery")
	}
}

func TestSettingsOptionsExpiryServesPreviousSnapshotAndCachesMissingAgents(t *testing.T) {
	cache := newSettingsOptionsCache()
	defer cache.close()
	now := time.Unix(1, 0)
	cache.now = func() time.Time { return now }
	var calls atomic.Int32
	cache.discover = func(context.Context) settingsOptions { calls.Add(1); return defaultSettingsOptions() }
	cache.snapshot()
	waitSettingsRefresh(t, cache)
	if cache.snapshot().Refreshing || calls.Load() != 1 {
		t.Fatal("missing agents were not cached")
	}
	now = now.Add(settingsOptionsTTL)
	started := make(chan struct{})
	release := make(chan struct{})
	cache.discover = func(ctx context.Context) settingsOptions {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		options := defaultSettingsOptions()
		options.SynthesisBackends[0].Available = true
		return options
	}
	if snapshot := cache.snapshot(); !snapshot.Refreshing || snapshot.SynthesisBackends[0].Available {
		t.Fatal("expired snapshot must remain usable during refresh")
	}
	<-started
	cache.snapshot()
	if calls.Load() != 2 {
		t.Fatal("expired discovery was not deduplicated")
	}
	close(release)
	waitSettingsRefresh(t, cache)
	if snapshot := cache.snapshot(); snapshot.Refreshing || !snapshot.SynthesisBackends[0].Available {
		t.Fatal("newly installed agent was not published")
	}
}

func TestSettingsOptionsIncompleteDiscoveryPreservesSnapshotAndRetries(t *testing.T) {
	cache := newSettingsOptionsCache()
	defer cache.close()
	cache.options.SynthesisBackends[0].Available = true
	var cancelDiscovery context.CancelFunc
	cache.withTimeout = func(parent context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancelDiscovery = cancel
		return ctx, cancel
	}
	var calls atomic.Int32
	started := make(chan struct{})
	cache.discover = func(ctx context.Context) settingsOptions {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return defaultSettingsOptions()
		}
		options := defaultSettingsOptions()
		options.SynthesisBackends[1].Available = true
		return options
	}

	cache.snapshot()
	<-started
	cancelDiscovery()
	waitSettingsRefresh(t, cache)
	cache.mu.Lock()
	firstAvailable := cache.options.SynthesisBackends[0].Available
	cache.mu.Unlock()
	if !firstAvailable {
		t.Fatal("incomplete discovery replaced the last complete snapshot")
	}

	cache.snapshot()
	waitSettingsRefresh(t, cache)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if calls.Load() != 2 || !cache.options.SynthesisBackends[1].Available {
		t.Fatal("incomplete discovery was not retried")
	}
}

func TestSettingsOptionsShutdownCancelsRefreshAndDiscardsCompletion(t *testing.T) {
	cache := newSettingsOptionsCache()
	started := make(chan struct{})
	cache.discover = func(ctx context.Context) settingsOptions {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("refresh has no deadline")
		}
		close(started)
		<-ctx.Done()
		options := defaultSettingsOptions()
		options.SynthesisBackends[0].Available = true
		return options
	}
	cache.snapshot()
	<-started
	cache.close()
	if snapshot := cache.snapshot(); snapshot.Refreshing || snapshot.SynthesisBackends[0].Available {
		t.Fatal("shutdown published stale work or restarted discovery")
	}
}

func TestSettingsOptionsMissingExecutables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("PATH", t.TempDir())
	options := discoverSettingsOptions(context.Background())
	for _, option := range options.SynthesisBackends {
		if option.Available {
			t.Fatalf("missing backend %s available", option.ID)
		}
	}
	for _, option := range options.Reviewers {
		if option.Available {
			t.Fatalf("missing reviewer %s available", option.ID)
		}
	}
}
