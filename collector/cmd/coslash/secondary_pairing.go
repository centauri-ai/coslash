package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

const secondaryDisclosure = "Pairing this Hub stores a device credential but does not authorize or start personal sync. Enabling sync requires separate consent. Workspace sharing requires separate consent."
const secondaryEnableDisclosure = "Enabling this Hub starts sync and authorizes automatic upload of eligible personal session records, including prompts, commands and source snippets, to this destination under its policy and Local privacy settings. This consent persists across restarts. Workspace sharing requires separate consent."
const secondaryRecoveryDisclosure = "Recovery stops this secondary and keeps its pending queue and prepared data. An approved credential is restored only if it matches the existing queue binding. A different credential requires a separate migration; sync remains disabled."

func runSecondaryPairCLI(out io.Writer, args []string) error {
	if len(args) == 2 && args[0] == "recover" {
		fmt.Fprintln(out, secondaryRecoveryDisclosure)
		fmt.Fprintf(out, "To recover %s, repeat with --consent.\n", args[1])
		return nil
	}
	if len(args) == 3 && args[0] == "recover" && args[2] == "--consent" {
		fmt.Fprintln(out, secondaryRecoveryDisclosure)
		client, err := newLocalAPIClient()
		if err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"origin": args[1], "consent": true})
		response, err := client.request(http.MethodPost, "/api/hubs/re-pairings", bytes.NewReader(data))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(response))
		return err
	}
	if len(args) == 1 && args[0] == "status" {
		client, err := newLocalAPIClient()
		if err != nil {
			return err
		}
		data, err := client.request(http.MethodGet, "/api/hubs/status", nil)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	if len(args) == 2 && (args[0] == "disable" || args[0] == "retry") {
		client, err := newLocalAPIClient()
		if err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]string{"origin": args[1]})
		response, err := client.request(http.MethodPost, "/api/hubs/"+args[0], bytes.NewReader(data))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(response))
		return err
	}
	if len(args) == 2 && args[0] == "enable" {
		fmt.Fprintln(out, secondaryEnableDisclosure)
		fmt.Fprintf(out, "To enable %s, repeat with --consent.\n", args[1])
		return nil
	}
	if len(args) == 3 && args[0] == "enable" && args[2] == "--consent" {
		fmt.Fprintln(out, secondaryEnableDisclosure)
		client, err := newLocalAPIClient()
		if err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"origin": args[1], "consent": true})
		response, err := client.request(http.MethodPost, "/api/hubs/enable", bytes.NewReader(data))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(response))
		return err
	}
	if len(args) == 1 {
		fmt.Fprintln(out, secondaryDisclosure)
		fmt.Fprintf(out, "To consent for %s, repeat with --consent.\n", args[0])
		return nil
	}
	if len(args) != 2 {
		return errors.New("usage: coslash secondary-pair <origin> [--consent] | coslash secondary-pair poll <attempt-id>")
	}
	client, err := newLocalAPIClient()
	if err != nil {
		return err
	}
	var path string
	var body io.Reader
	if args[0] == "poll" {
		decoded, err := hex.DecodeString(args[1])
		if err != nil || len(decoded) != 16 {
			return errors.New("invalid pairing attempt")
		}
		path = "/api/hubs/pairings/" + args[1] + "/poll"
	} else if args[1] == "--consent" {
		fmt.Fprintln(out, secondaryDisclosure)
		data, _ := json.Marshal(map[string]any{"origin": args[0], "consent": true})
		body = bytes.NewReader(data)
		path = "/api/hubs/pairings"
	} else {
		return errors.New("explicit --consent is required")
	}
	data, err := client.request(http.MethodPost, path, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

type stagedCredential struct {
	mu    sync.Mutex
	value string
}

func (s *stagedCredential) Load(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.value == "" {
		return "", hubclient.ErrNotPaired
	}
	return s.value, nil
}
func (s *stagedCredential) Save(ctx context.Context, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if v == "" {
		return errors.New("empty credential")
	}
	s.value = v
	return nil
}
func (s *stagedCredential) Delete(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.value = ""
	return nil
}

type secondaryAttempt struct {
	origin   string
	client   *hubclient.Client
	staged   *stagedCredential
	pairing  hubclient.PairingResult
	busy     bool
	recovery bool
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

func (m *secondaryPairings) begin(ctx context.Context, raw string, consent bool) (string, hubclient.PairingResult, error) {
	return m.beginAttempt(ctx, raw, consent, false)
}

func (m *secondaryPairings) beginAttempt(ctx context.Context, raw string, consent, recovery bool) (string, hubclient.PairingResult, error) {
	if !consent {
		return "", hubclient.PairingResult{}, errors.New("explicit Local consent required")
	}
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil || len(origin) < 8 || origin[:8] != "https://" {
		return "", hubclient.PairingResult{}, errors.New("invalid secondary Hub")
	}
	state := m.workers.registry.snapshot()
	if state.Primary == "" {
		return "", hubclient.PairingResult{}, errors.New("primary Hub required")
	}
	exists := false
	for _, c := range state.Connections {
		if c.Origin == origin {
			exists = true
			break
		}
	}
	if origin == state.Primary || exists != recovery {
		return "", hubclient.PairingResult{}, errors.New("invalid secondary pairing target")
	}
	id, err := newConnectJobID()
	if err != nil {
		return "", hubclient.PairingResult{}, err
	}
	attempt := &secondaryAttempt{origin: origin, busy: true, recovery: recovery}
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
	client, err := m.workers.client(hubConnection{Origin: origin}, false)
	if err == nil && (client == nil || client.BaseURL == nil || client.BaseURL.String() != origin || client.Credentials == nil) {
		err = errors.New("invalid secondary Hub client")
	}
	if err == nil && recovery {
		loadContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, loadErr := client.Credentials.Load(loadContext)
		cancel()
		if !errors.Is(loadErr, hubclient.ErrNotPaired) {
			err = errors.New("recovery requires a missing credential")
		} else {
			err = m.workers.Disable(origin)
		}
	}
	if err == nil {
		attempt.staged = &stagedCredential{}
		client.Credentials = attempt.staged
		attempt.client = client
		attempt.pairing, err = client.BeginPairing(ctx)
	}
	m.mu.Lock()
	attempt.busy = false
	if err != nil {
		delete(m.attempts, id)
		delete(m.origins, origin)
	}
	result := attempt.pairing
	m.mu.Unlock()
	if err != nil {
		return "", hubclient.PairingResult{}, err
	}
	return id, result, nil
}

func (m *secondaryPairings) poll(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	attempt := m.attempts[id]
	if attempt == nil {
		m.mu.Unlock()
		return "", errors.New("unknown pairing")
	}
	if attempt.busy {
		m.mu.Unlock()
		return "", errors.New("pairing in progress")
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
	if !time.Now().Before(attempt.pairing.ExpiresAt) {
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
		if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
			return "expired", nil
		}
		credential, err := attempt.staged.Load(ctx)
		if err != nil {
			return "failed", nil
		}
		if attempt.recovery {
			return m.commitRecovery(ctx, attempt, credential), nil
		}
		state := m.workers.registry.snapshot()
		for _, connection := range state.Connections {
			if connection.Origin == attempt.origin {
				return "failed", nil
			}
		}
		client, err := m.workers.client(hubConnection{Origin: attempt.origin}, false)
		if err != nil || client == nil || client.BaseURL == nil || client.BaseURL.String() != attempt.origin || client.Credentials == nil {
			return "failed", nil
		}
		if err = client.Credentials.Save(ctx, credential); err != nil {
			return "failed", nil
		}
		if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
			return "expired", nil
		}
		if err = m.workers.registry.addSecondary(attempt.origin); err != nil {
			return "failed", nil
		}
		return "paired_disabled", nil
	default:
		return result.State, nil
	}
}

func (m *secondaryPairings) commitRecovery(ctx context.Context, attempt *secondaryAttempt, credential string) string {
	result := "failed"
	slot := m.workers.slot(attempt.origin)
	err := slot.executeControl(func() error {
		if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
			result = "expired"
			return nil
		}
		state := m.workers.registry.snapshot()
		found := false
		for _, c := range state.Connections {
			if c.Origin == attempt.origin && c.Origin != state.Primary && !c.Enabled && !c.SyncConsent {
				found = true
				break
			}
		}
		if !found {
			return errors.New("recovery target changed")
		}
		client, err := m.workers.client(hubConnection{Origin: attempt.origin}, false)
		if err != nil || client == nil || client.BaseURL == nil || client.BaseURL.String() != attempt.origin || client.Credentials == nil {
			return errors.New("recovery client unavailable")
		}
		loadContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = client.Credentials.Load(loadContext)
		cancel()
		if !errors.Is(err, hubclient.ErrNotPaired) {
			return errors.New("recovery credential changed")
		}
		var stored string
		if slot.controller != nil && slot.controller.Queue() != nil {
			stored, _ = slot.controller.Queue().DiscoveryCursor()
		} else {
			root := filepath.Join(m.workers.home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(attempt.origin))), "sync-v4")
			stored, err = syncv4.ReadStoredBinding(root)
			if err != nil {
				result = "binding_unknown"
				return nil
			}
		}
		binding, err := (&hubclient.Client{BaseURL: client.BaseURL, Credentials: fixedCredential(credential)}).V4Binding(ctx)
		if err != nil {
			return err
		}
		if stored == "" {
			result = "binding_unknown"
			return nil
		}
		if stored != binding {
			result = "binding_mismatch"
			return nil
		}
		if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
			result = "expired"
			return nil
		}
		if err := client.Credentials.Save(ctx, credential); err != nil {
			return err
		}
		if slot.controller != nil {
			slot.controller.mu.Lock()
			slot.controller.client = nil
			slot.controller.connected = false
			slot.controller.revoked = false
			slot.controller.mu.Unlock()
		}
		result = "recovered_disabled"
		return nil
	})
	if err != nil {
		return "failed"
	}
	return result
}

