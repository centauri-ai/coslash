package main

import (
	"context"
	"errors"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

var errPrimaryHubMismatch = errors.New("primary Hub does not match the existing sync binding")

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
		var primary hubConnection
		for _, c := range state.Connections {
			if c.Origin == state.Primary {
				primary = c
				break
			}
		}
		expected := primary.Origin
		if primary.LegacyOrigin != "" {
			expected = primary.LegacyOrigin
		}
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

func (p *primaryHubRuntime) checkPrimaryTarget(origin string) error {
	state := p.registry.snapshot()
	if state.Primary != "" {
		for _, connection := range state.Connections {
			if connection.Origin != state.Primary {
				continue
			}
			expected := connection.Origin
			if connection.LegacyOrigin != "" {
				expected = connection.LegacyOrigin
			}
			if origin != expected {
				return errPrimaryHubMismatch
			}
			return nil
		}
		return errPrimaryHubMismatch
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
	if client == nil || client.BaseURL == nil || client.Credentials == nil {
		if state.Primary == "" {
			return p.controller.Ensure(client)
		}
		slot := p.workers.slot(state.Primary)
		return slot.execute(func() error { return p.controller.ensure(context.Background(), client) })
	}
	if state.Primary != "" {
		for _, c := range state.Connections {
			if c.Origin != state.Primary {
				continue
			}
			expected := c.Origin
			if c.LegacyOrigin != "" {
				expected = c.LegacyOrigin
			}
			if client.BaseURL.String() != expected {
				return errPrimaryHubMismatch
			}
		}
	}
	if !hubclient.V4SyncEnabled() {
		if state.Primary == "" {
			return p.controller.Ensure(client)
		}
		slot := p.workers.slot(state.Primary)
		return slot.execute(func() error { return p.controller.ensure(context.Background(), client) })
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
		if state.Primary == "" {
			return p.controller.Ensure(client)
		}
		slot := p.workers.slot(state.Primary)
		return slot.execute(func() error { return p.controller.ensure(context.Background(), client) })
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
	for _, c := range state.Connections {
		if c.Origin != origin {
			continue
		}
		expected := c.Origin
		if c.LegacyOrigin != "" {
			expected = c.LegacyOrigin
		}
		if client.BaseURL.String() != expected {
			return errPrimaryHubMismatch
		}
		if !c.Enabled {
			return errors.New("primary Hub is disabled")
		}
		if err := p.adopt(origin); err != nil {
			return err
		}
		return p.workers.StartEnabled(origin)
	}
	return errPrimaryHubMismatch
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
	for _, connection := range state.Connections {
		if connection.Origin == origin {
			return true, connection.Enabled && (origin == state.Primary || connection.SyncConsent), origin
		}
	}
	return true, false, origin
}

func (p *primaryHubRuntime) signalHub(origin string) bool { return p.workers.Retry(origin) }
