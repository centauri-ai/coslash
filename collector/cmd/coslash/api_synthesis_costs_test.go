package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

type integratedCostRunner struct {
	calls chan int
	next  int
}

func (*integratedCostRunner) VendorName() string { return "codex" }
func (*integratedCostRunner) ModelName() string  { return "gpt-4o" }
func (r *integratedCostRunner) Run(context.Context, string) (synthesis.RunResult, error) {
	r.next++
	cost := int64(r.next * 10)
	r.calls <- r.next
	result := synthesis.RunResult{
		Synthesis: session.SessionSynthesis{Outcome: "summary"},
		Usage:     synthesis.UsageReport{ReportedCostMicroUSD: &cost, Coverage: "complete"},
	}
	if r.next == 2 {
		return result, errors.New("paid failure")
	}
	return result, nil
}

func TestSynthesisCostsIntegratedManagerRoutesAndRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := synthesis.OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	runner := &integratedCostRunner{calls: make(chan int, 3)}
	current := &session.Session{Agent: "codex", ID: "integrated", SessionDetails: session.SessionDetails{Turns: 6}}
	revision, err := session.LocalDetailRevision(*current)
	if err != nil {
		t.Fatal(err)
	}
	oldList := listSessions
	t.Cleanup(func() { listSessions = oldList })
	listSessions = func(context.Context, int64) ([]*session.Session, error) { return nil, nil }

	manager := synthesis.NewManager(runner, store)
	handler := newServer(httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}, manager,
		reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil).Handler
	get := func(path string) (int, synthesis.CostResponse) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787"+path, nil)
		request.Header.Set("X-Coslash-Token", "secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var costs synthesis.CostResponse
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &costs); err != nil {
				t.Fatal(err)
			}
		}
		return response.Code, costs
	}
	waitForVersion := func(want string) {
		t.Helper()
		<-runner.calls
		for manager.AccountingVersion() != want {
			runtime.Gosched()
		}
	}
	if !manager.Ensure(current, 42) {
		t.Fatal("first round not started")
	}
	waitForVersion("1")
	if summary := manager.Lookup("codex", "integrated", 42); summary == nil || summary.Outcome != "summary" {
		t.Fatalf("first summary = %+v", summary)
	}
	if !manager.Ensure(current, 43) {
		t.Fatal("paid failure round not started")
	}
	waitForVersion("2")
	if manager.Lookup("codex", "integrated", 42) == nil || manager.Lookup("codex", "integrated", 43) != nil {
		t.Fatal("paid failure replaced the last valid summary")
	}
	manager.Shutdown()

	manager = synthesis.NewManager(runner, store)
	handler = newServer(httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}, manager,
		reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil).Handler
	if !manager.Ensure(current, 43) {
		t.Fatal("retry round not started")
	}
	waitForVersion("1")
	manager.Shutdown()
	if summary := manager.Lookup("codex", "integrated", 43); summary == nil || summary.Outcome != "summary" {
		t.Fatalf("retry summary = %+v", summary)
	}
	if code, costs := get("/api/synthesis-costs?source=local&since=1&until=9007199254740991"); code != 200 ||
		costs.Totals.RoundCount != 3 || costs.Totals.InvocationCount != 3 || costs.Totals.KnownCostMicroUSD == nil || *costs.Totals.KnownCostMicroUSD != 60 {
		t.Fatalf("monthly costs = %d %+v", code, costs)
	}
	code, costs := get("/api/synthesis-costs?source=local&agent=codex&id=integrated")
	if code != 200 ||
		costs.Totals.RoundCount != 3 || len(costs.Rounds) != 3 || costs.Totals.KnownCostMicroUSD == nil || *costs.Totals.KnownCostMicroUSD != 60 {
		t.Fatalf("session costs = %d %+v", code, costs)
	}
	outcomes := map[string]int{}
	for _, round := range costs.Rounds {
		outcomes[round.Outcome]++
		if round.Agent != "codex" || round.SessionID != "integrated" || len(round.VendorModels) != 1 || round.VendorModels[0].Vendor != "codex" {
			t.Fatalf("round attribution = %+v", round)
		}
	}
	if outcomes["success"] != 2 || outcomes["failed"] != 1 {
		t.Fatalf("round outcomes = %+v", outcomes)
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1:8787/api/synthesis-costs?source=local&agent=codex&id=integrated", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unguarded costs status = %d", unauthorized.Code)
	}
	if got, err := session.LocalDetailRevision(*current); err != nil || got != revision {
		t.Fatalf("portable revision changed: %q to %q (%v)", revision, got, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := synthesis.OpenAccountingStore(home, 2)
	if err != nil {
		t.Fatal(err)
	}
	manager = synthesis.NewManager(nil, reopened)
	handler = newServer(httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}, manager,
		reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil).Handler
	if code, costs := get("/api/synthesis-costs?source=local&agent=codex&id=integrated"); code != 200 ||
		costs.Totals.RoundCount != 3 || costs.Totals.KnownCostMicroUSD == nil || *costs.Totals.KnownCostMicroUSD != 60 {
		t.Fatalf("reopened costs = %d %+v", code, costs)
	}
	if code, costs := get("/api/synthesis-costs?source=local&since=1&until=9007199254740991"); code != 200 ||
		costs.Totals.InvocationCount != 3 || costs.Totals.KnownCostMicroUSD == nil || *costs.Totals.KnownCostMicroUSD != 60 {
		t.Fatalf("reopened monthly costs = %d %+v", code, costs)
	}
	if summary := manager.Lookup("codex", "integrated", 43); summary == nil || summary.Outcome != "summary" {
		t.Fatalf("reopened summary = %+v", summary)
	}
	if err := reopened.MarkIncomplete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, _ := get("/api/synthesis-costs?source=local&agent=codex&id=integrated"); code != http.StatusServiceUnavailable {
		t.Fatalf("incomplete accounting status = %d", code)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err = synthesis.OpenAccountingStore(home, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	manager = synthesis.NewManager(nil, reopened)
	handler = newServer(httpsec.Guard{Addr: "127.0.0.1:8787", Token: "secret"}, manager,
		reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil).Handler
	if code, _ := get("/api/synthesis-costs?source=local&since=1&until=9007199254740991"); code != http.StatusServiceUnavailable {
		t.Fatalf("restarted incomplete accounting status = %d", code)
	}
	if manager.Lookup("codex", "integrated", 43) == nil {
		t.Fatal("accounting failure hid cached summary")
	}
}

func TestSynthesisCostsAPIMonthAndIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := synthesis.OpenAccountingStore(home, 900)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	write := func(id, agent, sessionID, vendor string, at int64, cost int64) {
		t.Helper()
		if err := store.BeginRound(ctx, synthesis.Round{ID: id, SourceID: "local", Agent: agent, SessionID: sessionID, SourceRevision: 1, StartedAtMs: at - 1}); err != nil {
			t.Fatal(err)
		}
		if err := store.StartInvocation(ctx, id, 0, "source", vendor, "model", at); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishInvocation(ctx, id, 0, at+1, "success", synthesis.UsageReport{ReportedCostMicroUSD: &cost, Coverage: "complete"}); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishRound(ctx, id, at+2, "success"); err != nil {
			t.Fatal(err)
		}
	}
	write("start", "claude", "shared", "codex", 1000, 10)
	write("inside", "claude", "shared", "cursor", 1499, 20)
	write("end", "codex", "shared", "claude", 1500, 30)
	write("pi", "pi", "SDK/opaque identity", "pi", 2000, 5)
	if err := store.BeginRound(ctx, synthesis.Round{ID: "spanning", SourceID: "local", Agent: "cursor", SessionID: "outside-month", SourceRevision: 700, StartedAtMs: 950}); err != nil {
		t.Fatal(err)
	}
	for ordinal, use := range []struct{ at, cost int64 }{{999, 40}, {1000, 50}} {
		if err := store.StartInvocation(ctx, "spanning", ordinal, "source", "opencode", "model", use.at); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishInvocation(ctx, "spanning", ordinal, use.at+1, "success", synthesis.UsageReport{ReportedCostMicroUSD: &use.cost, Coverage: "complete"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.FinishRound(ctx, "spanning", 1501, "success"); err != nil {
		t.Fatal(err)
	}
	manager := synthesis.NewManager(nil, store)
	get := func(path string) (int, synthesis.CostResponse) {
		t.Helper()
		response := httptest.NewRecorder()
		handleSynthesisCosts(response, httptest.NewRequest(http.MethodGet, path, nil), manager)
		var value synthesis.CostResponse
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
		}
		return response.Code, value
	}
	if code, got := get("/api/synthesis-costs?source=local&since=1000&until=1500"); code != 200 || got.Totals.InvocationCount != 3 || got.Totals.KnownCostMicroUSD == nil || *got.Totals.KnownCostMicroUSD != 80 || len(got.Rounds) != 0 || len(got.ByVendor) != 3 {
		t.Fatalf("monthly: %d %+v", code, got)
	}
	if code, got := get("/api/synthesis-costs?source=local&agent=claude&id=shared"); code != 200 || got.Totals.RoundCount != 2 || got.Totals.KnownCostMicroUSD == nil || *got.Totals.KnownCostMicroUSD != 30 {
		t.Fatalf("claude identity: %d %+v", code, got)
	}
	if code, got := get("/api/synthesis-costs?source=local&agent=codex&id=shared"); code != 200 || got.Totals.RoundCount != 1 || *got.Totals.KnownCostMicroUSD != 30 {
		t.Fatalf("codex identity: %d %+v", code, got)
	}
	if code, got := get("/api/synthesis-costs?source=local&agent=cursor&id=outside-month"); code != 200 || got.Totals.RoundCount != 1 || *got.Totals.KnownCostMicroUSD != 90 {
		t.Fatalf("outside-month session: %d %+v", code, got)
	}
	if code, got := get("/api/synthesis-costs?source=local&agent=pi&id=SDK%2Fopaque%20identity"); code != 200 || got.Totals.KnownCostMicroUSD == nil || *got.Totals.KnownCostMicroUSD != 5 {
		t.Fatalf("Pi identity: %d %+v", code, got)
	}
	for _, path := range []string{
		"/api/synthesis-costs?source=remote&since=1000&until=1500",
		"/api/synthesis-costs?source=local&agent=codex&id=SDK%2Fopaque%20identity",
		"/api/synthesis-costs?source=local&agent=pi&id=bad%00identity",
		"/api/synthesis-costs?source=local&since=1500&until=1000",
		"/api/synthesis-costs?source=local&since=1000&until=1500&id=shared",
		"/api/synthesis-costs?source=local&agent=claude&id=shared&cursor=bad",
		"/api/synthesis-costs?source=local&agent=claude&id=shared&limit=51",
		"/api/synthesis-costs?source=local&agent=claude&id=shared&since=1000&until=1500",
		"/api/synthesis-costs?source=local&since=1000&since=1001&until=1500",
	} {
		if code, _ := get(path); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, code)
		}
	}
	write("tie-a", "claude", "shared", "codex", 1000, 1)
	write("tie-b", "claude", "shared", "codex", 1000, 1)
	cursor := ""
	ids := map[string]bool{}
	for {
		path := "/api/synthesis-costs?source=local&agent=claude&id=shared&limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		code, page := get(path)
		if code != 200 || page.Totals.RoundCount != 4 || len(page.Rounds) == 0 || len(page.Rounds) > 2 {
			t.Fatalf("page: %d %+v", code, page)
		}
		for _, round := range page.Rounds {
			if ids[round.ID] {
				t.Fatalf("duplicate round %s", round.ID)
			}
			ids[round.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(ids) != 4 {
		t.Fatalf("paginated IDs = %+v", ids)
	}
}

func TestSynthesisCostsAPIUsesLegacySummaryAndReportsStorageFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := synthesis.OpenAccountingStore(home, 2000)
	if err != nil {
		t.Fatal(err)
	}
	manager := synthesis.NewManager(nil, store)
	cache := synthesis.NewCache()
	if err := cache.Store("claude", "legacy", synthesis.Record{Revision: 1, GeneratedAt: 1000, Synthesis: session.SessionSynthesis{Outcome: "old"}}); err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handleSynthesisCosts(response, httptest.NewRequest(http.MethodGet, path, nil), manager)
		return response
	}
	response := get("/api/synthesis-costs?source=local&agent=claude&id=legacy")
	var result synthesis.CostResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.HistoricalUnknown {
		t.Fatalf("legacy: %d %s", response.Code, response.Body.String())
	}
	response = get("/api/synthesis-costs?source=local&since=1000&until=3000")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.HistoricalUnknown {
		t.Fatalf("pretracking month: %d %s", response.Code, response.Body.String())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	response = get("/api/synthesis-costs?source=local&since=2000&until=3000")
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "sqlite") {
		t.Fatalf("closed store: %d %s", response.Code, response.Body.String())
	}
}

type apiCostRunner struct {
	calls   int
	started chan struct{}
}

func (r *apiCostRunner) VendorName() string { return "codex" }
func (r *apiCostRunner) ModelName() string  { return "gpt-4o" }
func (r *apiCostRunner) Run(context.Context, string) (synthesis.RunResult, error) {
	r.calls++
	close(r.started)
	cost := int64(12)
	return synthesis.RunResult{Usage: synthesis.UsageReport{ReportedCostMicroUSD: &cost, Coverage: "complete"}}, context.Canceled
}

func TestSynthesisCostVersionHeaderAfterPaidFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := synthesis.OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := &apiCostRunner{started: make(chan struct{})}
	manager := synthesis.NewManager(runner, store)
	oldList := listSessions
	t.Cleanup(func() { listSessions = oldList })
	listSessions = func(context.Context, int64) ([]*session.Session, error) { return nil, nil }
	handler := routes(manager, reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil)
	getVersion := func() string {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
		if response.Code != 200 {
			t.Fatalf("sessions status %d: %s", response.Code, response.Body.String())
		}
		return response.Header().Get("X-Coslash-Synthesis-Cost-Version")
	}
	if value := getVersion(); value != "0" {
		t.Fatalf("initial version = %q", value)
	}
	if !manager.Ensure(&session.Session{Agent: "codex", ID: "paid-failure", SessionDetails: session.SessionDetails{Turns: 6}}, 42) {
		t.Fatal("round not started")
	}
	<-runner.started
	manager.Shutdown()
	if value := getVersion(); value != "1" {
		t.Fatalf("final version = %q", value)
	}
	if runner.calls != 1 {
		t.Fatalf("GET called runner %d times", runner.calls)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/synthesis-costs?source=local&agent=codex&id=paid-failure", nil))
	if response.Code != 200 {
		t.Fatalf("cost GET = %d %s", response.Code, response.Body.String())
	}
}
