package main

import (
	"encoding/base64"
	"encoding/json"
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
	"github.com/centauri-ai/coslash/collector/internal/session"
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
		{method: http.MethodPost, path: "/api/diff"},
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

func TestHandleDiffReturnsRecordedEditsInOrder(t *testing.T) {
	edits := session.NewFileEditSet()
	edits.Add("file.txt", 1, 1, false)
	edits.Change("file.txt", "before\n", "middle\n")
	edits.Add("file.txt", 1, 1, false)
	edits.Change("file.txt", "middle\n", "after\n")
	found := &session.Session{
		ID: "session-1",
		SessionDetails: session.SessionDetails{
			FileEdits: edits.Edits,
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/diff?id=session-1&path=file.txt", nil)
	response := httptest.NewRecorder()

	handleDiff(response, request, func(id string) (*session.Session, error) {
		if id != found.ID {
			t.Fatalf("session id = %q, want %q", id, found.ID)
		}
		return found, nil
	})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Changes []struct {
			Kind      string `json:"kind"`
			Text      string `json:"text"`
			Operation string `json:"operation"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Changes) != 2 {
		t.Fatalf("changes = %#v, want two recorded edits", body.Changes)
	}
	if body.Changes[0].Operation != "Edit" ||
		body.Changes[0].Additions != 1 || body.Changes[0].Deletions != 1 ||
		!strings.Contains(body.Changes[0].Text, "-before\n+middle") ||
		!strings.Contains(body.Changes[1].Text, "-middle\n+after") {
		t.Fatalf("changes = %#v, want recorded edits in transcript order", body.Changes)
	}
}

func TestHandleDiffRejectsFilesOutsideTheSession(t *testing.T) {
	found := &session.Session{
		ID:               "session-1",
		WorkingDirectory: t.TempDir(),
		SessionDetails: session.SessionDetails{
			FileEdits: []session.FileEdit{{Path: "recorded.txt"}},
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/diff?id=session-1&path=../secret.txt", nil)
	response := httptest.NewRecorder()

	handleDiff(response, request, func(string) (*session.Session, error) { return found, nil })

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
