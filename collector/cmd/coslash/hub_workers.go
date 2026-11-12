package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type hubWorkerSlot struct {
	mu          sync.Mutex
	pending     []hubWorkerOperation
	running     bool
	startMu     sync.Mutex
	startCancel context.CancelFunc
	controller  *syncController
}

type hubWorkerOperation struct {
	run  func() error
	done chan error
}

func (slot *hubWorkerSlot) execute(run func() error) error {
	return slot.enqueue(run, false)
}

func (slot *hubWorkerSlot) executeControl(run func() error) error {
	return slot.enqueue(run, true)
}

func (slot *hubWorkerSlot) enqueue(run func() error, control bool) error {
	op := hubWorkerOperation{run: run, done: make(chan error, 1)}
	slot.mu.Lock()
	if len(slot.pending) >= 128 && !control {
		slot.mu.Unlock()
		return errors.New("too many pending Hub worker operations")
	}
	if control {
		slot.pending = append([]hubWorkerOperation{op}, slot.pending...)
	} else {
		slot.pending = append(slot.pending, op)
	}
	if !slot.running {
		slot.running = true
		go slot.runOperations()
	}
	slot.mu.Unlock()
	return <-op.done
}

func (slot *hubWorkerSlot) runOperations() {
	for {
		slot.mu.Lock()
		if len(slot.pending) == 0 {
			slot.running = false
			slot.mu.Unlock()
			return
		}
		op := slot.pending[0]
		slot.pending[0] = hubWorkerOperation{}
		slot.pending = slot.pending[1:]
		slot.mu.Unlock()
		op.done <- op.run()
	}
}

func (slot *hubWorkerSlot) cancelStart() {
	slot.startMu.Lock()
	if slot.startCancel != nil {
		slot.startCancel()
	}
	slot.startMu.Unlock()
}

func (slot *hubWorkerSlot) stopWorker() {
	if slot.controller == nil {
		return
	}
	slot.controller.stopLoop(true)
	slot.controller.mu.Lock()
	slot.controller.connected = false
	slot.controller.mu.Unlock()
}

// hubWorkerSupervisor has one independently serialized lifecycle per origin.
// Its map lock only protects the map, never credentials, queue I/O or waits.
type hubWorkerSupervisor struct {
	mu          sync.Mutex
	slots       map[string]*hubWorkerSlot
	closed      bool
	primaryOnly bool
	registry    *hubRegistry
	home        string
	version     string
	pause       func() bool
	backup      sessionbackupproducer.Options
	runner      func() *syncv4.Runner
	client      func(hubConnection, bool) (*hubclient.Client, error)
	poll        func(context.Context, *syncLoopControl)
}

func newHubWorkerSupervisor(registry *hubRegistry, version string, pause func() bool, backup sessionbackupproducer.Options, runner func() *syncv4.Runner) (*hubWorkerSupervisor, error) {
	if registry == nil || pause == nil || runner == nil {
		return nil, errors.New("invalid Hub worker configuration")
	}
	s := &hubWorkerSupervisor{slots: map[string]*hubWorkerSlot{}, registry: registry, home: settings.Home(), version: version, pause: pause, backup: backup, runner: runner}
	s.client = s.clientForConnection
	s.poll = func(ctx context.Context, control *syncLoopControl) { runActivePoller(ctx, "", control) }
	return s, nil
}

func (s *hubWorkerSupervisor) clientForConnection(connection hubConnection, primary bool) (*hubclient.Client, error) {
	origin := connection.Origin
	if primary && connection.LegacyOrigin != "" {
		origin = connection.LegacyOrigin
	}
	client, err := hubClientForURL(s.version, origin)
	if err != nil {
		return nil, err
	}
	if !primary {
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(connection.Origin)))
		if runtime.GOOS == "linux" {
			client.Credentials = hubclient.FileCredentialStore{Path: filepath.Join(s.home, "hub-connections", key, "credential")}
		} else {
			home, err := filepath.Abs(s.home)
			if err != nil {
				return nil, err
			}
			client.Credentials = hubclient.OSKeychain{Service: "ai.coslash.hub-device", Account: fmt.Sprintf("%x:multi:%s", sha256.Sum256([]byte(home)), connection.Origin)}
		}
	}
	return client, nil
}

