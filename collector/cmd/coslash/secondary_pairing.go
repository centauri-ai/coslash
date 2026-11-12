package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

const secondaryDisclosure = "Pairing this Hub stores a device credential but does not authorize or start personal sync. Enabling sync requires separate consent. Workspace sharing requires separate consent."
const secondaryEnableDisclosure = "Enabling this Hub starts sync and authorizes automatic upload of eligible personal session records, including prompts, commands and source snippets, to this destination under its policy and Local privacy settings. This consent persists across restarts. Workspace sharing requires separate consent."
const secondaryRecoveryDisclosure = "Recovery stops sync and keeps pending data on this computer. If the approved connection matches the previous one, its pending data can resume. A different connection may belong to another account. If you choose to connect it, old pending data stays on this computer and will not be sent to that account. After you separately enable sync, eligible sessions may be uploaded to the new account. If recovery is uncertain, check Hub status before trying again."

func runSecondaryPairCLI(out io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: coslash secondary-pair <origin> [--consent] | coslash secondary-pair poll <attempt-id>")
	}
	method, path := http.MethodPost, "/api/hubs/pairings"
	var body any
	consented := len(args) >= 2 && args[len(args)-1] == "--consent"
	switch action := args[0]; {
	case action == "status" && len(args) == 1:
		method, path = http.MethodGet, "/api/hubs/status"
	case action == "poll" && len(args) == 2:
		decoded, err := hex.DecodeString(args[1])
		if err != nil || len(decoded) != 16 {
			return errors.New("invalid pairing attempt")
		}
		path = "/api/hubs/pairings/" + args[1] + "/poll"
	case (action == "disable" || action == "retry") && len(args) == 2:
		path, body = "/api/hubs/"+action, map[string]string{"origin": args[1]}
	case action == "enable" || action == "recover":
		newBinding := action == "recover" && len(args) == 4 && args[2] == "--consent" && args[3] == "--new-binding-consent"
		if len(args) != 2 && !(len(args) == 3 && consented) && !newBinding {
			return errors.New("explicit --consent is required")
		}
		disclosure := secondaryEnableDisclosure
		path = "/api/hubs/enable"
		if action == "recover" {
			disclosure, path = secondaryRecoveryDisclosure, "/api/hubs/re-pairings"
		}
		fmt.Fprintln(out, disclosure)
		if len(args) == 2 {
			if action == "recover" {
				fmt.Fprintf(out, "To recover %s, repeat with --consent. Add --new-binding-consent to keep old pending data here and allow a different connection.\n", args[1])
			} else {
				fmt.Fprintf(out, "To enable %s, repeat with --consent.\n", args[1])
			}
			return nil
		}
		input := map[string]any{"origin": args[1], "consent": true}
		if action == "recover" {
			input["newBindingConsent"] = newBinding
		}
		body = input
	case len(args) == 1 || len(args) == 2 && consented:
		fmt.Fprintln(out, secondaryDisclosure)
		if len(args) == 1 {
			fmt.Fprintf(out, "To consent for %s, repeat with --consent.\n", args[0])
			return nil
		}
		body = map[string]any{"origin": args[0], "consent": true}
	default:
		return errors.New("explicit --consent is required")
	}
	client, err := newLocalAPIClient()
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	data, err := client.request(method, path, reader)
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
	origin            string
	client            *hubclient.Client
	staged            *stagedCredential
	pairing           hubclient.PairingResult
	busy              bool
	recovery          bool
	newBindingConsent bool
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
	return m.beginAttemptWithNewBinding(ctx, raw, consent, recovery, false)
}

