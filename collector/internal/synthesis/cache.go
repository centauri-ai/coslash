package synthesis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/session"
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

func (c *Cache) Load(agent, id string) (Record, error) {
	key := cacheKey{agent: agent, id: id}
	if value, ok := c.records.Load(key); ok {
		return value.(Record), nil
	}
	data, err := os.ReadFile(c.recordPath(agent, id))
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
	directory := filepath.Join(SummariesDir(), agent)
	if err := os.MkdirAll(directory, 0o700); err != nil {
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
	if err := temp.Chmod(0o600); err != nil {
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
	if err := os.Rename(tempName, c.recordPath(agent, id)); err != nil {
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

func (c *Cache) recordPath(agent, id string) string {
	return filepath.Join(SummariesDir(), agent, id+".json")
}
