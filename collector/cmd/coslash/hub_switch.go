package main

import (
	"context"
	"errors"
	"log"
	"runtime"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

// A link from a Hub other than the one this computer syncs to never switches
// Hubs on its own. Local keeps the request and asks the person in its UI.

const hubSwitchTTL = 10 * time.Minute

var (
	errHubSwitchPending = errors.New("Hub switch needs confirmation")
	errNoHubSwitch      = errors.New("no matching Hub switch request")
)

var hubCredentialStore = func(origin string) hubclient.CredentialStore {
	return deviceCredentialStore(runtime.GOOS, origin)
}

type hubSwitch struct {
	From string `json:"from"`
	To   string `json:"to"`
	// CanSwitch is false when the requesting Hub has no credential on this
	// computer and the request cannot pair it, so only setup in that Hub helps.
	CanSwitch   bool      `json:"canSwitch"`
	RequestedAt time.Time `json:"requestedAt"`

	action      string
	attemptID   string
	launchToken string
}

// boundElsewhere returns the saved Hub when it differs from origin and this
// computer holds a credential for it.
func (m *onboardingManager) boundElsewhere(origin string) (string, bool) {
	stored, err := readStoredHubURL()
	if err != nil || stored == "" || stored == origin {
		return "", false
	}
	return stored, m.hasCredential(stored)
}

func (m *onboardingManager) hasCredential(origin string) bool {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	credential, err := hubCredentialStore(origin).Load(ctx)
	return err == nil && credential != ""
}

func (m *onboardingManager) askSwitch(request hubSwitch) {
	request.RequestedAt = time.Now()
	m.mu.Lock()
	m.pendingSwitch = &request
	show := m.showLocal
	m.mu.Unlock()
	log.Printf("Hub switch requested; waiting for confirmation in coSlash Local")
	if show != nil {
		show()
	}
}

func (m *onboardingManager) PendingSwitch() *hubSwitch {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingSwitch == nil || time.Since(m.pendingSwitch.RequestedAt) > hubSwitchTTL {
		m.pendingSwitch = nil
		return nil
	}
	request := *m.pendingSwitch
	return &request
}

// ResolveSwitch consumes the pending request for rawHubURL. With confirm it
// performs the step that the Hub asked for; otherwise it drops the request.
func (m *onboardingManager) ResolveSwitch(rawHubURL string, confirm bool) error {
	origin, err := hubOrigin(rawHubURL)
	if err != nil {
		return errNoHubSwitch
	}
	m.mu.Lock()
	request := m.pendingSwitch
	if request == nil || request.To != origin || time.Since(request.RequestedAt) > hubSwitchTTL {
		m.mu.Unlock()
		return errNoHubSwitch
	}
	m.pendingSwitch = nil
	m.mu.Unlock()
	if !confirm || !request.CanSwitch {
		return nil
	}
	if request.action == "pair" {
		return m.startPairing(request.To, request.attemptID, request.launchToken)
	}
	return m.checkIn(request.To)
}
