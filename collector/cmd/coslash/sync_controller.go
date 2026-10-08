package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type syncLoopStarter func(context.Context, *syncv4.Queue, *hubclient.Client, chan struct{})

type syncStatus struct {
	State     string            `json:"state"`
	HubOrigin string            `json:"hubOrigin"`
	Sessions  map[string]string `json:"sessions"`
}

type syncController struct {
	ensureMu   sync.Mutex
	mu         sync.RWMutex
	queue      *syncv4.Queue
	startLoop  syncLoopStarter
	localPause func() bool

	client     *hubclient.Client
	binding    string
	connected  bool
	revoked    bool
	cancel     context.CancelFunc
	done       chan struct{}
	wake       chan struct{}
	passActive atomic.Bool
}

func newSyncController(startLoop syncLoopStarter, localPause func() bool) (*syncController, error) {
	controller := &syncController{startLoop: startLoop, localPause: localPause}
	if !hubclient.V4SyncEnabled() {
		return controller, nil
	}
	queue, err := syncv4.Open("")
	if err != nil {
		return nil, err
	}
	controller.queue = queue
	return controller, nil
}

func (c *syncController) Queue() *syncv4.Queue { return c.queue }

func (c *syncController) Ensure(client *hubclient.Client) error {
	c.ensureMu.Lock()
	defer c.ensureMu.Unlock()
	if client == nil || client.BaseURL == nil || client.Credentials == nil {
		c.stopLoop(true)
		c.mu.Lock()
		c.client, c.binding, c.connected, c.revoked = client, "", false, false
		c.mu.Unlock()
		return nil
	}
	if !hubclient.V4SyncEnabled() {
		c.stopLoop(true)
		c.mu.Lock()
		c.client, c.binding, c.connected, c.revoked = client, "", true, false
		c.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	credential, err := client.Credentials.Load(ctx)
	if errors.Is(err, hubclient.ErrNotPaired) || (err == nil && credential == "") {
		c.stopLoop(true)
		c.mu.Lock()
		c.client, c.binding, c.connected, c.revoked = client, "", false, false
		c.mu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	binding, err := client.V4Binding(ctx)
	if err != nil {
		return err
	}
	c.mu.RLock()
	sameLoop := c.done != nil && c.binding == binding && !c.revoked
	revokedBinding := c.binding == binding && c.revoked
	c.mu.RUnlock()
	if sameLoop {
		c.mu.Lock()
		c.client, c.connected, c.revoked = client, true, false
		c.mu.Unlock()
		return nil
	}
	if revokedBinding {
		c.mu.Lock()
		c.client = client
		c.mu.Unlock()
		return nil
	}
	c.stopLoop(true)
	if c.queue != nil {
		if err := c.queue.Rebind(binding); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.client, c.binding, c.connected, c.revoked = client, binding, true, false
	if c.queue == nil || c.startLoop == nil {
		c.mu.Unlock()
		return nil
	}
	loopContext, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	c.cancel, c.done, c.wake = stop, done, wake
	c.passActive.Store(false)
	c.mu.Unlock()
	go func() {
		defer close(done)
		c.startLoop(loopContext, c.queue, client, wake)
		c.passActive.Store(false)
		c.mu.Lock()
		if c.done == done {
			c.cancel, c.done, c.wake = nil, nil, nil
		}
		c.mu.Unlock()
	}()
	return nil
}

func (c *syncController) RequestPass(_ string) {
	c.mu.RLock()
	connected, wake := c.connected, c.wake
	c.mu.RUnlock()
	if !connected || wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (c *syncController) LoopIdle() bool { return !c.passActive.Load() }

func (c *syncController) Stop() {
	c.ensureMu.Lock()
	defer c.ensureMu.Unlock()
	c.stopLoop(true)
}

func (c *syncController) stopLoop(wait bool) {
	c.mu.RLock()
	cancel, done := c.cancel, c.done
	c.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if wait && done != nil {
		<-done
	}
}

func (c *syncController) Status() syncStatus {
	c.mu.RLock()
	client, connected, revoked := c.client, c.connected, c.revoked
	c.mu.RUnlock()
	status := syncStatus{State: "not_connected", Sessions: map[string]string{}}
	if client != nil && client.BaseURL != nil {
		status.HubOrigin = client.BaseURL.Scheme + "://" + client.BaseURL.Host
	}
	if revoked {
		status.State = "disconnected"
		return status
	}
	if !connected {
		return status
	}
	status.State = "connected_idle"
	if !hubclient.V4SyncEnabled() {
		status.State = "auto_sync_off"
		return status
	}
	if c.queue == nil {
		return status
	}
	_, config, _ := c.queue.Policy()
	status.Sessions = c.queue.SessionStates()
	activeSession := false
	for _, state := range status.Sessions {
		if state == "syncing" {
			activeSession = true
			break
		}
	}
	switch {
	case c.queue.UpdatePrompt().Required:
		status.State = "update_required"
	case c.localPause != nil && c.localPause():
		status.State = "paused"
	case config.Paused || config.DeviceOff:
		status.State = "auto_sync_off"
	case !c.LoopIdle() || activeSession:
		status.State = "connected_syncing"
	}
	return status
}

func (c *syncController) observed(runner v4SyncWorker, client *hubclient.Client) v4SyncWorker {
	c.mu.RLock()
	binding := c.binding
	c.mu.RUnlock()
	return observedSyncWorker{runner: runner, controller: c, client: client, binding: binding}
}

type observedSyncWorker struct {
	runner     v4SyncWorker
	controller *syncController
	client     *hubclient.Client
	binding    string
}

func (w observedSyncWorker) SyncOnce(ctx context.Context) error {
	w.controller.passActive.Store(true)
	err := w.runner.SyncOnce(ctx)
	w.controller.passActive.Store(false)
	var problem hubclient.V4Problem
	if errors.As(err, &problem) && problem.Code == "device_revoked" {
		if deleter, ok := w.client.Credentials.(interface{ Delete(context.Context) error }); ok {
			deleteContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if deleteErr := deleter.Delete(deleteContext); deleteErr != nil {
				log.Printf("delete revoked Hub credential: %v", deleteErr)
			}
		}
		w.controller.markRevoked(w.client, w.binding)
	}
	return err
}

func (c *syncController) markRevoked(client *hubclient.Client, binding string) {
	c.mu.Lock()
	if c.client != client || c.binding != binding {
		c.mu.Unlock()
		return
	}
	c.revoked, c.connected = true, false
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