func (m *secondaryPairings) enable(ctx context.Context, raw string, consent bool) error {
	if !consent {
		return errors.New("explicit sync consent required")
	}
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil || m.workers.primaryOnly || !hubclient.V4SyncEnabled() {
		return errors.New("secondary enable unavailable")
	}
	state := m.workers.registry.snapshot()
	if origin == state.Primary {
		return errors.New("secondary enable unavailable")
	}
	found := false
	for _, c := range state.Connections {
		if c.Origin == origin {
			found = true
			break
		}
	}
	if !found {
		return errors.New("secondary Hub is not paired")
	}
	slot := m.workers.slot(origin)
	slot.startMu.Lock()
	if slot.disabling {
		slot.startMu.Unlock()
		return errors.New("Hub is being disabled")
	}
	if slot.startCancel != nil {
		slot.startCancel()
	}
	startContext, cancelStart := context.WithCancel(ctx)
	slot.startCancel = cancelStart
	slot.startMu.Unlock()
	defer cancelStart()
	return slot.execute(func() error {
		if err := startContext.Err(); err != nil {
			return err
		}
		state := m.workers.registry.snapshot()
		found := false
		for _, c := range state.Connections {
			if c.Origin == origin && origin != state.Primary {
				found = true
				break
			}
		}
		if !found {
			return errors.New("secondary Hub is not paired")
		}
		client, err := m.workers.client(hubConnection{Origin: origin}, false)
		if err != nil || client == nil || client.BaseURL == nil || client.BaseURL.String() != origin || client.Credentials == nil {
			return errors.New("secondary credential unavailable")
		}
		loadContext, cancelLoad := context.WithTimeout(startContext, 5*time.Second)
		credential, err := client.Credentials.Load(loadContext)
		cancelLoad()
		if err != nil || credential == "" {
			return errors.New("secondary credential unavailable")
		}
		if err := m.workers.registry.edit(func(state hubRegistryState) (hubRegistryState, error) {
			if err := startContext.Err(); err != nil {
				return state, err
			}
			for i := range state.Connections {
				if state.Connections[i].Origin == origin && origin != state.Primary {
					state.Connections[i].Enabled = true
					state.Connections[i].SyncConsent = true
					return state, nil
				}
			}
			return state, errors.New("secondary Hub is not paired")
		}); err != nil {
			return err
		}
		err = m.workers.start(slot, origin, startContext)
		if err != nil {
			slot.lastError = "start_failed"
			return err
		}
		slot.lastError = ""
		return nil
	})
}

