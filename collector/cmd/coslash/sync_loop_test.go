package main

import (
	"context"
	"errors"
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

type syncLoopWorker struct{ started chan context.Context }

type syncLoopWorkerFunc func(context.Context) error

func (f syncLoopWorkerFunc) SyncOnce(ctx context.Context) error { return f(ctx) }

func (w syncLoopWorker) SyncOnce(ctx context.Context) error {
	w.started <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func TestV4WaitInterruptsActiveSyncForCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := syncLoopWorker{started: make(chan context.Context, 2)}
	event := make(chan hubclient.V4Wait, 1)
	waitStarted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		runV4SyncLoop(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
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

func TestSyncRequestedRunsCheckInWithinOneSecond(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := syncLoopWorker{started: make(chan context.Context, 3)}
	request := make(chan hubclient.V4Wait, 1)
	waitStarted := make(chan struct{}, 4)
	done := make(chan struct{})
	go func() {
		runV4SyncLoop(ctx, worker, syncLoopQueue{}, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
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
		})
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
	requestedAt := time.Now()
	request <- hubclient.V4Wait{SyncRequested: true}
	select {
	case <-worker.started:
		if elapsed := time.Since(requestedAt); elapsed > time.Second {
			t.Fatalf("sync-now pass started after %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("sync-now did not start a check-in pass within one second")
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
				runV4SyncLoop(ctx, syncLoopWorkerFunc(func(context.Context) error {
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

type policyVersionQueue struct{ version atomic.Int64 }

func (q *policyVersionQueue) Policy() (int64, hubclient.V4Config, string) {
	return q.version.Load(), hubclient.V4Config{}, ""
}
func (*policyVersionQueue) Results() []hubclient.V4CommandResult { return nil }
func (*policyVersionQueue) InFlight() int                        { return 0 }
func (*policyVersionQueue) NextRetryDelay() time.Duration        { return 0 }
func (*policyVersionQueue) NextCheckInDelay() time.Duration      { return 60 * time.Second }
func (*policyVersionQueue) Progress() hubclient.V4Queue          { return hubclient.V4Queue{} }

func TestSyncPausedCodeIdlesWithoutFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := &policyVersionQueue{}
	var passes atomic.Int32
	done := make(chan struct{})
	go func() {
		runV4SyncLoop(ctx, syncLoopWorkerFunc(func(context.Context) error {
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
