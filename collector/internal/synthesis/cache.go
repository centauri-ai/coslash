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
	path, err := c.recordPath(id)
	if err != nil {
		return Record{}, err
	}
	if value, ok := c.records.Load(id); ok {
		return value.(Record), nil
	}
	if err := protectSynthesisDirectories(SummariesDir()); err != nil {
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
	c.records.Store(id, record)
	return record, nil
}

func (c *Cache) Store(id string, record Record) error {
	path, err := c.recordPath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(SummariesDir(), 0o700); err != nil {
		return err
	}
	if err := protectSynthesisDirectories(SummariesDir()); err != nil {
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

func (c *Cache) LookupLatest(id string) *session.SessionSynthesis {
	record, err := c.Load(id)
	if err != nil {
		return nil
	}
	synthesis := record.Synthesis
	return &synthesis
}

func (c *Cache) recordPath(id string) (string, error) {
	if id == "" || len(id) > 256 {
		return "", fmt.Errorf("invalid synthesis cache session id")
	}
	for _, character := range id {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return "", fmt.Errorf("invalid synthesis cache session id")
		}
	}
	return filepath.Join(SummariesDir(), id+".json"), nil
}
