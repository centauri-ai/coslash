package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

// stagedCredential holds an approved credential in memory until the pairing is
// committed, so an abandoned attempt never reaches the credential store.
type stagedCredential struct {
	mu    sync.Mutex
	value string
}

func (s *stagedCredential) Load(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value == "" {
		return "", hubclient.ErrNotPaired
	}
	return s.value, nil
}

func (s *stagedCredential) Save(_ context.Context, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = value
	return nil
}

func (s *stagedCredential) Delete(ctx context.Context) error { return s.Save(ctx, "") }

type secondaryAttempt struct {
	origin  string
	client  *hubclient.Client
	staged  *stagedCredential
	pairing hubclient.PairingResult
	busy    bool
}

func (a *secondaryAttempt) expired(ctx context.Context) bool {
	return ctx.Err() != nil || !time.Now().Before(a.pairing.ExpiresAt)
}

type secondaryPairings struct {
	mu       sync.Mutex
	workers  *hubWorkerSupervisor
	attempts map[string]*secondaryAttempt
	origins  map[string]string
}

func newSecondaryPairings(workers *hubWorkerSupervisor) *secondaryPairings {
	return &secondaryPairings{workers: workers, attempts: map[string]*secondaryAttempt{}, origins: map[string]string{}}
}

func (m *secondaryPairings) credentialMissing(ctx context.Context, client *hubclient.Client) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := client.Credentials.Load(ctx)
	return errors.Is(err, hubclient.ErrNotPaired)
}

