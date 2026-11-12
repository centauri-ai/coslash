package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const hubRegistryVersion = 1

var errInvalidHubRegistry = errors.New("invalid Hub connection registry")

// hubConnection is one Hub destination. For a secondary, Enabled records the
// explicit consent to sync personal sessions to it.
type hubConnection struct {
	Origin       string `json:"origin"`
	Enabled      bool   `json:"enabled"`
	LegacyOrigin string `json:"legacyOrigin,omitempty"`
}

// clientOrigin is the exact origin the primary's credential and queue binding
// were created with, which may differ from the canonical registry key.
func (c hubConnection) clientOrigin() string {
	if c.LegacyOrigin != "" {
		return c.LegacyOrigin
	}
	return c.Origin
}

type hubRegistryState struct {
	Version     int             `json:"version"`
	Primary     string          `json:"primary"`
	Connections []hubConnection `json:"connections"`
}

func (state hubRegistryState) connection(origin string) (hubConnection, bool) {
	for _, c := range state.Connections {
		if c.Origin == origin {
			return c, true
		}
	}
	return hubConnection{}, false
}

type hubRegistry struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	dir     string
	state   hubRegistryState
	write   func(string, []byte) (bool, error)
}

// canonicalRegistryOrigin is the registry key: a validated Hub origin with a
// lower-case host and no default port.
func canonicalRegistryOrigin(raw string) (string, error) {
	origin, err := hubOrigin(raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	host, port := strings.TrimSuffix(strings.ToLower(u.Hostname()), "."), u.Port()
	if host == "" {
		return "", errors.New("invalid Hub origin")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	u.Host = host
	return u.String(), nil
}

// openHubRegistry is owned by the single Local runtime. It migrates a legacy
// origin only when paired verifies it; an unpaired selector creates no file, so
// a later completed pairing can still be migrated.
func openHubRegistry(home, legacy string, paired func(string) (bool, error)) (*hubRegistry, error) {
	dir := filepath.Join(home, "hub-connections")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := syncHubRegistryDirectory(home); err != nil {
		return nil, err
	}
	if err := protectHubRegistryDirectory(dir); err != nil {
		return nil, err
	}
	r := &hubRegistry{dir: dir, write: writeHubRegistryFile, state: hubRegistryState{Version: hubRegistryVersion, Connections: []hubConnection{}}}
	f, err := os.Open(filepath.Join(dir, "registry.json"))
	if errors.Is(err, os.ErrNotExist) {
		if legacy == "" {
			return r, nil
		}
		if ok, err := paired(legacy); err != nil || !ok {
			return r, err
		}
		return r, r.adoptLegacyPrimary(legacy)
	}
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 1<<20 || decoder.Decode(&r.state) != nil {
		return nil, errInvalidHubRegistry
	}
	if err := validateHubRegistry(r.state); err != nil {
		return nil, err
	}
	return r, nil
}

func validateHubRegistry(state hubRegistryState) error {
	if state.Version != hubRegistryVersion || len(state.Connections) == 0 || len(state.Connections) > 100 {
		return errInvalidHubRegistry
	}
	seen := make(map[string]bool, len(state.Connections))
	for _, c := range state.Connections {
		origin, err := canonicalRegistryOrigin(c.Origin)
		if err != nil || origin != c.Origin || seen[origin] {
			return errInvalidHubRegistry
		}
		if c.LegacyOrigin != "" {
			legacy, err := canonicalRegistryOrigin(c.LegacyOrigin)
			if err != nil || legacy != c.Origin || c.Origin != state.Primary {
				return errInvalidHubRegistry
			}
		} else if !strings.HasPrefix(c.Origin, "https://") {
			return errInvalidHubRegistry
		}
		seen[origin] = true
	}
	if !seen[state.Primary] {
		return errInvalidHubRegistry
	}
	return nil
}

func (r *hubRegistry) snapshot() hubRegistryState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state
	state.Connections = append([]hubConnection(nil), state.Connections...)
	return state
}

// adoptLegacyPrimary is called only after verifying the credential and the
// existing queue binding. Authority never moves to another connection.
func (r *hubRegistry) adoptLegacyPrimary(raw string) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	return r.edit(func(state hubRegistryState) (hubRegistryState, error) {
		if state.Primary != "" {
			return state, errors.New("primary Hub already exists")
		}
		return hubRegistryState{Version: hubRegistryVersion, Primary: origin, Connections: []hubConnection{{Origin: origin, Enabled: true, LegacyOrigin: raw}}}, nil
	})
}

// addSecondary records a destination disabled; it does not authorize sync.
func (r *hubRegistry) addSecondary(origin string) error {
	return r.edit(func(state hubRegistryState) (hubRegistryState, error) {
		if state.Primary == "" {
			return state, errors.New("primary Hub required")
		}
		if _, exists := state.connection(origin); exists {
			return state, errors.New("Hub already exists")
		}
		state.Connections = append(state.Connections, hubConnection{Origin: origin})
		return state, nil
	})
}

// setEnabled records an explicit user action; the caller must verify pairing
// and consent before passing true.
func (r *hubRegistry) setEnabled(origin string, enabled bool) error {
	return r.edit(func(state hubRegistryState) (hubRegistryState, error) {
		for i := range state.Connections {
			if state.Connections[i].Origin == origin {
				state.Connections[i].Enabled = enabled
				return state, nil
			}
		}
		return state, errors.New("unknown Hub")
	})
}

// edit publishes the in-memory state only when the file was replaced, which
// can be true even when the write reports a later durability error.
func (r *hubRegistry) edit(apply func(hubRegistryState) (hubRegistryState, error)) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	next, err := apply(r.snapshot())
	if err != nil {
		return err
	}
	if err := validateHubRegistry(next); err != nil {
		return err
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	committed, err := r.write(r.dir, data)
	if committed {
		r.mu.Lock()
		r.state = next
		r.mu.Unlock()
	}
	return err
}
