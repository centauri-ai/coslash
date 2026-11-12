package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const hubRegistryVersion = 3

type hubConnection struct {
	Origin       string   `json:"origin"`
	Enabled      bool     `json:"enabled"`
	LegacyOrigin string   `json:"legacyOrigin,omitempty"`
	SyncConsent  bool     `json:"syncConsent,omitempty"`
	Generation   string   `json:"generation,omitempty"`
	Quarantined  []string `json:"quarantined,omitempty"`
}

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

type hubRegistryWire struct {
	Version     *int    `json:"version"`
	Primary     *string `json:"primary"`
	Connections []struct {
		Origin       *string  `json:"origin"`
		Enabled      *bool    `json:"enabled"`
		LegacyOrigin string   `json:"legacyOrigin,omitempty"`
		SyncConsent  *bool    `json:"syncConsent,omitempty"`
		Generation   *string  `json:"generation,omitempty"`
		Quarantined  []string `json:"quarantined,omitempty"`
	} `json:"connections"`
}

type hubRegistry struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	pending atomic.Int32
	dir     string
	state   hubRegistryState
	write   func(string, []byte) (bool, error)
}

// canonicalRegistryOrigin normalizes the origin while retaining non-default ports.
// The exact legacy origin is stored separately for the old credential and binding.
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
	if host == "" || len(host) > 253 {
		return "", errors.New("invalid Hub origin")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", errors.New("invalid Hub host")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", errors.New("invalid Hub host")
				}
			}
		}
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid Hub port")
		}
		port = strconv.Itoa(n)
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	return u.String(), nil
}

// openHubRegistry is owned by the single Local runtime. All mutations must use
// the returned instance. It migrates only a legacy origin whose pairing is verified by
// paired. An unpaired legacy selector is not a committed connection. No file
// is created in that case, so a later completed legacy pairing can be migrated.
func openHubRegistry(home, legacy string, paired func(string) (bool, error)) (*hubRegistry, error) {
	return openHubRegistryWithWriter(home, legacy, paired, writeHubRegistryFile)
}

