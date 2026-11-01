package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type completionTestWorker struct {
	*syncv4.Runner
	passDone chan error
	bypasses atomic.Int32
}

type completionPollQueue struct {
	syncLoopQueue
	active atomic.Bool
	wake   chan struct{}
}

func (q *completionPollQueue) PendingUpdate() *syncv4.UpdateAttempt {
	if !q.active.Load() {
		return nil
	}
	return &syncv4.UpdateAttempt{ID: "update", Stage: "finalizing", HandoffReady: true}
}
func (q *completionPollQueue) CompletionWake() <-chan struct{} { return q.wake }
func (q *completionPollQueue) Results() []hubclient.V4CommandResult {
	if q.active.Load() {
		return []hubclient.V4CommandResult{{CommandID: "update", Result: "done"}}
	}
	return nil
}

type completionPollWorker struct {
	q        *completionPollQueue
	checks   atomic.Int32
	bypasses atomic.Int32
}

func (w *completionPollWorker) SyncOnce(context.Context) error { return syncv4.ErrPolicyBlocked }
func (w *completionPollWorker) CheckInForUpdate(context.Context) error {
	if w.checks.Add(1) == 1 {
		return errors.New("synthetic lost response")
	}
	w.q.active.Store(false)
	return syncv4.ErrPaused
}
func (w *completionPollWorker) BypassRetryBackoff() { w.bypasses.Add(1) }

func TestBlockedUpdateCompletionRetriesWithoutBypassingUploads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q := &completionPollQueue{wake: make(chan struct{}, 1)}
		q.active.Store(true)
		worker := &completionPollWorker{q: q}
		go runV4SyncLoopForTest(ctx, worker, q, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			<-ctx.Done()
			return hubclient.V4Wait{}, ctx.Err()
		})
		synctest.Wait()
		q.wake <- struct{}{}
		synctest.Wait()
		if worker.checks.Load() != 1 {
			t.Fatalf("initial checks=%d", worker.checks.Load())
		}
		time.Sleep(updateCompletionPoll)
		synctest.Wait()
		if worker.checks.Load() != 2 || worker.bypasses.Load() != 0 {
			t.Fatalf("checks=%d bypasses=%d", worker.checks.Load(), worker.bypasses.Load())
		}
	})
}

type oldHubCompletionWorker struct{ passes, checks atomic.Int32 }

func (w *oldHubCompletionWorker) SyncOnce(context.Context) error {
	w.passes.Add(1)
	return syncv4.ErrPaused
}
func (w *oldHubCompletionWorker) CheckInForUpdate(context.Context) error {
	w.checks.Add(1)
	return syncv4.ErrPaused
}

func TestPausedUpdateCompletionDoesNotSpinAgainstOlderHub(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q := &completionPollQueue{wake: make(chan struct{}, 1)}
		q.active.Store(true)
		worker := &oldHubCompletionWorker{}
		go runV4SyncLoopForTest(ctx, worker, q, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			<-ctx.Done()
			return hubclient.V4Wait{}, ctx.Err()
		})
		synctest.Wait()
		q.wake <- struct{}{}
		synctest.Wait()
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if worker.passes.Load() > 5 || worker.checks.Load() < 2 || worker.checks.Load() > 8 {
			t.Fatalf("passes=%d checks=%d", worker.passes.Load(), worker.checks.Load())
		}
	})
}

func (w *completionTestWorker) SyncOnce(ctx context.Context) error {
	err := w.Runner.SyncOnce(ctx)
	w.passDone <- err
	return err
}
func (w *completionTestWorker) BypassRetryBackoff() { w.bypasses.Add(1) }

