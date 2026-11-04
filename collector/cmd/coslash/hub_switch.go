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
func (m *onboardingManager) boundElsewhere(origin string) (string, bool, error) {
	stored, err := readStoredHubURL()
	if err != nil {
		return "", false, err
	}
	if stored == "" || stored == origin {
		return "", false, nil
	}
	paired, err := m.hasCredential(stored)
	if err != nil {
		return "", false, err
	}
	return stored, paired, nil
}

func (m *onboardingManager) hasCredential(origin string) (bool, error) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	credential, err := hubCredentialStore(origin).Load(ctx)
	if errors.Is(err, hubclient.ErrNotPaired) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return credential != "", nil
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

// ResolveSwitch performs the pending request for rawHubURL. It drops a declined
// request and consumes a confirmed request only after the requested step succeeds.
func (m *onboardingManager) ResolveSwitch(rawHubURL string, confirm bool) error {
	origin, err := hubOrigin(rawHubURL)
	if err != nil {
		return errNoHubSwitch
	}
	m.mu.Lock()
	if m.pendingSwitch == nil || m.pendingSwitch.To != origin || time.Since(m.pendingSwitch.RequestedAt) > hubSwitchTTL {
		m.mu.Unlock()
		return errNoHubSwitch
	}
	request := *m.pendingSwitch
	m.pendingSwitch = nil
	m.mu.Unlock()
	if !confirm || !request.CanSwitch {
		return nil
	}
	var operationErr error
	if request.action == "pair" {
		operationErr = m.startPairing(request.To, request.attemptID, request.launchToken)
	} else {
		operationErr = m.checkIn(request.To)
	}
	if operationErr == nil {
		return nil
	}
	m.mu.Lock()
	if m.pendingSwitch == nil && time.Since(request.RequestedAt) <= hubSwitchTTL {
		m.pendingSwitch = &request
	}
	m.mu.Unlock()
	return operationErr
}