// begin starts pairing a new secondary, or re-pairing a known one whose
// credential is gone. Re-pairing disables the connection first, so sync to it
// needs fresh consent.
func (m *secondaryPairings) begin(ctx context.Context, raw string, consent bool) (string, hubclient.PairingResult, error) {
	if !consent {
		return "", hubclient.PairingResult{}, errors.New("explicit Local consent required")
	}
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil || !strings.HasPrefix(origin, "https://") {
		return "", hubclient.PairingResult{}, errors.New("invalid secondary Hub")
	}
	state := m.workers.registry.snapshot()
	if state.Primary == "" || origin == state.Primary {
		return "", hubclient.PairingResult{}, errors.New("invalid secondary pairing target")
	}
	_, exists := state.connection(origin)
	id, err := newConnectJobID()
	if err != nil {
		return "", hubclient.PairingResult{}, err
	}
	attempt := &secondaryAttempt{origin: origin, busy: true, staged: &stagedCredential{}}
	m.mu.Lock()
	for previousID, previous := range m.attempts {
		if !previous.busy && !time.Now().Before(previous.pairing.ExpiresAt) {
			delete(m.attempts, previousID)
			delete(m.origins, previous.origin)
		}
	}
	if len(m.attempts) >= 100 || m.origins[origin] != "" {
		m.mu.Unlock()
		return "", hubclient.PairingResult{}, errors.New("Hub pairing already pending")
	}
	m.attempts[id] = attempt
	m.origins[origin] = id
	m.mu.Unlock()
	client, err := m.workers.checkedClient(hubConnection{Origin: origin}, false)
	if err == nil && exists {
		if !m.credentialMissing(ctx, client) {
			err = errors.New("Hub is already paired")
		} else {
			err = m.workers.Disable(origin)
		}
	}
	if err == nil {
		client.Credentials = attempt.staged
		attempt.client = client
		attempt.pairing, err = client.BeginPairing(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt.busy = false
	if err != nil {
		delete(m.attempts, id)
		delete(m.origins, origin)
		return "", hubclient.PairingResult{}, err
	}
	return id, attempt.pairing, nil
}

func (m *secondaryPairings) poll(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	attempt := m.attempts[id]
	if attempt == nil || attempt.busy {
		m.mu.Unlock()
		return "", errors.New("pairing unavailable")
	}
	attempt.busy = true
	m.mu.Unlock()
	terminal := false
	defer func() {
		m.mu.Lock()
		attempt.busy = false
		if terminal {
			delete(m.attempts, id)
			delete(m.origins, attempt.origin)
		}
		m.mu.Unlock()
	}()
	if attempt.expired(context.Background()) {
		terminal = true
		return "expired", nil
	}
	result, err := attempt.client.PollPairing(ctx, attempt.pairing.PairingID)
	if err != nil {
		if ctx.Err() != nil {
			terminal = true
			return "cancelled", nil
		}
		return "retrying", nil
	}
	switch result.State {
	case hubclient.PairingStateDeclined, hubclient.PairingStateExpired, hubclient.PairingStateCredentialStoreFailed:
		terminal = true
		return result.State, nil
	case hubclient.PairingStatePaired:
		terminal = true
		if attempt.expired(ctx) {
			return "expired", nil
		}
		if m.commit(ctx, attempt) != nil {
			return "failed", nil
		}
		return "paired_disabled", nil
	default:
		return result.State, nil
	}
}

// commit stores the approved credential in the origin's lifecycle lane and
// leaves the connection disabled. A known connection keeps its queue; if the
// new credential has another binding, the next start resets its upload state.
func (m *secondaryPairings) commit(ctx context.Context, attempt *secondaryAttempt) error {
	credential, err := attempt.staged.Load(ctx)
	if err != nil {
		return err
	}
	slot := m.workers.slot(attempt.origin)
	return slot.execute(func() error {
		connection, exists := m.workers.registry.snapshot().connection(attempt.origin)
		client, err := m.workers.checkedClient(hubConnection{Origin: attempt.origin}, false)
		if err != nil {
			return err
		}
		if exists && (connection.Enabled || !m.credentialMissing(ctx, client)) {
			return errors.New("pairing target changed")
		}
		if err := client.Credentials.Save(ctx, credential); err != nil {
			return err
		}
		if !exists {
			return m.workers.registry.addSecondary(attempt.origin)
		}
		if slot.controller != nil {
			slot.controller.mu.Lock()
			slot.controller.client, slot.controller.connected, slot.controller.revoked = nil, false, false
			slot.controller.mu.Unlock()
		}
		return nil
	})
}

// enable records consent to sync personal sessions to a paired secondary and
// starts its worker.
func (m *secondaryPairings) enable(ctx context.Context, raw string, consent bool) error {
	if !consent {
		return errors.New("explicit sync consent required")
	}
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil || m.workers.primaryOnly || !hubclient.V4SyncEnabled() {
		return errors.New("secondary enable unavailable")
	}
	slot := m.workers.slot(origin)
	return slot.execute(func() error {
		state := m.workers.registry.snapshot()
		connection, found := state.connection(origin)
		if !found || origin == state.Primary {
			return errors.New("secondary Hub is not paired")
		}
		client, err := m.workers.checkedClient(connection, false)
		if err != nil || m.credentialMissing(ctx, client) {
			return errors.New("secondary credential unavailable")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.workers.registry.setEnabled(origin, true); err != nil {
			return err
		}
		return m.workers.start(slot, origin)
	})
}

func registerSecondaryPairingRoutes(api *http.ServeMux, pairings *secondaryPairings) {
	type originInput struct {
		Origin string `json:"origin"`
	}
	type consentInput struct {
		Origin  string `json:"origin"`
		Consent bool   `json:"consent"`
	}
	type noInput struct{}
	state := func(value string, err error) (any, error) { return map[string]string{"state": value}, err }
	registerSecondaryAction(api, pairings, "GET /api/hubs/status", "Hub status is unavailable.", http.StatusOK,
		func(*http.Request, noInput) (any, error) { return pairings.workers.statuses(), nil })
	registerSecondaryAction(api, pairings, "POST /api/hubs/disable", "This Hub could not be disabled.", http.StatusOK,
		func(_ *http.Request, input originInput) (any, error) {
			return state("disabled", pairings.workers.Disable(input.Origin))
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/retry", "This Hub could not be started.", http.StatusOK,
		func(_ *http.Request, input originInput) (any, error) {
			origin, err := canonicalRegistryOrigin(input.Origin)
			if err != nil || origin == pairings.workers.registry.snapshot().Primary {
				return nil, errors.New("secondary Hub unavailable")
			}
			if err := pairings.workers.StartEnabled(origin); err != nil {
				return nil, err
			}
			pairings.workers.Retry(origin)
			return state("started", nil)
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/enable", "This Hub could not be enabled. If its pairing was removed, pair it again first.", http.StatusOK,
		func(r *http.Request, input consentInput) (any, error) {
			return state("started", pairings.enable(r.Context(), input.Origin, input.Consent))
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/pairings", "Pairing could not be started. A Hub that is still paired cannot be paired again.", http.StatusCreated,
		func(r *http.Request, input consentInput) (any, error) {
			id, result, err := pairings.begin(r.Context(), input.Origin, input.Consent)
			return map[string]any{"id": id, "pairing": result}, err
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/pairings/{id}/poll", "This pairing attempt is no longer available.", http.StatusOK,
		func(r *http.Request, _ noInput) (any, error) {
			return state(pairings.poll(r.Context(), r.PathValue("id")))
		})
}

func registerSecondaryAction[T any](api *http.ServeMux, pairings *secondaryPairings, pattern, failure string, status int, action func(*http.Request, T) (any, error)) {
	api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "Hub connections are unavailable", http.StatusServiceUnavailable)
			return
		}
		var input T
		if r.Method == http.MethodPost && r.ContentLength != 0 && decodeHubJSON(r.Body, &input) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		result, err := action(r, input)
		if err != nil {
			http.Error(w, failure, http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		writeJSON(w, result)
	})
}
