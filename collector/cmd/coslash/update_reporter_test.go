package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

const fixtureUpdateID = "10000000-0000-4000-8000-000000000001"

type reporterFunc func(context.Context) error

func (f reporterFunc) ReportUpdate(ctx context.Context) error { return f(ctx) }

// slowReporter models a Hub round trip that is still in flight when shutdown
// cancels the sync context.
type slowReporter struct {
	started   chan struct{}
	delivered atomic.Int32
	calls     atomic.Int32
}

func (r *slowReporter) ReportUpdate(ctx context.Context) error {
	if r.calls.Add(1) == 1 {
		close(r.started)
		<-ctx.Done()
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.delivered.Add(1)
	return nil
}

func failLegacyUpdate(t *testing.T, queue *syncv4.Queue) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"attemptId": fixtureUpdateID, "targetVersion": "1.2.4", "expiresAt": time.Now().Add(time.Hour), "legacySettlement": true})
	if err := queue.AcceptUpdate(hubclient.V4Command{ID: fixtureUpdateID, Type: "update", Payload: payload}, "1.2.4", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetUpdateStage(fixtureUpdateID, "failed", "update_failed"); err != nil {
		t.Fatal(err)
	}
}

func failedLegacyQueue(t *testing.T) *syncv4.Queue {
	t.Helper()
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	failLegacyUpdate(t, queue)
	return queue
}

// A legacy rollback restarts into a binary that discards the failed attempt, so
// the failure receipt must be delivered before the reporter lets go.
func TestUpdateReporterDeliversFailureInterruptedByShutdown(t *testing.T) {
	queue := failedLegacyQueue(t)
	reporter := &slowReporter{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runUpdateReporter(ctx, reporter, queue) }()
	queue.SignalUpdate()
	<-reporter.started
	cancel()
	<-done
	if reporter.delivered.Load() != 1 {
		t.Fatalf("failure receipt was not delivered before shutdown completed (calls=%d)", reporter.calls.Load())
	}
}

func TestUpdateReporterShutdownSendsNothingWithoutAFailure(t *testing.T) {
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reporter := &slowReporter{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runUpdateReporter(ctx, reporter, queue) }()
	cancel()
	<-done
	if reporter.calls.Load() != 0 {
		t.Fatalf("idle shutdown reported %d times", reporter.calls.Load())
	}
}

// switchingCredentials returns first for the first switchAfter reads and second
// afterwards, like a same-origin re-pair landing between two reads.
type switchingCredentials struct {
	mu          sync.Mutex
	loads       int
	switchAfter int
	first       string
	second      string
}

func (s *switchingCredentials) Load(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if s.switchAfter >= 0 && s.loads > s.switchAfter {
		return s.second, nil
	}
	return s.first, nil
}
func (*switchingCredentials) Save(context.Context, string) error { return nil }
func (*switchingCredentials) Delete(context.Context) error       { return nil }

func (s *switchingCredentials) switchNow() {
	s.mu.Lock()
	s.switchAfter = s.loads
	s.mu.Unlock()
}

type receiptServer struct {
	*httptest.Server
	hits atomic.Int32
	auth atomic.Value
}

func newReceiptServer(t *testing.T) *receiptServer {
	t.Helper()
	server := &receiptServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.hits.Add(1)
		server.auth.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"active":true}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func pinnedFixture(t *testing.T, server *receiptServer, store *switchingCredentials) (*hubclient.Client, string) {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &hubclient.Client{BaseURL: base, Credentials: store}
	binding, err := client.V4Binding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return bindingPinnedClient(client, binding), binding
}

var failedReceipt = hubclient.V4CommandResult{CommandID: fixtureUpdateID, Result: "failed", Error: "update_failed"}

// An origin check cannot tell two accounts on the same Hub apart; the credential
// used for the request must itself belong to the binding that owns the receipt.
func TestPinnedReporterClientRefusesAnotherCredentialOnTheSameOrigin(t *testing.T) {
	server := newReceiptServer(t)
	store := &switchingCredentials{first: "cred-one", second: "cred-two", switchAfter: -1}
	pinned, _ := pinnedFixture(t, server, store)
	if _, err := pinned.V4ReportUpdate(context.Background(), failedReceipt); err != nil || server.hits.Load() != 1 || server.auth.Load() != "Device cred-one" {
		t.Fatalf("own credential: hits=%d auth=%v err=%v", server.hits.Load(), server.auth.Load(), err)
	}
	store.switchNow()
	if _, err := pinned.V4ReportUpdate(context.Background(), failedReceipt); !errors.Is(err, errHubBindingChanged) || server.hits.Load() != 1 {
		t.Fatalf("re-paired credential was used: hits=%d err=%v", server.hits.Load(), err)
	}
}

