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
	policyWake := make(chan struct{}, 1)
	var policyMu sync.Mutex
	blockedPolicyVersion := int64(-1)
	go func() {
		authFailures := 0
		for ctx.Err() == nil {
			version, _, _ := queue.Policy()
			result, err := wait(ctx, version)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				policyMu.Lock()
				blockedAt := blockedPolicyVersion
				switch {
				case blockedAt >= 0 && result.ConfigVersion > blockedAt:
					select {
					case policyWake <- struct{}{}:
					default:
					}
				case blockedAt < 0 && (result.Changed || result.CommandsAvailable || result.SyncRequested):
					control.signal()
				}
				policyMu.Unlock()
			}
			delay := 100 * time.Millisecond
			if credentialRetry(err) {
				authFailures++
				delay = credentialRetryDelay(authFailures)
			} else if err != nil {
				delay = time.Second
			} else {
				authFailures = 0
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

	authFailures := 0
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
		if policyBlocked(err) {
			version, _, _ := queue.Policy()
			policyMu.Lock()
			blockedPolicyVersion = version
			// Ignore requests that arrived before the policy block was observed.
			clearWake(control.wake)
			clearWake(inventoryWake)
			clearWake(policyWake)
			policyMu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-policyWake:
			}
			policyMu.Lock()
			blockedPolicyVersion = -1
			clearWake(policyWake)
			clearWake(control.wake)
			clearWake(inventoryWake)
			policyMu.Unlock()
			continue
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("v4 sync deferred: %s", syncv4.DeferReason(err))
		}
		if credentialRetry(err) {
			authFailures++
		} else if err == nil {
			authFailures = 0
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
		if credentialRetry(err) {
			delay = credentialRetryDelay(authFailures)
		}
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

func clearWake(wake <-chan struct{}) {
	if wake == nil {
		return
	}
	select {
	case <-wake:
	default:
	}
}

func policyBlocked(err error) bool {
	if errors.Is(err, syncv4.ErrPolicyBlocked) || errors.Is(err, syncv4.ErrDeviceSyncOff) {
		return true
	}
	var problem hubclient.V4Problem
	return errors.As(err, &problem) && (problem.Code == "sync_paused" || problem.Code == "device_sync_off")
}

func credentialRetry(err error) bool {
	var problem hubclient.V4Problem
	return errors.As(err, &problem) && (problem.Code == "device_dormant" || problem.Code == "unauthorized" || problem.HTTPStatus == 401)
}

func credentialRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	case 3:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}
