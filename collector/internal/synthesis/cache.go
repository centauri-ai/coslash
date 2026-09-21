package synthesis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type Record struct {
	Agent       string                   `json:"agent"`
	SessionID   string                   `json:"sessionId"`
	Revision    int64                    `json:"mtime"`
	Model       string                   `json:"model"`
	GeneratedAt int64                    `json:"generatedAt"`
	Synthesis   session.SessionSynthesis `json:"synthesis"`
}

type Cache struct {
	records sync.Map
}

type cacheKey struct {
	agent string
	id    string
}

func NewCache() *Cache {
	return &Cache{}
}

func MigrateLegacyCache(exists func(agent, id string) (bool, error)) error {
	entries, err := os.ReadDir(SummariesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cache := NewCache()
	var failures []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		legacyPath := filepath.Join(SummariesDir(), entry.Name())
		data, err := os.ReadFile(legacyPath)
		if err != nil {
			failures = append(failures, fmt.Errorf("read legacy synthesis %q: %w", id, err))
			continue
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			failures = append(failures, fmt.Errorf("decode legacy synthesis %q: %w", id, err))
			continue
		}
		var matches []string
		resolutionFailed := false
		for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex, vendors.AgentOpenCode} {
			found, err := exists(agent, id)
			if err != nil {
				failures = append(failures, fmt.Errorf("resolve legacy synthesis %q: %w", id, err))
				resolutionFailed = true
				break
			}
			if found {
				matches = append(matches, agent)
				if len(matches) > 1 {
					break
				}
			}
		}
		if resolutionFailed {
			continue
		}
		if len(matches) == 0 {
			continue
		}
		if len(matches) == 1 {
			destination, err := cache.recordPath(matches[0], id)
			if err != nil {
				failures = append(failures, fmt.Errorf("resolve synthesis destination %q: %w", id, err))
				continue
			}
			if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
				if err := cache.Store(matches[0], id, record); err != nil {
					failures = append(failures, fmt.Errorf("migrate legacy synthesis %q: %w", id, err))
					continue
				}
			} else if err != nil {
				failures = append(failures, fmt.Errorf("inspect synthesis destination %q: %w", id, err))
				continue
			}
		}
		if err := os.Remove(legacyPath); err != nil {
			failures = append(failures, fmt.Errorf("remove legacy synthesis %q: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

func (c *Cache) Load(agent, id string) (Record, error) {
	path, err := c.recordPath(agent, id)
	if err != nil {
		return Record{}, err
	}
	key := cacheKey{agent: agent, id: id}
	if value, ok := c.records.Load(key); ok {
		return value.(Record), nil
	}
	if err := protectSynthesisDirectories(filepath.Dir(path)); err != nil {
		return Record{}, err
	}
	data, err := readSynthesisFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("decode synthesis cache %q: %w", id, err)
	}
	c.records.Store(key, record)
	return record, nil
}

func (c *Cache) Store(agent, id string, record Record) error {
	path, err := c.recordPath(agent, id)
	if err != nil {
		return err
	}
	directory := filepath.Join(SummariesDir(), agent)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := protectSynthesisDirectories(directory); err != nil {
		return err
	}
	record.Agent = agent
	record.SessionID = id
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".synthesis-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := protectSynthesisFile(tempName, temp); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	c.records.Store(cacheKey{agent: agent, id: id}, record)
	return nil
}

func (c *Cache) Lookup(agent, id string, revision int64) *session.SessionSynthesis {
	if revision <= 0 {
		return nil
	}
	record, err := c.Load(agent, id)
	if err != nil || record.Revision != revision {
		return nil
	}
	synthesis := record.Synthesis
	return &synthesis
}

func (c *Cache) LookupLatest(agent, id string) *session.SessionSynthesis {
	record, err := c.Load(agent, id)
	if err != nil {
		return nil
	}
	synthesis := record.Synthesis
	return &synthesis
}

func (c *Cache) recordPath(agent, id string) (string, error) {
	if !validCachePathComponent(agent) {
		return "", fmt.Errorf("invalid synthesis cache agent")
	}
	if !validCachePathComponent(id) {
		return "", fmt.Errorf("invalid synthesis cache session id")
	}
	return filepath.Join(SummariesDir(), agent, id+".json"), nil
}

func validCachePathComponent(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return false
		}
	}
	return true
}