func registerSecondaryPairingRoutes(api *http.ServeMux, pairings *secondaryPairings) {
	api.HandleFunc("GET /api/hubs/recovery-disclosure", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"disclosure": secondaryRecoveryDisclosure})
	})
	api.HandleFunc("POST /api/hubs/re-pairings", func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "secondary recovery unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			Origin  string `json:"origin"`
			Consent bool   `json:"consent"`
		}
		if decodeHubJSON(r.Body, &input) != nil {
			http.Error(w, "invalid recovery request", http.StatusBadRequest)
			return
		}
		id, result, err := pairings.beginAttempt(r.Context(), input.Origin, input.Consent, true)
		if err != nil {
			http.Error(w, "secondary recovery could not be started", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": id, "origin": input.Origin, "pairing": result, "disclosure": secondaryRecoveryDisclosure})
	})
	api.HandleFunc("GET /api/hubs/status", func(w http.ResponseWriter, _ *http.Request) {
		if pairings == nil {
			http.Error(w, "Hub status unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, pairings.workers.statuses())
	})
	api.HandleFunc("POST /api/hubs/disable", func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "secondary Hub unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			Origin string `json:"origin"`
		}
		if decodeHubJSON(r.Body, &input) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := pairings.workers.Disable(input.Origin); err != nil {
			http.Error(w, "secondary Hub could not be disabled", http.StatusConflict)
			return
		}
		writeJSON(w, map[string]string{"state": "disabled"})
	})
	api.HandleFunc("POST /api/hubs/retry", func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "secondary Hub unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			Origin string `json:"origin"`
		}
		if decodeHubJSON(r.Body, &input) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		origin, err := canonicalRegistryOrigin(input.Origin)
		if err != nil || origin == pairings.workers.registry.snapshot().Primary {
			http.Error(w, "secondary Hub unavailable", http.StatusConflict)
			return
		}
		if err := pairings.workers.StartEnabled(origin); err != nil {
			http.Error(w, "secondary Hub could not start", http.StatusConflict)
			return
		}
		pairings.workers.Retry(origin)
		writeJSON(w, map[string]string{"state": "started"})
	})
	api.HandleFunc("GET /api/hubs/pairing-disclosure", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"disclosure": secondaryDisclosure})
	})
	api.HandleFunc("GET /api/hubs/enable-disclosure", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"disclosure": secondaryEnableDisclosure})
	})
	api.HandleFunc("POST /api/hubs/enable", func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "secondary enable unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			Origin  string `json:"origin"`
			Consent bool   `json:"consent"`
		}
		if decodeHubJSON(r.Body, &input) != nil {
			http.Error(w, "invalid enable request", http.StatusBadRequest)
			return
		}
		if err := pairings.enable(r.Context(), input.Origin, input.Consent); err != nil {
			http.Error(w, "secondary Hub could not be enabled", http.StatusConflict)
			return
		}
		writeJSON(w, map[string]string{"state": "started"})
	})
	api.HandleFunc("POST /api/hubs/pairings", func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "secondary pairing unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			Origin  string `json:"origin"`
			Consent bool   `json:"consent"`
		}
		if decodeHubJSON(r.Body, &input) != nil {
			http.Error(w, "invalid pairing request", http.StatusBadRequest)
			return
		}
		id, result, err := pairings.begin(r.Context(), input.Origin, input.Consent)
		if err != nil {
			http.Error(w, "secondary pairing could not be started", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": id, "origin": input.Origin, "pairing": result, "disclosure": secondaryDisclosure})
	})
	api.HandleFunc("POST /api/hubs/pairings/{id}/poll", func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "secondary pairing unavailable", http.StatusServiceUnavailable)
			return
		}
		state, err := pairings.poll(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, "secondary pairing is unavailable", http.StatusConflict)
			return
		}
		writeJSON(w, map[string]string{"state": state})
	})
}
