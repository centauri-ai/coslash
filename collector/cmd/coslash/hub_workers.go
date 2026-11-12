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
	mu         sync.Mutex
	controller *syncController
	lastError  string
}

func (slot *hubWorkerSlot) execute(run func() error) error {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return run()
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
	ctx         context.Context
	cancel      context.CancelFunc
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
	prepare     func(context.Context, *syncv4.Runner, *syncv4.Queue, *hubclient.Client, chan struct{}) (chan struct{}, func())
}

func newHubWorkerSupervisor(registry *hubRegistry, version string, pause func() bool, backup sessionbackupproducer.Options, runner func() *syncv4.Runner) (*hubWorkerSupervisor, error) {
	if registry == nil || pause == nil || runner == nil {
		return nil, errors.New("invalid Hub worker configuration")
	}
	s := &hubWorkerSupervisor{slots: map[string]*hubWorkerSlot{}, registry: registry, home: settings.Home(), version: version, pause: pause, backup: backup, runner: runner}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.client = s.clientForConnection
	s.poll = func(ctx context.Context, control *syncLoopControl) { runActivePoller(ctx, "", control) }
	return s, nil
}

func (s *hubWorkerSupervisor) clientForConnection(connection hubConnection, primary bool) (*hubclient.Client, error) {
	origin := connection.clientOrigin()
	client, err := hubClientForURL(s.version, origin)
	if err != nil {
		return nil, err
	}
	if !primary {
		if runtime.GOOS == "linux" {
			client.Credentials = hubclient.FileCredentialStore{Path: filepath.Join(s.secondaryRoot(connection), "credential")}
		} else {
			home, err := filepath.Abs(s.home)
			if err != nil {
				return nil, err
			}
			account := fmt.Sprintf("%x:multi:%s", sha256.Sum256([]byte(home)), connection.Origin)
			client.Credentials = hubclient.OSKeychain{Service: "ai.coslash.hub-device", Account: account}
		}
	}
	return client, nil
}

func (s *hubWorkerSupervisor) checkedClient(connection hubConnection, primary bool) (*hubclient.Client, error) {
	client, err := s.client(connection, primary)
	if err != nil {
		return nil, err
	}
	if client == nil || client.BaseURL == nil || client.BaseURL.String() != connection.clientOrigin() || client.Credentials == nil {
		return nil, errors.New("Hub worker client origin mismatch")
	}
	return client, nil
}

func (s *hubWorkerSupervisor) secondaryRoot(connection hubConnection) string {
	return filepath.Join(s.home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(connection.Origin))))
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
	connection, ok := state.connection(origin)
	primary := origin == state.Primary
	if !ok || !connection.Enabled {
		return hubConnection{}, false, errors.New("Hub connection is not enabled")
	}
	return connection, primary, nil
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
	slot := s.slot(origin)
	return slot.execute(func() error { return s.start(slot, origin) })
}

// start runs in the slot's lane. Its context ends only when the supervisor
// stops, so Disable and Stop wait for a start that is already in flight.
func (s *hubWorkerSupervisor) start(slot *hubWorkerSlot, origin string) (err error) {
	startContext := s.ctx
	defer func() {
		slot.lastError = ""
		if err != nil {
			slot.lastError = "start_failed"
		}
	}()
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
	if !primary && !hubclient.V4SyncEnabled() {
		return errors.New("secondary sync is disabled")
	}
	client, err := s.checkedClient(connection, primary)
	if err != nil {
		return err
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
	root := s.secondaryRoot(connection)
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
			s.runWorker(ctx, controller, queue, client, wake, retry, s.runner(), backup, primary)
		}, s.pause)
		if err != nil {
			return err
		}
		slot.controller = controller
	}
	// The primary never rebinds a bound queue. A secondary is re-paired only by
	// an explicit Local action, so a new binding resets its upload state.
	if primary && slot.controller.Queue() != nil {
		stored, _ := slot.controller.Queue().DiscoveryCursor()
		if stored != "" && stored != binding {
			slot.stopWorker()
			return errors.New("Hub queue binding mismatch")
		}
	}
	if err := slot.controller.ensure(startContext, pinned); err != nil {
		if errors.Is(err, errHubBindingChanged) || errors.Is(err, hubclient.ErrNotPaired) {
			slot.stopWorker()
		}
		return err
	}
	if !primary {
		slot.controller.mu.RLock()
		connected := slot.controller.connected && slot.controller.done != nil
		slot.controller.mu.RUnlock()
		if !connected {
			return errors.New("secondary worker did not start")
		}
	}
	return nil
}

