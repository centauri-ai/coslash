package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type hostTestCredentials struct{ value string }

func (s *hostTestCredentials) Load(context.Context) (string, error) { return s.value, nil }
func (s *hostTestCredentials) Save(_ context.Context, value string) error {
	s.value = value
	return nil
}

func TestHostPairStoresOwnCredentialWithoutPrintingIt(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/device-authorizations":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"pair-1","deviceCode":"private-code","userCode":"ABCD-EFGH","verificationUri":%q,"expiresAt":%q,"intervalSeconds":1}`, server.URL+"/pair", time.Now().Add(time.Minute).Format(time.RFC3339))
		case "/v1/device-authorizations/token":
			fmt.Fprint(w, `{"deviceId":"host-device","credential":"private-credential","tokenType":"Device","scope":"ingest"}`)
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	credential := &hostTestCredentials{}
	hub := &hubclient.Client{BaseURL: base, Credentials: credential, DeviceName: "test-host"}
	var output bytes.Buffer
	if err := pairHost(t.Context(), hub, &output); err != nil {
		t.Fatal(err)
	}
	if got, err := credential.Load(context.Background()); err != nil || got != "private-credential" {
		t.Fatalf("stored credential = %q, %v", got, err)
	}
	if !strings.Contains(output.String(), "ABCD-EFGH") || strings.Contains(output.String(), "private-code") || strings.Contains(output.String(), "private-credential") {
		t.Fatalf("pairing output exposed a secret or omitted the user code: %q", output.String())
	}
}

func TestHostProcessLockPreventsConcurrentQueueWriters(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	first, err := acquireHostLock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireHostLock(); err == nil {
		second.Close()
		t.Fatal("a second host agent acquired the queue lock")
	}
}

func TestHostReportsOnlySupportedFoundAgents(t *testing.T) {
	if got := supportedHostAgents([]*session.Session{{Agent: vendors.AgentClaude}}); len(got) != 0 {
		t.Fatalf("unsupported host agent reported found: %v", got)
	}
	if got := supportedHostAgents([]*session.Session{{Agent: vendors.AgentCodex}}); len(got) != 1 || got[0] != vendors.AgentCodex {
		t.Fatalf("Codex host agent omitted: %v", got)
	}
}
