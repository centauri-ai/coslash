package main

import (
	"context"
	"errors"
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
