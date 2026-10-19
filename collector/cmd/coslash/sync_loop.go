package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"math/rand"
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
	LastHubFailure() *syncv4.HubContactFailure
}

type syncLoopControl struct {
	wake   chan struct{}
	mu     sync.Mutex
	idle   bool
	paused func() bool
}

const (
	transportRetryInitial    = 2 * time.Second
	transportRetryMax        = time.Minute
	syncNowInventoryDebounce = 30 * time.Second
)

type transportRetryBackoff struct{ failures int }

func (backoff *transportRetryBackoff) delay(err error) time.Duration {
	if err == nil {
		backoff.failures = 0
		return 0
	}
	if !retryableHubContact(err) {
		return 0
	}
	backoff.failures++
	delay := jitteredTransportRetryDelay(backoff.failures)
	var problem hubclient.V4Problem
	if errors.As(err, &problem) && problem.RetryAfter > delay {
		delay = problem.RetryAfter
	}
	return delay
}

func (backoff *transportRetryBackoff) reset() { backoff.failures = 0 }

func retryableHubContact(err error) bool {
	failure, ok := hubclient.ClassifyHubFailure(err)
	if !ok {
		return false
	}
	switch failure.Cause {
	case hubclient.FailureDNS, hubclient.FailureConnect, hubclient.FailureTLS,
		hubclient.FailureProxy, hubclient.FailureHubUnavailable:
		return true
	default:
		return false
	}
}

func transportRetryBase(attempt int) time.Duration {
	delay := transportRetryInitial
	for attempt > 1 && delay < transportRetryMax {
		delay *= 2
		attempt--
	}
	return min(delay, transportRetryMax)
}

func jitteredTransportRetryDelay(attempt int) time.Duration {
	delay := transportRetryBase(attempt)
	spread := delay / 5
	jitter := time.Duration(rand.Int63n(int64(spread*2+1))) - spread
	return min(max(delay+jitter, time.Second), transportRetryMax)
}

