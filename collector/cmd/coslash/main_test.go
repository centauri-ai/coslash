package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestListenBindsIPv4Loopback(t *testing.T) {
	listener, err := listen(0)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("sandbox does not permit opening a loopback listener: %v", err)
		}
		t.Fatal(err)
	}
	defer listener.Close()

	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("listener host = %q, want 127.0.0.1", host)
	}
}

func TestAPIRoutesRejectUnsupportedMethods(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	handler := routes(synthesis.NewManager(nil), settings.Open(), canvas.New())
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/sessions"},
		{method: http.MethodPost, path: "/api/synthesis"},
		{method: http.MethodGet, path: "/api/launch"},
		{method: http.MethodPost, path: "/api/diagnostics"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://127.0.0.1"+test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestServerWrapsRoutesWithGuard(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	server := newServer(
		httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"},
		synthesis.NewManager(nil),
		settings.Open(),
		canvas.New(),
	)
	request := httptest.NewRequest(http.MethodGet, "http://evil.example:8787/", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

// recordingPlugin stands in for the real Canvas plugin so the lifecycle can be
// observed without any product behavior existing yet. serve drives it from its
// own goroutine, so the counters are atomic.
type recordingPlugin struct {
	registered atomic.Bool
	started    atomic.Int32
	closed     atomic.Int32
	startErr   error
	handle     string
}

func (p *recordingPlugin) Register(mux *http.ServeMux) {
	p.registered.Store(true)
	if p.handle != "" {
		mux.HandleFunc("GET "+p.handle, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

func (p *recordingPlugin) Start(context.Context) error {
	p.started.Add(1)
	return p.startErr
}

func (p *recordingPlugin) Close(context.Context) error {
	p.closed.Add(1)
	return nil
}

func TestRoutesRegistersPluginUnderAPIGuard(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	plugin := &recordingPlugin{handle: "/api/canvas/ping"}
	guard := httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}
	handler := guard.Wrap(routes(synthesis.NewManager(nil), settings.Open(), plugin))
	if !plugin.registered.Load() {
		t.Fatal("plugin was not registered")
	}

	unauthenticated := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/canvas/ping", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unauthenticated)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated plugin route status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	authenticated := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/canvas/ping", nil)
	authenticated.Header.Set("X-Coslash-Token", "secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, authenticated)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated plugin route status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestRoutesKeepCoreEndpointsAheadOfPlugin(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	// A plugin that claims a core pattern must not be able to take it over.
	plugin := &recordingPlugin{handle: "/api/diagnostics"}
	defer func() {
		if recover() == nil {
			t.Fatal("registering a core pattern should conflict rather than shadow it")
		}
	}()
	routes(synthesis.NewManager(nil), settings.Open(), plugin)
}

func TestServeStartsAndClosesPlugin(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	plugin := &recordingPlugin{}
	done := make(chan error, 1)
	go func() {
		done <- serve(
			listener,
			httpsec.Guard{Addr: listener.Addr().String(), Token: "secret"},
			synthesis.NewManager(nil),
			settings.Open(),
			plugin,
		)
	}()

	// Closing the listener ends Serve the same way an interrupt would.
	waitFor(t, func() bool { return plugin.started.Load() == 1 })
	listener.Close()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return")
	}
	if got := plugin.started.Load(); got != 1 {
		t.Fatalf("plugin starts = %d, want 1", got)
	}
	if got := plugin.closed.Load(); got != 1 {
		t.Fatalf("plugin closes = %d, want 1", got)
	}
}

func TestServeFailsWhenPluginCannotStart(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	plugin := &recordingPlugin{startErr: errors.New("boom")}
	err = serve(
		listener,
		httpsec.Guard{Addr: listener.Addr().String(), Token: "secret"},
		synthesis.NewManager(nil),
		settings.Open(),
		plugin,
	)
	if err == nil {
		t.Fatal("serve should fail when the plugin cannot start")
	}
	if got := plugin.closed.Load(); got != 0 {
		t.Fatalf("plugin closes = %d, want 0 when it never started", got)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	for range 100 {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met in time")
}

func TestTokenLifecycle(t *testing.T) {
	token, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("token contains %d bytes, want 32", len(decoded))
	}

	home := filepath.Join(t.TempDir(), "coslash")
	t.Setenv("COSLASH_HOME", home)
	if err := writeToken(token); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "token")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != token {
		t.Fatal("token file does not contain generated token")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteTokenPreservesHomePermissions(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_HOME", home)

	if err := writeToken("secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("home mode = %o, want 750", info.Mode().Perm())
	}
}
