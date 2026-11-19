package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	searchpkg "github.com/centauri-ai/coslash/collector/internal/search"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

func testSearchIndex(t *testing.T) *searchpkg.Index {
	t.Helper()
	index, err := searchpkg.Open(filepath.Join(t.TempDir(), "search", "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return index
}

func TestSearchRoutesGuardReadinessAndValidation(t *testing.T) {
	index := testSearchIndex(t)
	service := &searchService{index: index}
	mux := routesWithOnboarding(nil, nil, nil, nil, nil, nil, serverServices{search: service})
	handler := httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}.Wrap(mux)
	for _, test := range []struct {
		path, token string
		code        int
	}{
		{"/api/search?q=queue", "", http.StatusUnauthorized},
		{"/api/search/status", "", http.StatusUnauthorized},
		{"/api/search?q=queue", "secret", http.StatusServiceUnavailable},
		{"/api/search?q=repo:coslash", "secret", http.StatusBadRequest},
		{"/api/search?q=queue&limit=0", "secret", http.StatusBadRequest},
		{"/api/search/status?source=local", "secret", http.StatusBadRequest},
		{"/api/search/status", "secret", http.StatusOK},
	} {
		t.Run(test.path+test.token, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787"+test.path, nil)
			request.Header.Set("X-Coslash-Token", test.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.code {
				t.Fatalf("status %d want %d: %s", response.Code, test.code, response.Body.String())
			}
		})
	}
}

func TestSearchRefreshInterruptedPassRetainsRows(t *testing.T) {
	index := testSearchIndex(t)
	old := listSessions
	t.Cleanup(func() { listSessions = old })
	value := &session.Session{Agent: "claude", ID: "kept", SessionDetails: session.SessionDetails{Digest: []session.DigestEntry{{Category: session.DigestUser, Description: "merge queue", SearchText: "merge queue"}}}}
	listSessions = func(context.Context, int64) ([]*session.Session, error) { return []*session.Session{value}, nil }
	if err := refreshSearchIndex(context.Background(), index, nil, nil); err != nil {
		t.Fatal(err)
	}
	listSessions = func(context.Context, int64) ([]*session.Session, error) {
		return nil, errors.New("interrupted inventory")
	}
	if err := refreshSearchIndex(context.Background(), index, nil, nil); err == nil {
		t.Fatal("expected failure")
	}
	status, err := index.Status(context.Background(), &searchpkg.Identity{SourceID: "local", Agent: "claude", SessionID: "kept"})
	if err != nil || status.Coverage == "absent" {
		t.Fatalf("lost last-good rows: %+v %v", status, err)
	}
	listSessions = func(context.Context, int64) ([]*session.Session, error) { return nil, nil }
	if err := refreshSearchIndex(context.Background(), index, nil, nil); err != nil {
		t.Fatal(err)
	}
	status, err = index.Status(context.Background(), &searchpkg.Identity{SourceID: "local", Agent: "claude", SessionID: "kept"})
	if err != nil || status.Coverage != "absent" {
		t.Fatalf("complete deletion: %+v %v", status, err)
	}
}

func TestSearchCLIPath(t *testing.T) {
	path, err := searchRequestPath([]string{"why queue repo:coslash agent:claude since:2026-01-01", "--limit", "3", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(path)
	q := parsed.Query()
	if q.Get("repo") != "coslash" || q.Get("agent") != "claude" || q.Get("limit") != "3" || q.Get("since") == "" {
		t.Fatalf("filters: %s", path)
	}
	path, err = searchRequestPath([]string{"--status", "local/claude/session", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = url.Parse(path)
	if parsed.Path != "/api/search/status" || parsed.Query().Get("session") != "session" {
		t.Fatal(path)
	}
	for _, args := range [][]string{{"queue machine:ssh", "--json"}, {"repo:coslash", "--json"}, {"queue", "--limit", "0", "--json"}, {"--status", "local/id", "--json"}, {"--status", "--limit", "2", "--json"}} {
		if _, err := searchRequestPath(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestSearchAPIFiltersBeforeLimit(t *testing.T) {
	index := testSearchIndex(t)
	for _, repo := range []string{"excluded", "included"} {
		value := session.Session{ID: repo, Agent: "claude", Repository: &repo, SessionDetails: session.SessionDetails{Digest: []session.DigestEntry{{Category: session.DigestUser, Description: "merge queue", SearchText: "merge queue"}}}}
		if err := index.Upsert(context.Background(), "local", value, "full"); err != nil {
			t.Fatal(err)
		}
	}
	index.SetState("ready", 2)
	response := httptest.NewRecorder()
	handleSearch(response, httptest.NewRequest(http.MethodGet, "/api/search?q=queue&repo=included&limit=1", nil), &searchService{index: index})
	var result searchpkg.Response
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].SessionID != "included" {
		t.Fatalf("response %s", response.Body.String())
	}
}

func TestSearchCLIRoundTrip(t *testing.T) {
	index := testSearchIndex(t)
	value := session.Session{ID: "answer", Agent: "claude", SessionDetails: session.SessionDetails{Synthesis: &session.SessionSynthesis{KeyDecisions: []string{"merge queue commits"}}}}
	if err := index.Upsert(context.Background(), "local", value, "full"); err != nil {
		t.Fatal(err)
	}
	index.SetState("ready", 1)
	mux := http.NewServeMux()
	service := &searchService{index: index}
	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) { handleSearch(w, r, service) })
	mux.HandleFunc("GET /api/search/status", func(w http.ResponseWriter, r *http.Request) { handleSearchStatus(w, r, service) })
	server := httptest.NewUnstartedServer(nil)
	guard := httpsec.Guard{Addr: server.Listener.Addr().String(), Token: "search-test-token"}
	server.Config.Handler = guard.Wrap(mux)
	server.Start()
	defer server.Close()
	writeTestRuntime(t, server.URL, "search-test-token")
	var output bytes.Buffer
	if err := runSearch(&output, []string{"why merge queue", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result searchpkg.Response
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Decisions) != 1 || result.Decisions[0].Selector != "claude:answer" || result.Decisions[0].Cited || len(result.Decisions[0].Turns) != 0 || len(result.Sessions) != 1 {
		t.Fatalf("%s", output.String())
	}
	output.Reset()
	if err := runSearch(&output, []string{"--status", "local/claude/answer", "--json"}); err != nil {
		t.Fatal(err)
	}
	var status searchpkg.Status
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "ready" || status.Coverage != "full" {
		t.Fatalf("%s", output.String())
	}
	index.SetState("building", 1)
	output.Reset()
	if err := runSearch(&output, []string{"merge queue", "--json"}); err == nil || !strings.Contains(err.Error(), "building") || output.Len() != 0 {
		t.Fatalf("building: output %q, err %v", output.String(), err)
	}
}

func TestSearchQueryBoundAndUnavailableCoverage(t *testing.T) {
	response := httptest.NewRecorder()
	handleSearch(response, httptest.NewRequest(http.MethodGet, "/api/search?q="+strings.Repeat("a", 8193), nil), nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized query: %d", response.Code)
	}
	response = httptest.NewRecorder()
	handleSearchStatus(response, httptest.NewRequest(http.MethodGet, "/api/search/status?source=local&agent=claude&session=missing", nil), nil)
	var status searchpkg.Status
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "unavailable" || status.Coverage != "absent" {
		t.Fatalf("%+v", status)
	}
}
