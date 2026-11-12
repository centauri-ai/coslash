package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

const wiringTimeout = 10 * time.Second

// receiptHub records the credential of every update report and answers each one
// with respond, so a test can hold a report in flight.
type receiptHub struct {
	*httptest.Server
	mu       sync.Mutex
	auth     []string
	entered  chan struct{}
	received atomic.Int32
}

func newReceiptHub(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) *receiptHub {
	t.Helper()
	hub := &receiptHub{entered: make(chan struct{}, 16)}
	hub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.mu.Lock()
		hub.auth = append(hub.auth, r.Header.Get("Authorization"))
		hub.mu.Unlock()
		hub.received.Add(1)
		hub.entered <- struct{}{}
		respond(w, r)
	}))
	t.Cleanup(hub.Close)
	return hub
}

func acknowledge(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"active":true}`)) }

func (h *receiptHub) credentials() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.auth...)
}

func (h *receiptHub) waitForRequest(t *testing.T) {
	t.Helper()
	select {
	case <-h.entered:
	case <-time.After(wiringTimeout):
		t.Fatal("the Hub never received a report")
	}
}

// fileStoreClient is a client on the Hub whose credential lives in a real file store.
func fileStoreClient(t *testing.T, hubURL, credential string) (*hubclient.Client, hubclient.FileCredentialStore) {
	t.Helper()
	store := hubclient.FileCredentialStore{Path: filepath.Join(t.TempDir(), "credential")}
	if err := store.Save(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	return &hubclient.Client{BaseURL: mustParseURL(t, hubURL), Credentials: store}, store
}

// reportingController is a real controller whose loop is only the production
// reporter wiring, so everything a test observes comes from the code main runs.
func reportingController(t *testing.T) *syncController {
	t.Helper()
	t.Setenv("COSLASH_HOME", t.TempDir())
	var controller *syncController
	controller, err := newSyncController(func(ctx context.Context, queue *syncv4.Queue, client *hubclient.Client, _, _ chan struct{}) {
		controller.runWithUpdateReporter(ctx, "1.2.4", queue, client, func() { <-ctx.Done() })
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.Stop)
	return controller
}

// Stopping the loop must not return, and so Ensure must not rebind the queue,
// while the shutdown flush is still talking to the Hub. The flush goes out as the
// binding that owns the attempt, although the replacement client is on the same
// Hub, and nothing is sent once the queue belongs to the new binding.
func TestRebindWaitsForTheReporterFlushAndSendsAsTheOldBinding(t *testing.T) {
	release := make(chan struct{})
	hub := newReceiptHub(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		acknowledge(w, r)
	})
	controller := reportingController(t)
	first, _ := fileStoreClient(t, hub.URL, "cred-one")
	second, _ := fileStoreClient(t, hub.URL, "cred-two")
	if err := controller.Ensure(first); err != nil {
		t.Fatal(err)
	}
	failLegacyUpdate(t, controller.Queue())
	rebound := make(chan error, 1)
	go func() { rebound <- controller.Ensure(second) }()
	hub.waitForRequest(t)
	select {
	case err := <-rebound:
		t.Fatalf("the controller rebound while the flush was in flight (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	if controller.Queue().PendingUpdate() == nil {
		t.Fatal("the attempt was dropped while its flush was in flight")
	}
	close(release)
	select {
	case err := <-rebound:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(wiringTimeout):
		t.Fatal("the rebind never finished")
	}
	if controller.Queue().PendingUpdate() != nil {
		t.Fatal("the old binding's attempt survived the rebind")
	}
	controller.Stop()
	if got := hub.credentials(); len(got) != 1 || got[0] != "Device cred-one" {
		t.Fatalf("Hub saw %v, want one report as the old binding", got)
	}
}

// A same-origin re-pair replaces the credential under the same store before the
// old loop is stopped. The flush for the old attempt must not be sent as the new
// account, and an unchanged credential must still be used.
func TestReporterNeverSendsAnAttemptAsAnotherAccountOnTheSameOrigin(t *testing.T) {
	for _, test := range []struct {
		name    string
		repair  bool
		wantAny []string
	}{
		{"unchanged credential", false, []string{"Device cred-one"}},
		{"credential replaced before the stop", true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := newReceiptHub(t, acknowledge)
			controller := reportingController(t)
			client, store := fileStoreClient(t, hub.URL, "cred-one")
			if err := controller.Ensure(client); err != nil {
				t.Fatal(err)
			}
			failLegacyUpdate(t, controller.Queue())
			if test.repair {
				if err := store.Save(context.Background(), "cred-two"); err != nil {
					t.Fatal(err)
				}
			}
			controller.Stop()
			got := hub.credentials()
			if len(got) != len(test.wantAny) || (len(got) == 1 && got[0] != test.wantAny[0]) {
				t.Fatalf("Hub saw %v, want %v", got, test.wantAny)
			}
		})
	}
}

// The Hub keeps a legacy-bridge attempt active after it accepted the failure, so
// the attempt stays pending. Once this reporter has had the failure accepted,
// stopping must neither repeat it nor wait on the Hub.
func TestShutdownDoesNotRepeatAFailureTheHubAlreadyAccepted(t *testing.T) {
	hub := newReceiptHub(t, acknowledge)
	controller := reportingController(t)
	client, _ := fileStoreClient(t, hub.URL, "cred-one")
	if err := controller.Ensure(client); err != nil {
		t.Fatal(err)
	}
	failLegacyUpdate(t, controller.Queue())
	controller.Queue().SignalUpdate()
	hub.waitForRequest(t)
	// A second wake is only picked up after the first report was fully handled,
	// so its request proves the acceptance has been recorded.
	controller.Queue().SignalUpdate()
	hub.waitForRequest(t)
	controller.Stop()
	if got := hub.received.Load(); got != 2 {
		t.Fatalf("the Hub received %d reports, want the 2 sent while running and none from the stop", got)
	}
	if controller.Queue().PendingUpdate() == nil {
		t.Fatal("the attempt must stay pending until the Hub stops reporting it active")
	}
}

type recordingReporter struct {
	calls    atomic.Int32
	failWith error
	reported chan struct{}
}

func (r *recordingReporter) ReportUpdate(context.Context) error {
	r.calls.Add(1)
	if r.reported != nil {
		select {
		case r.reported <- struct{}{}:
		default:
		}
	}
	return r.failWith
}

// runReporterThrough starts the reporter, lets one wake-up report complete, runs
// between, and shuts the reporter down. It returns the reports sent.
func runReporterThrough(t *testing.T, queue *syncv4.Queue, reporter *recordingReporter, between func()) int32 {
	t.Helper()
	reporter.reported = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runUpdateReporter(ctx, reporter, queue) }()
	queue.SignalUpdate()
	select {
	case <-reporter.reported:
	case <-time.After(wiringTimeout):
		t.Fatal("the reporter never reported")
	}
	if between != nil {
		between()
	}
	cancel()
	select {
	case <-done:
	case <-time.After(wiringTimeout):
		t.Fatal("the reporter never stopped")
	}
	return reporter.calls.Load()
}

func TestShutdownFlushSkipsOnlyWhatTheHubAcceptedFromThisReporter(t *testing.T) {
	t.Run("accepted failure", func(t *testing.T) {
		if calls := runReporterThrough(t, failedLegacyQueue(t), &recordingReporter{}, nil); calls != 1 {
			t.Fatalf("calls = %d, want 1: the stop repeated an accepted failure", calls)
		}
	})
	t.Run("failure the Hub did not accept", func(t *testing.T) {
		if calls := runReporterThrough(t, failedLegacyQueue(t), &recordingReporter{failWith: errors.New("hub down")}, nil); calls != 2 {
			t.Fatalf("calls = %d, want the failed report and the flush", calls)
		}
	})
	t.Run("another cause after the acceptance", func(t *testing.T) {
		queue := failedLegacyQueue(t)
		calls := runReporterThrough(t, queue, &recordingReporter{}, func() {
			if err := queue.SetUpdateStage(fixtureUpdateID, "failed", "update_unavailable"); err != nil {
				t.Fatal(err)
			}
		})
		if calls != 2 {
			t.Fatalf("calls = %d, want the accepted cause and the flush of the new one", calls)
		}
	})
	t.Run("nothing pending", func(t *testing.T) {
		queue, err := syncv4.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		reporter := &recordingReporter{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); runUpdateReporter(ctx, reporter, queue) }()
		cancel()
		<-done
		if reporter.calls.Load() != 0 {
			t.Fatalf("calls = %d, want 0", reporter.calls.Load())
		}
	})
}

// honoringStore is a credential store that honors its context, as the keychain
// does, except for the first read: the request was sent before its deadline
// passed, and only the reply came late.
type honoringStore struct {
	mu      sync.Mutex
	loads   int
	deleted bool
}

func (s *honoringStore) Load(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loads > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	s.loads++
	if s.deleted {
		return "", hubclient.ErrNotPaired
	}
	return "cred", nil
}
func (*honoringStore) Save(context.Context, string) error { return nil }
func (*honoringStore) Delete(context.Context) error       { return nil }
func (s *honoringStore) DeleteIfMatches(ctx context.Context, _ string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = true
	return true, nil
}

// A revoked reply that lands after the request's deadline must still remove the
// dead credential, as it did before the flush existed; only the shutdown flush,
// whose caller waits for the cleanup, gives up at its deadline.
func TestRevokedCleanupOutlivesAnOrdinaryDeadlineButNotTheFlushBound(t *testing.T) {
	for _, test := range []struct {
		name        string
		mark        bool
		wantDeleted bool
	}{{"ordinary report", false, true}, {"shutdown flush", true, false}} {
		t.Run(test.name, func(t *testing.T) {
			store := &honoringStore{}
			client := &hubclient.Client{BaseURL: mustParseURL(t, "https://hub.example.test"), Credentials: store}
			controller := &syncController{client: client, binding: "binding", connected: true}
			worker := observedSyncWorker{runner: revokedRunner{}, controller: controller, client: client, binding: "binding"}
			ctx := context.Background()
			if test.mark {
				ctx = hubclient.WithBoundedCleanup(ctx)
			}
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			_ = worker.ReportUpdate(ctx)
			store.mu.Lock()
			deleted := store.deleted
			store.mu.Unlock()
			if deleted != test.wantDeleted {
				t.Fatalf("credential deleted = %v, want %v", deleted, test.wantDeleted)
			}
			if revoked := controller.Status().State == "disconnected"; revoked != test.wantDeleted {
				t.Fatalf("controller marked revoked = %v, want %v", revoked, test.wantDeleted)
			}
		})
	}
}

// Startup must use the shared loop, which runs primary update reporting.
func TestMainRunsTheSyncLoopThroughTheUpdateReporterWiring(t *testing.T) {
	for _, test := range []struct{ file, want string }{
		{"main.go", "runHubSyncLoop"},
		{"sync_controller.go", "runWithUpdateReporter"},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), test.file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if name == test.want {
				calls++
			}
			if test.file == "main.go" && (name == "runUpdateReporter" || name == "updateReporter") {
				t.Fatal("main.go bypasses the shared update reporter")
			}
			return true
		})
		if calls != 1 {
			t.Fatalf("%s calls %s %d times, want 1", test.file, test.want, calls)
		}
	}
}