func bypassRetryBackoff(runner v4SyncWorker) {
	if bypasser, ok := runner.(interface{ BypassRetryBackoff() }); ok {
		bypasser.BypassRetryBackoff()
	}
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

func runV4SyncLoopWithControl(ctx context.Context, runner v4SyncWorker, queue v4SyncState, wait func(context.Context, int64) (hubclient.V4Wait, error), control *syncLoopControl, externalWake ...chan struct{}) {
	var inventoryWake <-chan struct{}
	var retryWake <-chan struct{}
	var inventoryRequest chan<- struct{}
	if len(externalWake) > 0 {
		inventoryWake = externalWake[0]
	}
	if len(externalWake) > 1 {
		retryWake = externalWake[1]
	}
	if len(externalWake) > 2 {
		inventoryRequest = externalWake[2]
	}
	policyWake := make(chan struct{}, 1)
	var policyMu sync.Mutex
	blockedPolicyVersion := int64(-1)
	go func() {
		authFailures := 0
		transportBackoff := transportRetryBackoff{}
		syncRequestPending := false
		for ctx.Err() == nil {
			if queue.LastHubFailure() != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			version, _, _ := queue.Policy()
			result, err := wait(ctx, version)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				policyMu.Lock()
				if !result.SyncRequested {
					syncRequestPending = false
				}
				blockedAt := blockedPolicyVersion
				switch {
				case blockedAt >= 0 && result.ConfigVersion > blockedAt:
					select {
					case policyWake <- struct{}{}:
					default:
					}
				case blockedAt < 0:
					if result.Changed || result.CommandsAvailable {
						control.signal()
					}
					if result.SyncRequested && !syncRequestPending {
						syncRequestPending = true
						if inventoryRequest == nil {
							control.signal()
						} else {
							requestInventory(inventoryRequest)
						}
					}
				}
				policyMu.Unlock()
			}
			delay := 100 * time.Millisecond
			retrySync := false
			if credentialRetry(err) {
				authFailures++
				delay = credentialRetryDelay(authFailures)
			} else if retryableHubContact(err) {
				delay = transportBackoff.delay(err)
				retrySync = true
			} else if err != nil {
				transportBackoff.reset()
				delay = time.Second
			} else {
				authFailures = 0
				transportBackoff.reset()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
				if retrySync {
					control.wakeIfIdle()
				}
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
			transportBackoff := transportRetryBackoff{}
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if !reporter.ImportActive() || time.Now().Before(retryAt) {
					continue
				}
				if queue.LastHubFailure() != nil {
					control.wakeIfIdle()
					retryAt = time.Now().Add(time.Minute)
					continue
				}
				changed, retryAfter, err := reporter.ProgressCheckIn(ctx)
				if err != nil {
					if retryableHubContact(err) {
						retryAt = time.Now().Add(transportBackoff.delay(err))
					} else {
						transportBackoff.reset()
						retryAt = time.Now().Add(max(10*time.Second, retryAfter))
					}
					continue
				}
				transportBackoff.reset()
				if changed {
					control.signal()
				}
			}
		}()
	}

	authFailures := 0
	transportBackoff := transportRetryBackoff{}
	var lastLocalError [32]byte
	hasLastLocalError := false
	hasLoggedNotPaired := false
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
			requestDiscovery(runner)
			continue
		case <-retryWake:
			cancel()
			<-done
			control.setIdle(true)
			transportBackoff.reset()
			bypassRetryBackoff(runner)
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
			if clearWake(inventoryWake) {
				requestDiscovery(runner)
			}
			clearWake(policyWake)
			policyMu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-policyWake:
			case <-retryWake:
				transportBackoff.reset()
				bypassRetryBackoff(runner)
			}
			policyMu.Lock()
			blockedPolicyVersion = -1
			clearWake(policyWake)
			clearWake(control.wake)
			if clearWake(inventoryWake) {
				requestDiscovery(runner)
			}
			policyMu.Unlock()
			continue
		}
		if err != nil && (ctx.Err() == nil || syncv4.DeferReason(err) == "not_paired") {
			reason := syncv4.DeferReason(err)
			shouldLog := true
			if _, contactFailure := hubclient.ClassifyHubFailure(err); contactFailure {
				// The transport logs only when the classified contact cause changes.
				shouldLog = false
				hasLoggedNotPaired = false
				hasLastLocalError = false
			} else if reason == "not_paired" {
				shouldLog = !hasLoggedNotPaired
				hasLoggedNotPaired = true
				hasLastLocalError = false
			} else if reason == "local_error" {
				hasLoggedNotPaired = false
				fingerprint := sha256.Sum256([]byte(err.Error()))
				shouldLog = !hasLastLocalError || fingerprint != lastLocalError
				lastLocalError, hasLastLocalError = fingerprint, true
			} else {
				hasLoggedNotPaired = false
				hasLastLocalError = false
			}
			if shouldLog {
				if reason == "not_paired" {
					log.Printf("v4 sync stopped: device removed from Hub; run coslash connect to add it again")
				} else {
					log.Printf("v4 sync deferred: %s", reason)
				}
			}
		} else if err == nil {
			hasLoggedNotPaired = false
			hasLastLocalError = false
		}
		if credentialRetry(err) {
			authFailures++
		} else if err == nil {
			authFailures = 0
		}
		if err == nil || !retryableHubContact(err) {
			transportBackoff.reset()
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
		if retryableHubContact(err) {
			delay = transportBackoff.delay(err)
		}
		if credentialRetry(err) {
			delay = credentialRetryDelay(authFailures)
		}
		importActive := false
		if active, ok := runner.(interface{ ImportActive() bool }); ok {
			importActive = active.ImportActive()
		}
		_, config, _ := queue.Policy()
		importPending := config.ImportPlan != nil && queue.Progress().Pending > 0
		delay = clampImportRetry(delay, err, importActive, importPending)
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
			requestDiscovery(runner)
		case <-retryWake:
			transportBackoff.reset()
			bypassRetryBackoff(runner)
			continue
		case <-time.After(delay):
		}
	}
}

func requestInventory(request chan<- struct{}) {
	if request == nil {
		return
	}
	select {
	case request <- struct{}{}:
	default:
	}
}

func clampImportRetry(delay time.Duration, err error, importActive, importPending bool) time.Duration {
	if !credentialRetry(err) && (importActive || importPending) && delay > 10*time.Second {
		return 10 * time.Second
	}
	return delay
}

func clearWake(wake <-chan struct{}) bool {
	if wake == nil {
		return false
	}
	select {
	case <-wake:
		return true
	default:
		return false
	}
}

func requestDiscovery(runner v4SyncWorker) {
	if requester, ok := runner.(interface{ RequestDiscovery() }); ok {
		requester.RequestDiscovery()
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
