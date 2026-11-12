package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

var errPrimaryHubMismatch = hubclient.ErrPairingBindingMismatch

// blockedPrimarySync prevents legacy pairing and wake paths from starting or
// rebinding sync until the original binding is restored and Local restarts.
type blockedPrimarySync struct{}

func (blockedPrimarySync) Ensure(*hubclient.Client) error  { return errPrimaryHubMismatch }
func (blockedPrimarySync) RequestPass(string)              {}
func (blockedPrimarySync) checkPrimaryTarget(string) error { return errPrimaryHubMismatch }

type primaryHubRuntime struct {
	registry   *hubRegistry
	workers    *hubWorkerSupervisor
	controller *syncController
}

// verifyPrimaryBinding does not rebind the queue; an unknown credential is
// unpaired, while a known different binding must never overwrite pending work.
func verifyPrimaryBinding(client *hubclient.Client, controller *syncController) (bool, error) {
	if client == nil || client.BaseURL == nil || client.Credentials == nil || controller.Queue() == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	credential, err := client.Credentials.Load(ctx)
	if errors.Is(err, hubclient.ErrNotPaired) || (err == nil && credential == "") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	binding, err := (&hubclient.Client{BaseURL: client.BaseURL, Credentials: fixedCredential(credential)}).V4Binding(ctx)
	if err != nil {
		return false, err
	}
	stored, _ := controller.Queue().DiscoveryCursor()
	if stored != "" && stored != binding {
		return false, errPrimaryHubMismatch
	}
	return true, nil
}

func newPrimaryHubRuntime(client *hubclient.Client, controller *syncController, version string, pause func() bool) (*primaryHubRuntime, *hubclient.Client, error) {
	if controller == nil || pause == nil {
		return nil, nil, errors.New("invalid primary Hub runtime")
	}
	legacy := ""
	if client != nil && client.BaseURL != nil {
		legacy = client.BaseURL.String()
	}
	r, err := openHubRegistry(settings.Home(), legacy, func(string) (bool, error) {
		paired, err := verifyPrimaryBinding(client, controller)
		if errors.Is(err, errPrimaryHubMismatch) {
			return false, err
		}
		// An inaccessible keychain is not proof of pairing. The normal startup
		// hook will report the same error without publishing a registry entry.
		return paired, nil
	})
	if err != nil {
		return nil, nil, err
	}
	state := r.snapshot()
	if state.Primary != "" {
		primary, ok := state.connection(state.Primary)
		if !ok {
			return nil, nil, errPrimaryHubMismatch
		}
		expected := primary.clientOrigin()
		if client != nil {
			if client.BaseURL == nil {
				return nil, nil, errPrimaryHubMismatch
			}
			actual, err := canonicalRegistryOrigin(client.BaseURL.String())
			if err != nil || actual != state.Primary {
				return nil, nil, errPrimaryHubMismatch
			}
			if client.BaseURL.String() != expected {
				client, err = hubClientForURL(version, expected)
				if err != nil {
					return nil, nil, err
				}
			}
		}
		if _, err := verifyPrimaryBinding(client, controller); errors.Is(err, errPrimaryHubMismatch) {
			return nil, nil, err
		}
	}
	workers, err := newHubWorkerSupervisor(r, version, pause, sessionbackupproducer.Options{}, func() *syncv4.Runner { return nil })
	if err != nil {
		return nil, nil, err
	}
	workers.primaryOnly = true
	runtime := &primaryHubRuntime{registry: r, workers: workers, controller: controller}
	if state.Primary != "" {
		if err := runtime.adopt(state.Primary); err != nil {
			return nil, nil, err
		}
	}
	return runtime, client, nil
}

// guardPairingCredential prevents legacy pairing routes from replacing a
// credential that owns pending primary work before the binding is checked.
func (p *primaryHubRuntime) guardPairingCredential(client *hubclient.Client) {
	if client == nil || client.BaseURL == nil || client.Credentials == nil {
		return
	}
	if _, ok := client.Credentials.(*primaryPairingCredential); ok {
		return
	}
	origin, err := canonicalRegistryOrigin(client.BaseURL.String())
	if err != nil {
		return
	}
	client.Credentials = &primaryPairingCredential{inner: client.Credentials, base: *client.BaseURL, runtime: p, origin: origin}
}

type primaryPairingCredential struct {
	inner   hubclient.CredentialStore
	base    url.URL
	runtime *primaryHubRuntime
	origin  string
}

func (s *primaryPairingCredential) Load(ctx context.Context) (string, error) {
	return s.inner.Load(ctx)
}

