package main

import (
	"context"
	"errors"
	"log"
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
	Progress() hubclient.V4Queue
}

func runV4SyncLoop(ctx context.Context, runner v4SyncWorker, queue v4SyncState, wait func(context.Context, int64) (hubclient.V4Wait, error), externalWake ...<-chan struct{}) {
	wake := make(chan struct{}, 1)
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
			if err == nil && (result.Changed || result.CommandsAvailable) {
				select {
				case wake <- struct{}{}:
				default:
				}
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

	const interval = 5 * time.Minute
	for ctx.Err() == nil {
		passContext, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- runner.SyncOnce(passContext) }()
		var err error
		select {
		case err = <-done:
		case <-wake:
			cancel()
			<-done
			continue
		case <-inventoryWake:
			cancel()
			<-done
			continue
		case <-ctx.Done():
			cancel()
			<-done
			return
		}
		cancel()
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
		delay := syncv4.NextSyncDelay(err, queue.InFlight(), interval)
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
		case <-wake:
		case <-inventoryWake:
		case <-time.After(delay):
		}
	}
}
