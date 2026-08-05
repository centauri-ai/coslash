package main

import (
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
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
	handler := routes(synthesis.NewManager(nil), settings.Open())
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
	)
	request := httptest.NewRequest(http.MethodGet, "http://evil.example:8787/", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
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