func openHubRegistryWithWriter(home, legacy string, paired func(string) (bool, error), write func(string, []byte) (bool, error)) (*hubRegistry, error) {
	if home == "" || write == nil {
		return nil, errors.New("invalid Hub registry configuration")
	}
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
	r := &hubRegistry{dir: dir, write: write, state: hubRegistryState{Version: hubRegistryVersion, Connections: []hubConnection{}}}
	f, err := os.Open(filepath.Join(dir, "registry.json"))
	if errors.Is(err, os.ErrNotExist) {
		if legacy == "" {
			return r, nil
		}
		origin, err := canonicalRegistryOrigin(legacy)
		if err != nil {
			return nil, err
		}
		if paired == nil {
			return nil, errors.New("legacy pairing verification required")
		}
		ok, err := paired(legacy)
		if err != nil {
			return nil, err
		}
		if !ok {
			return r, nil
		}
		r.state.Primary = origin
		r.state.Connections = []hubConnection{{Origin: origin, Enabled: true, LegacyOrigin: legacy}}
		if _, err := r.save(r.state); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > 1<<20 || rejectDuplicateJSONKeys(data) != nil {
		return nil, errors.New("invalid Hub connection registry")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire hubRegistryWire
	if err := decoder.Decode(&wire); err != nil {
		return nil, errors.New("invalid Hub connection registry")
	}
	if wire.Version == nil || wire.Primary == nil || wire.Connections == nil {
		return nil, errors.New("invalid Hub connection registry")
	}
	if *wire.Version != 1 && *wire.Version != 2 && *wire.Version != hubRegistryVersion {
		return nil, errors.New("invalid Hub connection registry")
	}
	r.state = hubRegistryState{Version: hubRegistryVersion, Primary: *wire.Primary}
	for _, c := range wire.Connections {
		if c.Origin == nil || c.Enabled == nil || (*wire.Version == 1 && c.SyncConsent != nil) || (*wire.Version < 3 && (c.Generation != nil || c.Quarantined != nil)) {
			return nil, errors.New("invalid Hub connection registry")
		}
		consent := c.SyncConsent != nil && *c.SyncConsent
		enabled := *c.Enabled
		if *c.Origin != *wire.Primary && !consent {
			enabled = false
		}
		generation := ""
		if c.Generation != nil {
			generation = *c.Generation
		}
		r.state.Connections = append(r.state.Connections, hubConnection{Origin: *c.Origin, Enabled: enabled, LegacyOrigin: c.LegacyOrigin, SyncConsent: consent, Generation: generation, Quarantined: c.Quarantined})
	}
	if err := validateHubRegistry(r.state); err != nil {
		return nil, err
	}
	return r, nil
}

// JSON's ordinary decoder accepts duplicate keys, including duplicate consent
// fields. Reject them before decoding so the registry has one interpretation.
func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var readValue func(int) error
	readValue = func(depth int) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			if token == nil {
				return errors.New("null registry value")
			}
			return nil
		}
		// Bound nesting, including the versioned quarantine list.
		if depth > 4 || (depth == 4 && delim != '[') {
			return errors.New("registry nesting too deep")
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate JSON key")
				}
				seen[key] = true
				if err := readValue(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := readValue(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := readValue(1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func validateHubRegistry(state hubRegistryState) error {
	invalid := errors.New("invalid Hub connection registry")
	if state.Version != hubRegistryVersion || state.Primary == "" || len(state.Connections) == 0 || len(state.Connections) > 100 {
		return invalid
	}
	seen := make(map[string]bool, len(state.Connections))
	for _, c := range state.Connections {
		origin, err := canonicalRegistryOrigin(c.Origin)
		if err != nil || origin != c.Origin || seen[origin] {
			return invalid
		}
		if c.LegacyOrigin != "" {
			legacy, err := canonicalRegistryOrigin(c.LegacyOrigin)
			if err != nil || legacy != c.Origin || c.Origin != state.Primary {
				return invalid
			}
		}
		if c.LegacyOrigin == "" && !strings.HasPrefix(c.Origin, "https://") {
			return invalid
		}
		if c.Origin == state.Primary && (c.Generation != "" || len(c.Quarantined) != 0) {
			return invalid
		}
		if c.Generation != "" && !validHubGeneration(c.Generation) {
			return invalid
		}
		if len(c.Quarantined) > 100 {
			return invalid
		}
		generations := map[string]bool{}
		for _, generation := range c.Quarantined {
			if (generation != "legacy" && !validHubGeneration(generation)) || generation == c.Generation || generations[generation] {
				return invalid
			}
			generations[generation] = true
		}
		if (c.Generation == "" && len(c.Quarantined) != 0) || (c.Generation != "" && !generations["legacy"]) {
			return invalid
		}
		seen[origin] = true
	}
	if !seen[state.Primary] {
		return invalid
	}
	return nil
}

func validHubGeneration(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (r *hubRegistry) snapshot() hubRegistryState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state
	state.Connections = append([]hubConnection(nil), state.Connections...)
	for i := range state.Connections {
		state.Connections[i].Quarantined = append([]string(nil), state.Connections[i].Quarantined...)
	}
	return state
}

// addPrimary is for an explicitly approved and successfully paired first Hub.
// Authority never moves to another connection automatically.
func (r *hubRegistry) addPrimary(raw string) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(origin, "https://") {
		return errors.New("new Hub requires HTTPS")
	}
	return r.edit(func(state hubRegistryState) (hubRegistryState, error) {
		if state.Primary != "" {
			return state, errors.New("primary Hub already exists")
		}
		return hubRegistryState{Version: hubRegistryVersion, Primary: origin, Connections: []hubConnection{{Origin: origin, Enabled: true}}}, nil
	})
}

// adoptLegacyPrimary is called only after verifying the legacy credential and
// the existing queue binding. It keeps the exact origin used by that binding.
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
func (r *hubRegistry) addSecondary(raw string) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(origin, "https://") {
		return errors.New("secondary Hub requires HTTPS")
	}
	return r.edit(func(state hubRegistryState) (hubRegistryState, error) {
		if state.Primary == "" {
			return state, errors.New("primary Hub required")
		}
		for _, c := range state.Connections {
			if c.Origin == origin {
				return state, errors.New("Hub already exists")
			}
		}
		state.Connections = append(state.Connections, hubConnection{Origin: origin})
		return state, nil
	})
}

// setEnabled records an explicit user action; the caller must also verify
// pairing and consent before passing true. It never changes primary authority.
func (r *hubRegistry) setEnabled(raw string, enabled bool) error {
	origin, err := canonicalRegistryOrigin(raw)
	if err != nil {
		return err
	}
	return r.edit(func(state hubRegistryState) (hubRegistryState, error) {
		for i := range state.Connections {
			if state.Connections[i].Origin == origin {
				state.Connections[i].Enabled = enabled
				if origin != state.Primary {
					state.Connections[i].SyncConsent = enabled
				}
				return state, nil
			}
		}
		return state, errors.New("unknown Hub")
	})
}

func (r *hubRegistry) edit(apply func(hubRegistryState) (hubRegistryState, error)) error {
	defer r.pending.Add(-1)
	if r.pending.Add(1) > 128 {
		return errors.New("too many pending Hub registry changes")
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	next, err := apply(r.snapshot())
	if err != nil {
		return err
	}
	if err := validateHubRegistry(next); err != nil {
		return err
	}
	committed, err := r.save(next)
	if committed {
		r.mu.Lock()
		r.state = next
		r.mu.Unlock()
	}
	return err
}

func (r *hubRegistry) save(state hubRegistryState) (bool, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	committed, err := r.write(r.dir, data)
	if !committed && err == nil {
		return false, errors.New("Hub registry was not committed")
	}
	return committed, err
}
