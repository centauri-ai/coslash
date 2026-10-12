package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

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
	started   chan struct{}
	requested chan struct{}
	complete  bool
}

type syncLoopWorkerFunc func(context.Context) error

func (f syncLoopWorkerFunc) SyncOnce(ctx context.Context) error { return f(ctx) }

func runV4SyncLoopForTest(ctx context.Context, runner v4SyncWorker, queue v4SyncState, wait func(context.Context, int64) (hubclient.V4Wait, error), externalWake ...chan struct{}) {
	runV4SyncLoopWithControl(ctx, runner, queue, wait, newSyncLoopControl(nil), externalWake...)
}

func (w syncLoopWorker) SyncOnce(ctx context.Context) error {
	w.started <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func (w syncLoopDiscoveryWorker) SyncOnce(ctx context.Context) error {
	w.started <- struct{}{}
	if w.complete {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func (w syncLoopDiscoveryWorker) RequestDiscovery() {
	w.requested <- struct{}{}
}

func TestInventoryWakeRequestsDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := syncLoopDiscoveryWorker{started: make(chan struct{}, 2), requested: make(chan struct{}, 1)}
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

func TestInventoryWakeWhileWaitingRequestsDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := syncLoopDiscoveryWorker{started: make(chan struct{}, 2), requested: make(chan struct{}, 1), complete: true}
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
