package syncv4

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// FingerprintFormat names the on-disk layout of the local parse cache.
const FingerprintFormat = "fingerprints/v1"

// maxFingerprintEntryBytes bounds one cached summary; a larger family is
// parsed every pass rather than written to disk.
const maxFingerprintEntryBytes = 64 << 20

const discoveryCursorFile = "discovery-cursor.json"

// Fingerprints is the persisted local parse cache (fingerprints/v1): one
// private file per source under <root>/<agent>/, each holding a metadata
// line and the parsed summary. Entries are replaced atomically, a file that
// fails to decode is a miss that the next parse overwrites, and Prune drops
// entries whose source no longer exists.
type Fingerprints struct {
	root                 string
	mu                   sync.Mutex
	index                map[string]fingerprintMeta
	hits, misses, stores atomic.Int64
}

type fingerprintMeta struct {
	Agent       string `json:"agent"`
	Identity    string `json:"identity"`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

// FingerprintStats counts cache traffic since the store was opened.
type FingerprintStats struct {
	Entries, Hits, Misses, Stores int64
}

// OpenFingerprints opens or creates the cache under root ("" uses the Local
// data directory). A corrupt entry file is removed rather than trusted.
func OpenFingerprints(root string) (*Fingerprints, error) {
	if root == "" {
		root = filepath.Join(settings.Home(), "fingerprints", "v1")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := protectQueueDirectory(root); err != nil {
		return nil, err
	}
	store := &Fingerprints{root: root, index: map[string]fingerprintMeta{}}
	agents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, agent := range agents {
		if !agent.IsDir() {
			continue
		}
		dir := filepath.Join(root, agent.Name())
		if err := protectQueueDirectory(dir); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			meta, err := readFingerprintMeta(path)
			if err != nil {
				_ = os.Remove(path)
				continue
			}
			store.index[fingerprintName(vendors.CacheKey{Agent: meta.Agent, Identity: meta.Identity, Version: meta.Version})] = meta
		}
	}
	return store, nil
}

func fingerprintName(key vendors.CacheKey) string {
	sum := sha256.Sum256([]byte(key.Agent + "\x00" + key.Identity + "\x00" + key.Version))
	return hex.EncodeToString(sum[:16])
}

func (f *Fingerprints) path(key vendors.CacheKey) string {
	agent := key.Agent
	if agent == "" || agent != filepath.Base(agent) {
		agent = "other"
	}
	return filepath.Join(f.root, agent, fingerprintName(key)+".json")
}

func readFingerprintMeta(path string) (fingerprintMeta, error) {
	file, err := os.Open(path)
	if err != nil {
		return fingerprintMeta{}, err
	}
	defer file.Close()
	line, err := bufio.NewReaderSize(file, 4096).ReadBytes('\n')
	if err != nil {
		return fingerprintMeta{}, err
	}
	var meta fingerprintMeta
	if err := json.Unmarshal(line, &meta); err != nil || meta.Agent == "" || meta.Identity == "" {
		return fingerprintMeta{}, errors.New("invalid fingerprint entry")
	}
	return meta, nil
}

// Lookup returns the cached summary when the stored fingerprint matches.
func (f *Fingerprints) Lookup(key vendors.CacheKey) (json.RawMessage, bool) {
	name := fingerprintName(key)
	f.mu.Lock()
	meta, ok := f.index[name]
	f.mu.Unlock()
	if !ok || meta.Fingerprint != key.Fingerprint || meta.Version != key.Version {
		f.misses.Add(1)
		return nil, false
	}
	data, err := readQueueFile(f.path(key))
	if err != nil {
		f.misses.Add(1)
		return nil, false
	}
	newline := bytes.IndexByte(data, '\n')
	if newline < 0 {
		f.misses.Add(1)
		return nil, false
	}
	var stored fingerprintMeta
	if json.Unmarshal(data[:newline], &stored) != nil || stored != meta {
		f.misses.Add(1)
		return nil, false
	}
	payload := data[newline+1:]
	if !json.Valid(payload) {
		f.misses.Add(1)
		return nil, false
	}
	f.hits.Add(1)
	return json.RawMessage(payload), true
}

// Store writes the summary for key, replacing any older fingerprint.
func (f *Fingerprints) Store(key vendors.CacheKey, payload json.RawMessage) error {
	if key.Agent == "" || key.Identity == "" || key.Version == "" {
		return errors.New("incomplete fingerprint key")
	}
	if len(payload) > maxFingerprintEntryBytes {
		return errors.New("fingerprint entry too large")
	}
	meta := fingerprintMeta{Agent: key.Agent, Identity: key.Identity, Version: key.Version, Fingerprint: key.Fingerprint}
	head, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	path := f.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := protectQueueDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := writePrivateFile(path, append(append(head, '\n'), payload...)); err != nil {
		return err
	}
	f.mu.Lock()
	f.index[fingerprintName(key)] = meta
	f.mu.Unlock()
	f.stores.Add(1)
	return nil
}

// Prune removes every entry whose (agent, identity) keep rejects and returns
// how many were removed.
func (f *Fingerprints) Prune(keep func(agent, identity string) bool) (int, error) {
	f.mu.Lock()
	var drop []vendors.CacheKey
	for _, meta := range f.index {
		if !keep(meta.Agent, meta.Identity) {
			drop = append(drop, vendors.CacheKey{Agent: meta.Agent, Identity: meta.Identity, Version: meta.Version})
		}
	}
	f.mu.Unlock()
	var firstErr error
	removed := 0
	for _, key := range drop {
		if err := os.Remove(f.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			firstErr = errors.Join(firstErr, err)
			continue
		}
		f.mu.Lock()
		delete(f.index, fingerprintName(key))
		f.mu.Unlock()
		removed++
	}
	return removed, firstErr
}

// Stats reports entry and traffic counts.
func (f *Fingerprints) Stats() FingerprintStats {
	f.mu.Lock()
	entries := len(f.index)
	f.mu.Unlock()
	return FingerprintStats{Entries: int64(entries), Hits: f.hits.Load(), Misses: f.misses.Load(), Stores: f.stores.Load()}
}

// LoadDiscoveryCursor returns the persisted discovery cursor, if any.
func (f *Fingerprints) LoadDiscoveryCursor() (json.RawMessage, bool) {
	data, err := readQueueFile(filepath.Join(f.root, discoveryCursorFile))
	if err != nil || !json.Valid(data) {
		return nil, false
	}
	return json.RawMessage(data), true
}

// SaveDiscoveryCursor persists the discovery cursor atomically; a nil cursor
// clears it.
func (f *Fingerprints) SaveDiscoveryCursor(cursor json.RawMessage) error {
	path := filepath.Join(f.root, discoveryCursorFile)
	if cursor == nil {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return writePrivateFile(path, cursor)
}

// writePrivateFile writes data to a 0600 file through a temporary file and
// rename, so readers never see a partial entry.
func writePrivateFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".entry-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := protectQueueFile(file.Name(), file); err != nil {
		file.Close()
		return err
	}
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Root is the directory the store writes under.
func (f *Fingerprints) Root() string { return f.root }