func (s *hubWorkerSupervisor) slot(origin string) *hubWorkerSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots[origin] == nil {
		s.slots[origin] = &hubWorkerSlot{}
	}
	return s.slots[origin]
}

func (s *hubWorkerSupervisor) enabled(origin string) (hubConnection, bool, error) {
	state := s.registry.snapshot()
	for _, connection := range state.Connections {
		if connection.Origin == origin && connection.Enabled && (origin == state.Primary || connection.SyncConsent) {
			return connection, origin == state.Primary, nil
		}
	}
	return hubConnection{}, false, errors.New("Hub connection is not enabled")
}

// StartEnabled starts only an already enabled registry entry. Pairing and the
// explicit consent which permits enabling a secondary are owned by the caller.
func (s *hubWorkerSupervisor) StartEnabled(raw string) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	if _, primary, err := s.enabled(origin); err != nil {
		return err
	} else if s.primaryOnly && !primary {
		return errors.New("secondary Hub workers are not available")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("Hub supervisor stopped")
	}
	slot := s.slots[origin]
	if slot == nil {
		slot = &hubWorkerSlot{}
		s.slots[origin] = slot
	}
	s.mu.Unlock()
	slot.startMu.Lock()
	if slot.startCancel != nil {
		slot.startCancel()
	}
	startContext, cancelStart := context.WithCancel(context.Background())
	slot.startCancel = cancelStart
	slot.startMu.Unlock()
	defer cancelStart()
	return slot.execute(func() error { return s.start(slot, origin, startContext) })
}

func (s *hubWorkerSupervisor) start(slot *hubWorkerSlot, origin string, startContext context.Context) error {
	if err := startContext.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("Hub supervisor stopped")
	}
	connection, primary, err := s.enabled(origin)
	if err != nil {
		return err
	}
	client, err := s.client(connection, primary)
	if err != nil {
		return err
	}
	expected := origin
	if primary && connection.LegacyOrigin != "" {
		expected = connection.LegacyOrigin
	}
	if client == nil || client.BaseURL == nil || client.BaseURL.String() != expected || client.Credentials == nil {
		return errors.New("Hub worker client origin mismatch")
	}
	ctx, cancel := context.WithTimeout(startContext, 5*time.Second)
	binding, err := client.V4Binding(ctx)
	cancel()
	if err != nil {
		if errors.Is(err, hubclient.ErrNotPaired) && slot.controller != nil {
			slot.stopWorker()
		}
		return err
	}
	pinned := bindingPinnedClient(client, binding)
	root := filepath.Join(s.home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(origin))))
	queueRoot, spoolRoot := filepath.Join(root, "sync-v4"), filepath.Join(root, "prepared")
	if primary {
		queueRoot, spoolRoot = filepath.Join(s.home, "sync-v4"), filepath.Join(s.home, "session-backups", "prepared")
	}
	if s.primaryOnly && slot.controller == nil {
		return errors.New("primary worker was not adopted")
	}
	if slot.controller == nil {
		var controller *syncController
		controller, err = newSyncControllerAt(queueRoot, func(ctx context.Context, queue *syncv4.Queue, client *hubclient.Client, wake, retry chan struct{}) {
			options := s.backup
			options.Root, options.Lifecycle = spoolRoot, ctx
			if !primary {
				options.Remote = nil
			}
			backup := sessionbackupproducer.New(options)
			defer backup.Close()
			runner := s.runner()
			runner.Version, runner.Queue, runner.Backup, runner.Hub = s.version, queue, backup, currentHubTransport{current: func() *hubclient.Client { return client }, queue: queue}
			runner.NoSharedAuthority, runner.LocalPause, runner.CommandContext = !primary, s.pause, ctx
			runner.RequireImportPlan = true
			control := newSyncLoopControl(func() bool { _, policy, _ := queue.Policy(); return policy.Paused || policy.DeviceOff || s.pause() })
			go s.poll(ctx, control)
			loop := func() {
				runV4SyncLoopWithControl(ctx, controller.observed(runner, client), queue, controller.observedWait(client.V4Wait, client), control, wake, retry, nil)
			}
			if primary {
				controller.runWithUpdateReporter(ctx, s.version, queue, client, loop)
			} else {
				loop()
			}
		}, s.pause)
		if err != nil {
			return err
		}
		slot.controller = controller
	}
	if slot.controller.Queue() != nil {
		stored, _ := slot.controller.Queue().DiscoveryCursor()
		if stored != "" && stored != binding {
			slot.stopWorker()
			return errors.New("Hub queue binding mismatch")
		}
	}
	if _, _, err := s.enabled(origin); err != nil {
		return err
	}
	if err := startContext.Err(); err != nil {
		return err
	}
	if err := slot.controller.ensure(startContext, pinned); err != nil {
		if errors.Is(err, errHubBindingChanged) || errors.Is(err, hubclient.ErrNotPaired) {
			slot.stopWorker()
		}
		return err
	}
	if err := startContext.Err(); err != nil {
		slot.stopWorker()
		return err
	}
	return nil
}