func (s *hubWorkerSupervisor) runWorker(ctx context.Context, controller *syncController, queue *syncv4.Queue, client *hubclient.Client, wake, retry chan struct{}, runner *syncv4.Runner, backup *sessionbackupproducer.Manager, primary bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	controller.mu.RLock()
	binding := controller.binding
	controller.mu.RUnlock()
	pinned := bindingPinnedClient(client, binding)
	transport := currentHubTransport{current: func() *hubclient.Client { return pinned }, queue: queue}
	runner.Version, runner.Queue, runner.Backup, runner.Hub = s.version, queue, backup, transport
	runner.NoSharedAuthority, runner.LocalPause, runner.CommandContext = !primary, s.pause, ctx
	runner.RequireImportPlan = true
	var inventoryRequest chan struct{}
	if s.prepare != nil {
		var waitInventory func()
		inventoryRequest, waitInventory = s.prepare(ctx, runner, queue, pinned, wake)
		defer func() {
			cancel()
			if waitInventory != nil {
				waitInventory()
			}
		}()
	}
	control := newSyncLoopControl(func() bool {
		_, policy, _ := queue.Policy()
		return policy.Paused || policy.DeviceOff || s.pause()
	})
	go s.poll(ctx, control)
	loop := func() {
		runV4SyncLoopWithControl(ctx, controller.observed(runner, client), queue, controller.observedWait(transport.V4Wait, client), control, wake, retry, inventoryRequest)
	}
	if primary {
		controller.runWithUpdateReporter(ctx, s.version, queue, client, loop)
	} else {
		loop()
	}
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
	return slot.execute(func() error {
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

type hubWorkerStatus struct {
	Origin  string `json:"origin"`
	Primary bool   `json:"primary"`
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	Failure string `json:"failure,omitempty"`
}

func (s *hubWorkerSupervisor) statuses() []hubWorkerStatus {
	registry := s.registry.snapshot()
	result := make([]hubWorkerStatus, 0, len(registry.Connections))
	for _, c := range registry.Connections {
		status := hubWorkerStatus{Origin: c.Origin, Primary: c.Origin == registry.Primary, Enabled: c.Enabled, State: "disabled"}
		if c.Enabled {
			status.State = "not_running"
			slot := s.slot(c.Origin)
			_ = slot.execute(func() error {
				if slot.lastError != "" {
					status.State = slot.lastError
				} else if slot.controller != nil {
					view := slot.controller.Status()
					status.State = view.State
					if view.LastFailure != nil {
						status.Failure = view.LastFailure.Sentence
					}
				}
				return nil
			})
		}
		result = append(result, status)
	}
	return result
}

// Disable serializes with StartEnabled so a disabled destination cannot be
// restarted by an overlapping start. Direct registry disables must not bypass it.
func (s *hubWorkerSupervisor) Disable(raw string) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	state := s.registry.snapshot()
	if origin == state.Primary {
		return errors.New("cannot disable primary Hub")
	}
	if _, found := state.connection(origin); !found {
		return errors.New("unknown Hub")
	}
	slot := s.slot(origin)
	return slot.execute(func() error {
		slot.stopWorker()
		slot.lastError = ""
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
	s.mu.Unlock()
	s.cancel()
	var wait sync.WaitGroup
	for _, slot := range s.allSlots() {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = slot.execute(func() error {
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