func (m *secondaryPairings) beginAttemptWithNewBinding(ctx context.Context, raw string, consent, recovery, newBindingConsent bool) (string, hubclient.PairingResult, error) {
	if !consent {
		return "", hubclient.PairingResult{}, errors.New("explicit Local consent required")
	}
	if newBindingConsent && !recovery {
		return "", hubclient.PairingResult{}, errors.New("new binding consent requires recovery")
	}
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil || len(origin) < 8 || origin[:8] != "https://" {
		return "", hubclient.PairingResult{}, errors.New("invalid secondary Hub")
	}
	state := m.workers.registry.snapshot()
	if state.Primary == "" {
		return "", hubclient.PairingResult{}, errors.New("primary Hub required")
	}
	connection, exists := state.connection(origin)
	if !exists {
		connection = hubConnection{Origin: origin}
	}
	if origin == state.Primary || exists != recovery {
		return "", hubclient.PairingResult{}, errors.New("invalid secondary pairing target")
	}
	id, err := newConnectJobID()
	if err != nil {
		return "", hubclient.PairingResult{}, err
	}
	attempt := &secondaryAttempt{origin: origin, busy: true, recovery: recovery, newBindingConsent: newBindingConsent}
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
	client, err := m.workers.checkedClient(connection, false)
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
		if _, exists := m.workers.registry.snapshot().connection(attempt.origin); exists {
			return "failed", nil
		}
		client, err := m.workers.checkedClient(hubConnection{Origin: attempt.origin}, false)
		if err != nil {
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
		connection, found := state.connection(attempt.origin)
		if !found || connection.Origin == state.Primary || connection.Enabled || connection.SyncConsent {
			return errors.New("recovery target changed")
		}
		client, err := m.workers.checkedClient(connection, false)
		if err != nil {
			return errors.New("recovery client unavailable")
		}
		loadContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = client.Credentials.Load(loadContext)
		cancel()
		if !errors.Is(err, hubclient.ErrNotPaired) {
			return errors.New("recovery credential changed")
		}
		var stored string
		if slot.controller != nil && slot.generation == connection.Generation && slot.controller.Queue() != nil {
			stored, _ = slot.controller.Queue().DiscoveryCursor()
		} else {
			root := filepath.Join(m.workers.secondaryRoot(connection), "sync-v4")
			stored, err = syncv4.ReadStoredBinding(root)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) && attempt.newBindingConsent {
					result = m.commitNewGeneration(ctx, slot, attempt, connection, credential)
					return nil
				}
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
			if attempt.newBindingConsent {
				result = m.commitNewGeneration(ctx, slot, attempt, connection, credential)
				return nil
			}
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

// commitNewGeneration runs in the origin's lifecycle lane after the old worker
// has stopped. Registry publication is the only point selecting the new root.
func (m *secondaryPairings) commitNewGeneration(ctx context.Context, slot *hubWorkerSlot, attempt *secondaryAttempt, old hubConnection, credential string) string {
	if len(old.Quarantined) >= 100 {
		return "quarantine_limit"
	}
	if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
		return "expired"
	}
	generation, err := newConnectJobID()
	if err != nil {
		return "failed"
	}
	if generation == old.Generation {
		return "failed"
	}
	for _, quarantined := range old.Quarantined {
		if generation == quarantined {
			return "failed"
		}
	}
	next := hubConnection{Origin: old.Origin, Generation: generation}
	client, err := m.workers.checkedClient(next, false)
	if err != nil {
		return "failed"
	}
	loadContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = client.Credentials.Load(loadContext)
	cancel()
	if !errors.Is(err, hubclient.ErrNotPaired) {
		return "failed"
	}
	if _, err := os.Lstat(m.workers.secondaryRoot(next)); !errors.Is(err, os.ErrNotExist) {
		return "failed"
	}
	if err := client.Credentials.Save(ctx, credential); err != nil {
		return "failed"
	}
	if runtime.GOOS == "linux" {
		root := m.workers.secondaryRoot(next)
		for _, dir := range []string{root, filepath.Dir(root), filepath.Dir(filepath.Dir(root)), filepath.Join(m.workers.home, "hub-connections")} {
			if err := syncHubRegistryDirectory(dir); err != nil {
				return "failed"
			}
		}
	}
	if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
		return "expired"
	}
	err = m.workers.registry.edit(func(state hubRegistryState) (hubRegistryState, error) {
		if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
			return state, errors.New("recovery expired")
		}
		for i := range state.Connections {
			c := &state.Connections[i]
			if c.Origin != old.Origin || c.Origin == state.Primary {
				continue
			}
			if c.Enabled || c.SyncConsent || c.Generation != old.Generation {
				return state, errors.New("recovery target changed")
			}
			previous := old.Generation
			if previous == "" {
				previous = "legacy"
			}
			c.Quarantined = append(c.Quarantined, previous)
			c.Generation = generation
			return state, nil
		}
		return state, errors.New("unknown recovery target")
	})
	// A post-rename durability error can still publish the new registry state.
	published := false
	for _, c := range m.workers.registry.snapshot().Connections {
		if c.Origin == old.Origin && c.Generation == generation {
			published = true
			slot.stopWorker()
			slot.controller = nil
			slot.generation = generation
		}
	}
	if err != nil {
		if published {
			return "recovery_status_unknown"
		}
		return "failed"
	}
	return "recovered_new_binding_disabled"
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
	if _, found := state.connection(origin); !found {
		return errors.New("secondary Hub is not paired")
	}
	slot := m.workers.slot(origin)
	return slot.withStart(ctx, func(startContext context.Context) error {
		if err := startContext.Err(); err != nil {
			return err
		}
		state := m.workers.registry.snapshot()
		connection, found := state.connection(origin)
		if !found || origin == state.Primary {
			return errors.New("secondary Hub is not paired")
		}
		client, err := m.workers.checkedClient(connection, false)
		if err != nil {
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
		return m.workers.start(slot, origin, startContext)
	})
}

