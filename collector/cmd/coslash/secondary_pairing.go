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
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const secondaryDisclosure = "Adding this Hub allows eligible personal session records, including prompts, commands and source snippets, to sync automatically to this destination under its policy and Local privacy settings. Workspace sharing requires separate consent. Secondary sync is not active in this version."

func runSecondaryPairCLI(out io.Writer, args []string) error {
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
	origin  string
	client  *hubclient.Client
	staged  *stagedCredential
	pairing hubclient.PairingResult
	busy    bool
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
	for _, c := range state.Connections {
		if c.Origin == origin {
			return "", hubclient.PairingResult{}, errors.New("Hub already exists")
		}
	}
	id, err := newConnectJobID()
	if err != nil {
		return "", hubclient.PairingResult{}, err
	}
	attempt := &secondaryAttempt{origin: origin, busy: true}
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
		if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
			return "expired", nil
		}
		if err = m.workers.registry.edit(func(state hubRegistryState) (hubRegistryState, error) {
			if ctx.Err() != nil || !time.Now().Before(attempt.pairing.ExpiresAt) {
				return state, errors.New("pairing expired")
			}
			for i := range state.Connections {
				if state.Connections[i].Origin == attempt.origin {
					state.Connections[i].Enabled = true
					return state, nil
				}
			}
			return state, errors.New("unknown Hub")
		}); err != nil {
			return "failed", nil
		}
		return "paired", nil
	default:
		return result.State, nil
	}
}

func registerSecondaryPairingRoutes(api *http.ServeMux, pairings *secondaryPairings) {
	api.HandleFunc("GET /api/hubs/pairing-disclosure", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"disclosure": secondaryDisclosure})
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
