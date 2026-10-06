package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type v4SyncWorker interface {
	SyncOnce(context.Context) error
}

type v4SyncState interface {
	Policy() (int64, hubclient.V4Config, string)
	Results() []hubclient.V4CommandResult
	InFlight() int
	NextRetryDelay() time.Duration
	NextCheckInDelay() time.Duration
	Progress() hubclient.V4Queue
}

type syncLoopControl struct {
	wake   chan struct{}
	mu     sync.Mutex
	idle   bool
	paused func() bool
}

func newSyncLoopControl(paused func() bool) *syncLoopControl {
	return &syncLoopControl{wake: make(chan struct{}, 1), idle: true, paused: paused}
}

func (control *syncLoopControl) signal() {
	control.mu.Lock()
	defer control.mu.Unlock()
	select {
	case control.wake <- struct{}{}:
	default:
	}
}

func (control *syncLoopControl) wakeIfIdle() bool {
	control.mu.Lock()
	defer control.mu.Unlock()
	if !control.idle || control.paused != nil && control.paused() {
		return false
	}
	select {
	case control.wake <- struct{}{}:
		return true
	default:
		return true // A coalesced wake will start a pass that sees this change.
	}
}

func (control *syncLoopControl) setIdle(idle bool) {
	control.mu.Lock()
	control.idle = idle
	control.mu.Unlock()
}

func (control *syncLoopControl) beginPass() {
	control.mu.Lock()
	control.idle = false
	// A pass begins with a check-in, so a wake queued while the loop was idle
	// is satisfied by this pass and should not immediately cancel it.
	select {
	case <-control.wake:
	default:
	}
	control.mu.Unlock()
}

func runV4SyncLoop(ctx context.Context, runner v4SyncWorker, queue v4SyncState, wait func(context.Context, int64) (hubclient.V4Wait, error), externalWake ...<-chan struct{}) {
	runV4SyncLoopWithControl(ctx, runner, queue, wait, newSyncLoopControl(nil), externalWake...)
}

func runV4SyncLoopWithControl(ctx context.Context, runner v4SyncWorker, queue v4SyncState, wait func(context.Context, int64) (hubclient.V4Wait, error), control *syncLoopControl, externalWake ...<-chan struct{}) {
	var inventoryWake <-chan struct{}
	if len(externalWake) > 0 {
		inventoryWake = externalWake[0]
	}
	go func() {
		for ctx.Err() == nil {
			version, _, _ := queue.Policy()
			result, err := wait(ctx, version)
			if ctx.Err() != nil {
				return
			}
			if err == nil && (result.Changed || result.CommandsAvailable || result.SyncRequested) {
				control.signal()
			}
			delay := 100 * time.Millisecond
			if err != nil {
				delay = time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
	if reporter, ok := runner.(interface {
		ImportActive() bool
		ProgressCheckIn(context.Context) (bool, time.Duration, error)
	}); ok {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			var retryAt time.Time
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if !reporter.ImportActive() || time.Now().Before(retryAt) {
					continue
				}
				changed, retryAfter, err := reporter.ProgressCheckIn(ctx)
				if err != nil {
					retryAt = time.Now().Add(max(10*time.Second, retryAfter))
					continue
				}
				if changed {
					control.signal()
				}
			}
		}()
	}

	for ctx.Err() == nil {
		control.beginPass()
		passContext, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- runner.SyncOnce(passContext) }()
		var err error
		select {
		case err = <-done:
		case <-control.wake:
			cancel()
			<-done
			control.setIdle(true)
			continue
		case <-inventoryWake:
			cancel()
			<-done
			control.setIdle(true)
			continue
		case <-ctx.Done():
			cancel()
			<-done
			control.setIdle(true)
			return
		}
		cancel()
		control.setIdle(true)
		if errors.Is(err, syncv4.ErrCommandPickedUp) {
			continue
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("v4 sync deferred: %s", syncv4.DeferReason(err))
		}
		terminalResult := false
		if err == nil || errors.Is(err, syncv4.ErrPaused) {
			for _, result := range queue.Results() {
				if result.Result != "in_progress" {
					terminalResult = true
					break
				}
			}
		}
		if terminalResult {
			continue
		}
		delay := syncv4.NextSyncDelay(err, queue.InFlight(), queue.NextCheckInDelay())
		if active, ok := runner.(interface{ ImportActive() bool }); ok && active.ImportActive() && delay > 10*time.Second {
			delay = 10 * time.Second
		}
		_, config, _ := queue.Policy()
		if config.ImportPlan != nil && queue.Progress().Pending > 0 && delay > 10*time.Second {
			delay = 10 * time.Second
		}
		if retry := queue.NextRetryDelay(); retry > 0 && retry < delay {
			delay = retry
		}
		var problem hubclient.V4Problem
		if errors.As(err, &problem) && problem.RetryAfter > delay {
			delay = problem.RetryAfter
		}
		select {
		case <-ctx.Done():
			return
		case <-control.wake:
		case <-inventoryWake:
		case <-time.After(delay):
		}
	}
}