func registerSecondaryPairingRoutes(api *http.ServeMux, pairings *secondaryPairings) {
	for kind, disclosure := range map[string]string{
		"pairing": secondaryDisclosure, "recovery": secondaryRecoveryDisclosure, "enable": secondaryEnableDisclosure,
	} {
		api.HandleFunc("GET /api/hubs/"+kind+"-disclosure", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]string{"disclosure": disclosure})
		})
	}
	api.HandleFunc("GET /api/hubs/status", func(w http.ResponseWriter, _ *http.Request) {
		if pairings == nil {
			http.Error(w, "Hub status unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, pairings.workers.statuses())
	})
	type originInput struct {
		Origin string `json:"origin"`
	}
	type consentInput struct {
		Origin  string `json:"origin"`
		Consent bool   `json:"consent"`
	}
	registerSecondaryAction(api, pairings, "POST /api/hubs/disable", "secondary Hub could not be disabled", http.StatusOK,
		func(r *http.Request, input originInput) (any, error) {
			return map[string]string{"state": "disabled"}, pairings.workers.Disable(input.Origin)
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/retry", "secondary Hub could not start", http.StatusOK,
		func(r *http.Request, input originInput) (any, error) {
			origin, err := canonicalRegistryOrigin(input.Origin)
			if err != nil || origin == pairings.workers.registry.snapshot().Primary {
				return nil, errors.New("secondary Hub unavailable")
			}
			if err := pairings.workers.StartEnabled(origin); err != nil {
				return nil, err
			}
			pairings.workers.Retry(origin)
			return map[string]string{"state": "started"}, nil
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/enable", "secondary Hub could not be enabled", http.StatusOK,
		func(r *http.Request, input consentInput) (any, error) {
			return map[string]string{"state": "started"}, pairings.enable(r.Context(), input.Origin, input.Consent)
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/pairings", "secondary pairing could not be started", http.StatusCreated,
		func(r *http.Request, input consentInput) (any, error) {
			id, result, err := pairings.begin(r.Context(), input.Origin, input.Consent)
			return map[string]any{"id": id, "origin": input.Origin, "pairing": result, "disclosure": secondaryDisclosure}, err
		})
	registerSecondaryAction(api, pairings, "POST /api/hubs/re-pairings", "secondary recovery could not be started", http.StatusCreated,
		func(r *http.Request, input struct {
			Origin            string `json:"origin"`
			Consent           bool   `json:"consent"`
			NewBindingConsent bool   `json:"newBindingConsent"`
		}) (any, error) {
			id, result, err := pairings.beginAttemptWithNewBinding(r.Context(), input.Origin, input.Consent, true, input.NewBindingConsent)
			return map[string]any{"id": id, "origin": input.Origin, "pairing": result, "disclosure": secondaryRecoveryDisclosure}, err
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

func registerSecondaryAction[T any](api *http.ServeMux, pairings *secondaryPairings, pattern, failure string, status int, action func(*http.Request, T) (any, error)) {
	api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if pairings == nil {
			http.Error(w, "secondary Hub unavailable", http.StatusServiceUnavailable)
			return
		}
		var input T
		if decodeHubJSON(r.Body, &input) != nil {
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