// Checking the binding and then letting the transport read the credential again
// leaves a window; the swap here lands between the check and the request.
func TestPinnedReporterClientIsNotFooledByASwapAfterAPreCheck(t *testing.T) {
	server := newReceiptServer(t)
	store := &switchingCredentials{first: "cred-one", second: "cred-two", switchAfter: -1}
	pinned, binding := pinnedFixture(t, server, store)
	store.mu.Lock()
	store.switchAfter = store.loads + 1 // the next read passes the check, the one after it is the new account's
	store.mu.Unlock()
	probe := &hubclient.Client{BaseURL: mustParseURL(t, server.URL), Credentials: store}
	if checked, err := probe.V4Binding(context.Background()); err != nil || checked != binding {
		t.Fatalf("pre-check: %q %v", checked, err)
	}
	if _, err := pinned.V4ReportUpdate(context.Background(), failedReceipt); !errors.Is(err, errHubBindingChanged) || server.hits.Load() != 0 {
		t.Fatalf("swapped credential reached the Hub: hits=%d err=%v", server.hits.Load(), err)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

type staticCredentials string

func (s staticCredentials) Load(context.Context) (string, error) { return string(s), nil }
func (staticCredentials) Save(context.Context, string) error     { return nil }
func (staticCredentials) Delete(context.Context) error           { return nil }

// A flush that outlives the rebind would finish against the new binding's queue.
// Even a reporter that is slower than the bound and ignores it must be over
// before Ensure moves the queue to the new binding.
func TestRebindWaitsForTheShutdownFlushBeforeAnythingIsRebound(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	previous := updateFlushTimeout
	updateFlushTimeout = 100 * time.Millisecond
	t.Cleanup(func() { updateFlushTimeout = previous })
	var calls, afterRebind atomic.Int32
	controller, err := newSyncController(func(ctx context.Context, queue *syncv4.Queue, _ *hubclient.Client, _, _ chan struct{}) {
		runUpdateReporter(ctx, reporterFunc(func(context.Context) error {
			calls.Add(1)
			time.Sleep(300 * time.Millisecond)
			if queue.PendingUpdate() == nil {
				afterRebind.Add(1)
			}
			return nil
		}), queue)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.Stop)
	base := mustParseURL(t, "https://hub.example.test")
	if err := controller.Ensure(&hubclient.Client{BaseURL: base, Credentials: staticCredentials("cred-one")}); err != nil {
		t.Fatal(err)
	}
	failLegacyUpdate(t, controller.Queue())
	if err := controller.Ensure(&hubclient.Client{BaseURL: base, Credentials: staticCredentials("cred-two")}); err != nil {
		t.Fatal(err)
	}
	if controller.Queue().PendingUpdate() != nil {
		t.Fatal("the old binding's attempt survived the rebind")
	}
	time.Sleep(500 * time.Millisecond)
	if afterRebind.Load() != 0 || calls.Load() != 1 {
		t.Fatalf("stale reporter work outlived the rebind: calls=%d afterRebind=%d", calls.Load(), afterRebind.Load())
	}
}

// hungKeychain answers the first read, then behaves like a stuck keychain that
// only gives up when its context does.
type hungKeychain struct{ loads atomic.Int32 }

func (k *hungKeychain) Load(ctx context.Context) (string, error) {
	if k.loads.Add(1) == 1 {
		return "cred", nil
	}
	<-ctx.Done()
	return "", ctx.Err()
}
func (*hungKeychain) Save(context.Context, string) error { return nil }
func (*hungKeychain) Delete(context.Context) error       { return nil }
func (*hungKeychain) DeleteIfMatches(ctx context.Context, _ string) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

type revokedRunner struct{}

func (revokedRunner) SyncOnce(context.Context) error { return nil }
func (revokedRunner) ReportUpdate(context.Context) error {
	return hubclient.V4Problem{Code: "device_revoked"}
}

// A revoked reply makes the real worker re-read and delete the credential. Those
// steps used to drop the caller's deadline, so one stuck keychain call could hold
// the shutdown for as long as it liked.
func TestShutdownFlushStaysBoundedWhenARevokedReplyHitsAHungKeychain(t *testing.T) {
	previous := updateFlushTimeout
	updateFlushTimeout = 100 * time.Millisecond
	t.Cleanup(func() { updateFlushTimeout = previous })
	queue := failedLegacyQueue(t)
	client := &hubclient.Client{BaseURL: mustParseURL(t, "https://hub.example.test"), Credentials: &hungKeychain{}}
	worker := observedSyncWorker{runner: revokedRunner{}, controller: &syncController{}, client: client, binding: "binding"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runUpdateReporter(ctx, worker, queue) }()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown flush outlived its bound")
	}
}