func (s *primaryPairingCredential) Save(ctx context.Context, credential string) error {
	return s.runtime.workers.slot(s.origin).execute(func() error {
		var stored string
		if queue := s.runtime.controller.Queue(); queue != nil {
			stored, _ = queue.DiscoveryCursor()
		} else {
			var err error
			stored, err = syncv4.ReadStoredBinding(filepath.Join(s.runtime.workers.home, "sync-v4"))
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syncv4.ErrQueueUnbound) {
				return err
			}
		}
		if stored != "" {
			base := s.base
			binding, err := (&hubclient.Client{BaseURL: &base, Credentials: fixedCredential(credential)}).V4Binding(ctx)
			if err != nil {
				return err
			}
			if binding != stored {
				s.runtime.controller.mu.Lock()
				s.runtime.controller.pairingMismatch = true
				s.runtime.controller.mu.Unlock()
				return errPrimaryHubMismatch
			}
		}
		if err := s.inner.Save(ctx, credential); err != nil {
			return err
		}
		s.runtime.controller.mu.Lock()
		s.runtime.controller.pairingMismatch = false
		s.runtime.controller.mu.Unlock()
		return nil
	})
}

func (s *primaryPairingCredential) Delete(ctx context.Context) error { return s.inner.Delete(ctx) }

func (s *primaryPairingCredential) DeleteIfMatches(ctx context.Context, credential string) (bool, error) {
	if deleter, ok := s.inner.(hubclient.ConditionalCredentialDeleter); ok {
		return deleter.DeleteIfMatches(ctx, credential)
	}
	return false, nil
}

func (p *primaryHubRuntime) checkPrimaryTarget(origin string) error {
	state := p.registry.snapshot()
	if state.Primary != "" {
		connection, ok := state.connection(state.Primary)
		if !ok || origin != connection.clientOrigin() {
			return errPrimaryHubMismatch
		}
		return nil
	}
	if p.controller.Queue() != nil {
		binding, _ := p.controller.Queue().DiscoveryCursor()
		if binding != "" {
			return errPrimaryHubMismatch
		}
	}
	return nil
}

func (p *primaryHubRuntime) adopt(origin string) error {
	slot := p.workers.slot(origin)
	return slot.execute(func() error {
		if slot.controller != nil && slot.controller != p.controller {
			return errors.New("primary worker already owns another queue")
		}
		slot.controller = p.controller
		return nil
	})
}

// Ensure is the legacy onboarding hook, now routed through the primary's
// supervisor slot once pairing has been verified and committed.
func (p *primaryHubRuntime) Ensure(client *hubclient.Client) error {
	state := p.registry.snapshot()
	ensureLegacy := func() error {
		if state.Primary == "" {
			return p.controller.Ensure(client)
		}
		return p.workers.slot(state.Primary).execute(func() error {
			return p.controller.ensure(context.Background(), client)
		})
	}
	if client == nil || client.BaseURL == nil || client.Credentials == nil {
		return ensureLegacy()
	}
	if state.Primary != "" {
		connection, ok := state.connection(state.Primary)
		if !ok || client.BaseURL.String() != connection.clientOrigin() {
			return errPrimaryHubMismatch
		}
	}
	if !hubclient.V4SyncEnabled() {
		return ensureLegacy()
	}
	paired, err := verifyPrimaryBinding(client, p.controller)
	if err != nil {
		if errors.Is(err, errPrimaryHubMismatch) {
			if state.Primary != "" {
				_ = p.workers.Stop(state.Primary)
			} else {
				p.controller.Stop()
			}
		}
		return err
	}
	if !paired {
		return ensureLegacy()
	}
	origin, err := canonicalRegistryOrigin(client.BaseURL.String())
	if err != nil {
		return err
	}
	if state.Primary == "" {
		if err := p.registry.adoptLegacyPrimary(client.BaseURL.String()); err != nil {
			return err
		}
		state = p.registry.snapshot()
	}
	if state.Primary != origin {
		return errPrimaryHubMismatch
	}
	connection, ok := state.connection(origin)
	if !ok || client.BaseURL.String() != connection.clientOrigin() {
		return errPrimaryHubMismatch
	}
	if !connection.Enabled {
		return errors.New("primary Hub is disabled")
	}
	if err := p.adopt(origin); err != nil {
		return err
	}
	return p.workers.StartEnabled(origin)
}

func (p *primaryHubRuntime) RequestPass(reason string) { p.controller.RequestPass(reason) }

func (p *primaryHubRuntime) hubSignalTarget(raw string, includePrimary bool) (bool, bool, string) {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return true, false, ""
	}
	state := p.registry.snapshot()
	if state.Primary == "" {
		return true, false, origin
	}
	if origin == state.Primary && !includePrimary {
		return false, false, origin
	}
	connection, ok := state.connection(origin)
	return true, ok && connection.Enabled && (origin == state.Primary || connection.SyncConsent), origin
}

func (p *primaryHubRuntime) signalHub(origin string) bool { return p.workers.Retry(origin) }