func TestBlockedUpdateCompletionChecksInWithoutUploadRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const id = "10000000-0000-4000-8000-000000000001"
	now := time.Now()
	q, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var requests, uploads atomic.Int32
	done := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/devices/me/check-in" {
			uploads.Add(1)
			http.Error(w, "unexpected upload", http.StatusBadRequest)
			return
		}
		var body struct {
			Results []hubclient.V4CommandResult `json:"results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests.Add(1)
		commands := []hubclient.V4Command{{ID: id, Type: "update", Payload: json.RawMessage(`{"attemptId":"` + id + `","targetVersion":"0.0.6","expiresAt":"` + now.Add(time.Hour).UTC().Format(time.RFC3339Nano) + `"}`)}}
		if len(body.Results) > 0 && body.Results[0].Result == "done" {
			commands = nil
			done <- struct{}{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"configVersion": 1, "config": map[string]any{"paused": true, "autoUpdate": false, "deviceOff": true, "leaveOut": []string{}, "agentKnowledge": true}, "commands": commands, "minVersion": "0.0.3", "recommendedVersion": "0.0.6", "capabilities": []string{"update-completion/v1"}})
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &hubclient.Client{BaseURL: base, Credentials: fixedHubCredential("synthetic"), CollectorVersion: "0.0.6"}
	binding, err := client.V4Binding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Rebind(binding); err != nil {
		t.Fatal(err)
	}
	entry := syncv4.Entry{Key: "retry-one", SessionID: "ses_one", Session: hubclient.V4Session{Agent: "codex"}, RetryAt: now.Add(time.Hour), BackoffAttempt: 3}
	if err = q.Merge([]syncv4.Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err = q.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{Paused: true, DeviceOff: true}}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "0.0.6", "expiresAt": now.Add(time.Hour)})
	if err = q.AcceptUpdate(hubclient.V4Command{ID: id, Type: "update", Payload: payload}, "0.0.5", now); err != nil {
		t.Fatal(err)
	}
	if err = q.SetUpdateStage(id, "finalizing", ""); err != nil {
		t.Fatal(err)
	}
	before := q.Entries()
	worker := &completionTestWorker{Runner: &syncv4.Runner{Queue: q, Version: "0.0.6", Hub: client, Backup: &sessionbackupproducer.Manager{}, Discover: func(context.Context) ([]*session.Session, error) { t.Error("unexpected discovery"); return nil, nil }}, passDone: make(chan error, 2)}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		runV4SyncLoopForTest(ctx, worker, q, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			<-ctx.Done()
			return hubclient.V4Wait{}, ctx.Err()
		})
	}()
	select {
	case err := <-worker.passDone:
		if !errors.Is(err, syncv4.ErrPaused) {
			t.Fatalf("initial check-in: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initial check-in did not finish")
	}
	if q.PendingUpdate() == nil || requests.Load() != 1 {
		t.Fatal("initial check-in did not retain unconfirmed attempt")
	}
	time.Sleep(100 * time.Millisecond)
	if requests.Load() != 1 {
		t.Fatal("unexpected check-in before helper confirmation")
	}
	if err = q.ConfirmUpdateHandoff(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmed update did not check in while paused")
	}
	deadline := time.Now().Add(5 * time.Second)
	for q.PendingUpdate() != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	after := q.Entries()
	_, config, _ := q.Policy()
	if q.PendingUpdate() != nil || uploads.Load() != 0 || worker.bypasses.Load() != 0 || len(after) != 1 || !after[0].RetryAt.Equal(before[0].RetryAt) || after[0].BackoffAttempt != before[0].BackoffAttempt || !config.Paused || !config.DeviceOff {
		t.Fatalf("attempt=%+v uploads=%d bypass=%d before=%+v after=%+v policy=%+v", q.PendingUpdate(), uploads.Load(), worker.bypasses.Load(), before, after, config)
	}
	cancel()
	<-stopped
}

type syncLoopQueue struct{}

func (syncLoopQueue) Policy() (int64, hubclient.V4Config, string) { return 1, hubclient.V4Config{}, "" }
func (syncLoopQueue) Results() []hubclient.V4CommandResult        { return nil }
func (syncLoopQueue) InFlight() int                               { return 0 }
func (syncLoopQueue) NextRetryDelay() time.Duration               { return 0 }
func (syncLoopQueue) NextCheckInDelay() time.Duration             { return 60 * time.Second }
func (syncLoopQueue) Progress() hubclient.V4Queue                 { return hubclient.V4Queue{} }
func (syncLoopQueue) LastHubFailure() *syncv4.HubContactFailure   { return nil }

type fastSyncLoopQueue struct{ syncLoopQueue }

func (fastSyncLoopQueue) NextCheckInDelay() time.Duration { return 10 * time.Millisecond }

type syncLoopWorker struct{ started chan context.Context }

type syncLoopDiscoveryWorker struct {
	started   chan context.Context
	requested chan struct{}
	finish    <-chan struct{}
	complete  bool
}

type syncLoopWorkerFunc func(context.Context) error

func (f syncLoopWorkerFunc) SyncOnce(ctx context.Context) error { return f(ctx) }

type checkInSyncLoopWorker struct {
	syncLoopWorkerFunc
	generation atomic.Uint64
}

func (w *checkInSyncLoopWorker) CheckInGeneration() uint64 { return w.generation.Load() }

type blockingPolicyQueue struct {
	syncLoopQueue
	block         atomic.Bool
	read, release chan struct{}
}

func (q *blockingPolicyQueue) Policy() (int64, hubclient.V4Config, string) {
	if q.block.Swap(false) {
		close(q.read)
		<-q.release
	}
	return q.syncLoopQueue.Policy()
}

func runV4SyncLoopForTest(ctx context.Context, runner v4SyncWorker, queue v4SyncState, wait func(context.Context, int64) (hubclient.V4Wait, error), externalWake ...chan struct{}) {
	runV4SyncLoopWithControl(ctx, runner, queue, wait, newSyncLoopControl(nil), externalWake...)
}

func (w syncLoopWorker) SyncOnce(ctx context.Context) error {
	w.started <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func (w syncLoopDiscoveryWorker) SyncOnce(ctx context.Context) error {
	w.started <- ctx
	if w.complete {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.finish:
		return nil
	}
}

func (w syncLoopDiscoveryWorker) RequestDiscovery() {
	w.requested <- struct{}{}
}

func TestInventoryWakeRequestsDiscoveryWithoutCancellingActiveCapture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		finish := make(chan struct{})
		worker := syncLoopDiscoveryWorker{started: make(chan context.Context, 2), requested: make(chan struct{}, 1), finish: finish}
		inventoryWake := make(chan struct{}, 1)
		done := make(chan struct{})
		go func() {
			runV4SyncLoopForTest(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
				<-ctx.Done()
				return hubclient.V4Wait{}, ctx.Err()
			}, inventoryWake)
			close(done)
		}()
		active := <-worker.started
		for range 3 {
			inventoryWake <- struct{}{}
			synctest.Wait()
			if active.Err() != nil {
				t.Fatal("inventory wake cancelled active capture")
			}
			<-worker.requested
		}
		close(finish)
		<-worker.started
		cancel()
		<-done
	})
}

func TestInventoryWakeWhileWaitingRequestsDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := syncLoopDiscoveryWorker{started: make(chan context.Context, 2), requested: make(chan struct{}, 1), complete: true}
	inventoryWake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		runV4SyncLoopForTest(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			<-ctx.Done()
			return hubclient.V4Wait{}, ctx.Err()
		}, inventoryWake)
		close(done)
	}()
	<-worker.started
	inventoryWake <- struct{}{}
	<-worker.requested
	<-worker.started
	cancel()
	<-done
}

func TestV4WaitInterruptsActiveSyncForCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := syncLoopWorker{started: make(chan context.Context, 2)}
	event := make(chan hubclient.V4Wait, 1)
	waitStarted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		runV4SyncLoopForTest(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			select {
			case waitStarted <- struct{}{}:
			default:
			}
			select {
			case value := <-event:
				return value, nil
			case <-ctx.Done():
				return hubclient.V4Wait{}, ctx.Err()
			}
		})
		close(done)
	}()
	select {
	case <-worker.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first sync pass did not start")
	}
	select {
	case <-waitStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("wait channel did not open during sync")
	}
	event <- hubclient.V4Wait{CommandsAvailable: true}
	select {
	case <-worker.started:
	case <-time.After(3 * time.Second):
		t.Fatal("command wait did not interrupt the active pass")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("sync loop did not stop")
	}
}

func TestV4RepeatedWaitNotificationAllowsCheckInToFinish(t *testing.T) {
	for _, notification := range []struct {
		name string
		wait hubclient.V4Wait
	}{
		{"policy", hubclient.V4Wait{ConfigVersion: 2, Changed: true}},
		{"commands", hubclient.V4Wait{Changed: true, CommandsAvailable: true}},
		{"sync_request", hubclient.V4Wait{Changed: true, SyncRequested: true}},
	} {
		t.Run(notification.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				queue := &policyVersionQueue{}
				var passes, completed atomic.Int32
				event := make(chan struct{})
				go runV4SyncLoopForTest(ctx, syncLoopWorkerFunc(func(ctx context.Context) error {
					if passes.Add(1) == 1 {
						<-ctx.Done()
						return ctx.Err()
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Second):
						queue.version.Store(notification.wait.ConfigVersion)
						completed.Add(1)
						return nil
					}
				}), queue, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
					select {
					case <-ctx.Done():
						return hubclient.V4Wait{}, ctx.Err()
					case <-event:
						if completed.Load() == 0 {
							return notification.wait, nil
						}
						return hubclient.V4Wait{}, nil
					}
				})
				synctest.Wait()
				close(event)
				time.Sleep(3 * time.Second)
				synctest.Wait()
				wantPasses := int32(2)
				if notification.wait.ConfigVersion > 0 {
					wantPasses++ // The next check-in acknowledges the applied version.
				}
				if passes.Load() != wantPasses || completed.Load() != wantPasses-1 {
					t.Fatalf("passes=%d completed=%d; repeated notification starved check-in", passes.Load(), completed.Load())
				}
			})
		})
	}
}

func TestV4WaitInterruptsAgainForNewPolicyOrCommands(t *testing.T) {
	for _, mode := range []string{"policy", "commands_cleared", "commands_acknowledged"} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var passes atomic.Int32
			events := make(chan hubclient.V4Wait, 1)
			worker := &checkInSyncLoopWorker{syncLoopWorkerFunc: func(ctx context.Context) error {
				passes.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}}
			go runV4SyncLoopForTest(ctx, observedSyncWorker{runner: worker, controller: &syncController{}}, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
				select {
				case event := <-events:
					return event, nil
				case <-ctx.Done():
					return hubclient.V4Wait{}, ctx.Err()
				}
			})
			synctest.Wait()
			for _, version := range []int64{2, 3} {
				if mode == "commands_cleared" {
					events <- hubclient.V4Wait{}
					time.Sleep(100 * time.Millisecond)
					synctest.Wait()
				}
				if mode == "commands_acknowledged" {
					worker.generation.Add(1)
				}
				configVersion := int64(1)
				if mode == "policy" {
					configVersion = version
				}
				events <- hubclient.V4Wait{ConfigVersion: configVersion, Changed: true, CommandsAvailable: mode != "policy"}
				time.Sleep(100 * time.Millisecond)
				synctest.Wait()
				if got := passes.Load(); got != int32(version) {
					t.Fatalf("mode=%s version=%d passes=%d", mode, version, got)
				}
			}
		})
	}
}

func TestV4PolicyNotificationBeforeBlockedStateIsNotLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		queue := &blockingPolicyQueue{read: make(chan struct{}), release: make(chan struct{})}
		waitStarted, changed := make(chan struct{}), make(chan struct{})
		var passes atomic.Int32
		var waiting atomic.Bool
		go runV4SyncLoopForTest(ctx, syncLoopWorkerFunc(func(ctx context.Context) error {
			if passes.Add(1) == 1 {
				<-waitStarted
				queue.block.Store(true)
				return syncv4.ErrPolicyBlocked
			}
			<-ctx.Done()
			return ctx.Err()
		}), queue, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			if waiting.CompareAndSwap(false, true) {
				close(waitStarted)
			}
			select {
			case <-changed:
				return hubclient.V4Wait{ConfigVersion: 2, Changed: true}, nil
			case <-ctx.Done():
				return hubclient.V4Wait{}, ctx.Err()
			}
		})
		<-queue.read
		close(changed)
		synctest.Wait()
		close(queue.release)
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if passes.Load() != 2 {
			t.Fatal("policy notification was lost while entering the blocked state")
		}
	})
}

func TestSyncRequestedTriggersInventoryBeforeTheNextPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := syncLoopWorker{started: make(chan context.Context, 3)}
	request := make(chan hubclient.V4Wait, 1)
	waitStarted := make(chan struct{}, 4)
	inventoryWake := make(chan struct{}, 1)
	inventoryRequest := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		runV4SyncLoopForTest(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			select {
			case waitStarted <- struct{}{}:
			default:
			}
			select {
			case value := <-request:
				return value, nil
			case <-ctx.Done():
				return hubclient.V4Wait{}, ctx.Err()
			}
		}, inventoryWake, nil, inventoryRequest)
		close(done)
	}()
	select {
	case <-worker.started:
	case <-time.After(3 * time.Second):
		t.Fatal("initial pass did not start")
	}
	select {
	case <-waitStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("long-poll wait did not start")
	}
	request <- hubclient.V4Wait{SyncRequested: true}
	select {
	case <-inventoryRequest:
	case <-time.After(time.Second):
		t.Fatal("sync-now did not request a fresh inventory within one second")
	}
	inventoryWake <- struct{}{}
	select {
	case <-worker.started:
	case <-time.After(time.Second):
		t.Fatal("inventory completion did not start a sync pass")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("sync loop did not stop")
	}
}

func TestSyncRequestedInventoryIsDebounced(t *testing.T) {
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	if got := inventoryRequestDebounceDelay(time.Time{}, now); got != 0 {
		t.Fatalf("first inventory request delay = %s, want 0", got)
	}
	if got := inventoryRequestDebounceDelay(now, now.Add(syncNowInventoryDebounce-time.Second)); got != time.Second {
		t.Fatalf("request inside debounce window delay = %s, want 1s", got)
	}
	if got := inventoryRequestDebounceDelay(now, now.Add(syncNowInventoryDebounce)); got != 0 {
		t.Fatalf("request at debounce boundary delay = %s, want 0", got)
	}
}

func TestTransportRetryBackoffScheduleResetCapAndRetryAfter(t *testing.T) {
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{{1, 2 * time.Second}, {2, 4 * time.Second}, {3, 8 * time.Second}, {4, 16 * time.Second},
		{5, 32 * time.Second}, {6, time.Minute}, {7, time.Minute}, {100, time.Minute}} {
		if got := transportRetryBase(test.attempt); got != test.want {
			t.Errorf("transportRetryBase(%d) = %s, want %s", test.attempt, got, test.want)
		}
	}
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	backoff := transportRetryBackoff{}
	for attempt, want := range []time.Duration{2 * time.Second, 4 * time.Second} {
		got := backoff.delay(err)
		spread := want / 5
		if got < want-spread || got > want+spread {
			t.Fatalf("jittered retry %d = %s, outside %s..%s", attempt+1, got, want-spread, want+spread)
		}
	}
	if got := backoff.delay(nil); got != 0 || backoff.failures != 0 {
		t.Fatalf("success reset = delay %s, failures %d", got, backoff.failures)
	}
	if got := backoff.delay(err); got < 1600*time.Millisecond || got > 2400*time.Millisecond {
		t.Fatalf("retry after success = %s, want first-step jitter", got)
	}
	if got := backoff.delay(hubclient.V4Problem{Code: "maintenance", HTTPStatus: 503, RetryAfter: 90 * time.Second}); got != 90*time.Second {
		t.Fatalf("Retry-After delay = %s, want 90s", got)
	}
	if !retryableHubContact(hubclient.V4Problem{Code: "maintenance", HTTPStatus: 503}) || !retryableHubContact(err) {
		t.Fatal("transport failure was not retryable")
	}
	if got := jitteredTransportRetryDelay(100); got < 48*time.Second || got > time.Minute {
		t.Fatalf("capped jitter = %s, want 48s..60s", got)
	}
	if !retryableHubContact(hubclient.V4Problem{Code: "rate_limited", HTTPStatus: http.StatusTooManyRequests}) {
		t.Fatal("429 was not treated as retryable")
	}
	if retryableHubContact(hubclient.V4Problem{Code: "device_removed", HTTPStatus: 403}) {
		t.Fatal("permanent Hub rejection was treated as a transport failure")
	}
}

type retryBypassWorker struct {
	calls       atomic.Int32
	bypasses    atomic.Int32
	firstFailed chan struct{}
	secondStart chan struct{}
}

func (worker *retryBypassWorker) SyncOnce(ctx context.Context) error {
	switch worker.calls.Add(1) {
	case 1:
		close(worker.firstFailed)
		return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	case 2:
		close(worker.secondStart)
		<-ctx.Done()
		return ctx.Err()
	default:
		return nil
	}
}

func (worker *retryBypassWorker) BypassRetryBackoff() { worker.bypasses.Add(1) }

func TestExplicitRetryBypassesTransportBackoffOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &retryBypassWorker{firstFailed: make(chan struct{}), secondStart: make(chan struct{})}
	retryWake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		runV4SyncLoopForTest(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			<-ctx.Done()
			return hubclient.V4Wait{}, ctx.Err()
		}, nil, retryWake)
		close(done)
	}()
	select {
	case <-worker.firstFailed:
	case <-time.After(3 * time.Second):
		t.Fatal("initial failed pass did not run")
	}
	retryWake <- struct{}{}
	select {
	case <-worker.secondStart:
	case <-time.After(time.Second):
		t.Fatal("explicit retry did not bypass the backoff")
	}
	if worker.bypasses.Load() != 1 {
		t.Fatalf("backoff bypass calls = %d, want 1", worker.bypasses.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("sync loop did not stop")
	}
}

func TestV4WaitKeepsPickingUpCommandsAfterDeferredPass(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{{"paused", syncv4.ErrPaused}, {"failed", errors.New("cycle failed")}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			passes := make(chan struct{}, 2)
			event := make(chan hubclient.V4Wait, 1)
			waitStarted := make(chan struct{}, 1)
			done := make(chan struct{})
			go func() {
				runV4SyncLoopForTest(ctx, syncLoopWorkerFunc(func(context.Context) error {
					passes <- struct{}{}
					return test.err
				}), syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
					select {
					case waitStarted <- struct{}{}:
					default:
					}
					select {
					case value := <-event:
						return value, nil
					case <-ctx.Done():
						return hubclient.V4Wait{}, ctx.Err()
					}
				})
				close(done)
			}()
			select {
			case <-passes:
			case <-time.After(3 * time.Second):
				t.Fatal("first pass did not run")
			}
			select {
			case <-waitStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("wait did not open after a deferred pass")
			}
			event <- hubclient.V4Wait{CommandsAvailable: true}
			select {
			case <-passes:
			case <-time.After(3 * time.Second):
				t.Fatal("command was not picked up after defer")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("sync loop did not stop")
			}
		})
	}
}

func TestNotPairedSyncLogsOnceUntilStateChanges(t *testing.T) {
	var output bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var passes atomic.Int32
	done := make(chan struct{})
	go func() {
		runV4SyncLoopForTest(ctx, syncLoopWorkerFunc(func(context.Context) error {
			passes.Add(1)
			return hubclient.ErrNotPaired
		}), fastSyncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			<-ctx.Done()
			return hubclient.V4Wait{}, ctx.Err()
		})
		close(done)
	}()
	deadline := time.After(time.Second)
	for passes.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("sync loop did not retry the not-paired pass")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sync loop did not stop")
	}

	got := output.String()
	if strings.Count(got, "v4 sync stopped: device removed from Hub") != 1 || strings.Contains(got, "v4 sync deferred: local_error") {
		t.Fatalf("not-paired sync logs = %q", got)
	}
}

type policyVersionQueue struct{ version atomic.Int64 }

func (q *policyVersionQueue) Policy() (int64, hubclient.V4Config, string) {
	return q.version.Load(), hubclient.V4Config{}, ""
}
func (*policyVersionQueue) Results() []hubclient.V4CommandResult      { return nil }
func (*policyVersionQueue) InFlight() int                             { return 0 }
func (*policyVersionQueue) NextRetryDelay() time.Duration             { return 0 }
func (*policyVersionQueue) NextCheckInDelay() time.Duration           { return 60 * time.Second }
func (*policyVersionQueue) Progress() hubclient.V4Queue               { return hubclient.V4Queue{} }
func (*policyVersionQueue) LastHubFailure() *syncv4.HubContactFailure { return nil }

func TestSyncPausedCodeIdlesWithoutFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := &policyVersionQueue{}
	var passes atomic.Int32
	done := make(chan struct{})
	go func() {
		runV4SyncLoopForTest(ctx, syncLoopWorkerFunc(func(context.Context) error {
			passes.Add(1)
			return syncv4.ErrPolicyBlocked
		}), queue, func(ctx context.Context, since int64) (hubclient.V4Wait, error) {
			current := queue.version.Load()
			return hubclient.V4Wait{ConfigVersion: current, Changed: current > since}, nil
		})
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for passes.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("initial sync pass did not run")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-time.After(150 * time.Millisecond):
	case <-done:
		t.Fatal("sync loop stopped while policy was unchanged")
	}
	if got := passes.Load(); got != 1 {
		t.Fatalf("sync passes before policy change=%d, want 1", got)
	}
	queue.version.Store(1)
	deadline = time.After(2 * time.Second)
	for passes.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("new policy version did not resume sync")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync loop did not stop")
	}
}

func TestDormantBacksOff(t *testing.T) {
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{{1, time.Minute}, {2, 5 * time.Minute}, {3, 15 * time.Minute}, {4, time.Hour}, {5, time.Hour}} {
		if got := credentialRetryDelay(test.attempt); got != test.want {
			t.Errorf("attempt %d retry delay=%s, want %s", test.attempt, got, test.want)
		}
	}
	if !credentialRetry(hubclient.V4Problem{Code: "device_dormant"}) ||
		!credentialRetry(hubclient.V4Problem{Code: "unauthorized"}) ||
		!credentialRetry(hubclient.V4Problem{HTTPStatus: 401}) {
		t.Fatal("dormant and unauthorized credentials must use the backoff schedule")
	}
	if got := clampImportRetry(time.Minute, hubclient.V4Problem{Code: "device_dormant"}, true, true); got != time.Minute {
		t.Fatalf("credential retry during import=%s, want %s", got, time.Minute)
	}
	if got := clampImportRetry(time.Minute, errors.New("temporary import error"), true, false); got != 10*time.Second {
		t.Fatalf("ordinary retry during import=%s, want %s", got, 10*time.Second)
	}
}

func TestBlockedPolicyWakesForQueuedCommandWithoutRetryBypass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		events := make(chan hubclient.V4Wait, 1)
		var passes atomic.Int32
		worker := syncLoopWorkerFunc(func(ctx context.Context) error { passes.Add(1); return syncv4.ErrPolicyBlocked })
		go runV4SyncLoopForTest(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			select {
			case value := <-events:
				return value, nil
			case <-ctx.Done():
				return hubclient.V4Wait{}, ctx.Err()
			}
		})
		synctest.Wait()
		if passes.Load() != 1 {
			t.Fatalf("initial passes=%d", passes.Load())
		}
		events <- hubclient.V4Wait{ConfigVersion: 1, CommandsAvailable: true}
		synctest.Wait()
		if passes.Load() != 2 {
			t.Fatalf("command did not wake blocked check-in: %d", passes.Load())
		}
	})
}