func (s *hubWorkerSupervisor) Stop(raw string) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	slot := s.slots[origin]
	s.mu.Unlock()
	if slot == nil {
		return errors.New("Hub worker is not running")
	}
	slot.cancelStart()
	return slot.executeControl(func() error {
		slot.stopWorker()
		return nil
	})
}

func (s *hubWorkerSupervisor) Retry(raw string) bool {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return false
	}
	s.mu.Lock()
	slot := s.slots[origin]
	s.mu.Unlock()
	if slot == nil {
		return false
	}
	var retried bool
	err = slot.execute(func() error {
		if _, _, err := s.enabled(origin); err != nil {
			return err
		}
		retried = slot.controller != nil && slot.controller.RetryNow()
		return nil
	})
	return err == nil && retried
}

// Disable serializes with StartEnabled so a disabled destination cannot be
// restarted by an overlapping start. Direct registry disables must not bypass it.
func (s *hubWorkerSupervisor) Disable(raw string) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	if _, _, err := s.enabled(origin); err != nil {
		return err
	}
	slot := s.slot(origin)
	slot.cancelStart()
	return slot.executeControl(func() error {
		slot.stopWorker()
		return s.registry.setEnabled(origin, false)
	})
}

// WakeAll applies global Local settings changes to every worker independently.
func (s *hubWorkerSupervisor) WakeAll() {
	var wait sync.WaitGroup
	for _, slot := range s.allSlots() {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = slot.execute(func() error {
				if slot.controller != nil {
					slot.controller.RequestPass("settings")
				}
				return nil
			})
		}()
	}
	wait.Wait()
}

func (s *hubWorkerSupervisor) StopAll() {
	s.mu.Lock()
	s.closed = true
	slots := make([]*hubWorkerSlot, 0, len(s.slots))
	for _, slot := range s.slots {
		slots = append(slots, slot)
	}
	s.mu.Unlock()
	var wait sync.WaitGroup
	for _, slot := range slots {
		slot.cancelStart()
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = slot.executeControl(func() error {
				slot.stopWorker()
				return nil
			})
		}()
	}
	wait.Wait()
}

func (s *hubWorkerSupervisor) allSlots() []*hubWorkerSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	slots := make([]*hubWorkerSlot, 0, len(s.slots))
	for _, slot := range s.slots {
		slots = append(slots, slot)
	}
	return slots
}
