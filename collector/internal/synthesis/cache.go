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
	SessionID   string                   `json:"sessionId"`
	Revision    int64                    `json:"mtime"`
	Model       string                   `json:"model"`
	GeneratedAt int64                    `json:"generatedAt"`
	Synthesis   session.SessionSynthesis `json:"synthesis"`
}

type Cache struct {
	records sync.Map
}

func NewCache() *Cache {
	return &Cache{}
}

func (c *Cache) Load(id string) (Record, error) {
	if value, ok := c.records.Load(id); ok {
		return value.(Record), nil
	}
	data, err := os.ReadFile(c.recordPath(id))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("decode synthesis cache %q: %w", id, err)
	}
	c.records.Store(id, record)
	return record, nil
}

func (c *Cache) Store(id string, record Record) error {
	if err := os.MkdirAll(SummariesDir(), 0o700); err != nil {
		return err
	}
	record.SessionID = id
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(SummariesDir(), ".synthesis-*.tmp")
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
	if err := os.Rename(tempName, c.recordPath(id)); err != nil {
		return err
	}
	c.records.Store(id, record)
	return nil
}

func (c *Cache) Lookup(id string, revision int64) *session.SessionSynthesis {
	if revision <= 0 {
		return nil
	}
	record, err := c.Load(id)
	if err != nil || record.Revision != revision {
		return nil
	}
	synthesis := record.Synthesis
	return &synthesis
}

func (c *Cache) recordPath(id string) string {
	return filepath.Join(SummariesDir(), id+".json")
}
