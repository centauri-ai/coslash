package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

// CachedSnapshot is the last validated remote view plus Mac-clock metadata.
type CachedSnapshot struct {
	View             remoteviewv1.View `json:"view"`
	FetchedAtMs      int64             `json:"fetchedAtMs"`
	ClockOffsetMs    int64             `json:"clockOffsetMs"`
	RoundTripMs      int64             `json:"roundTripMs"`
	CollectorVersion string            `json:"collectorVersion"`
	SchemaVersion    string            `json:"schemaVersion"`
	Capabilities     []string          `json:"capabilities"`
	LaunchableAgents []string          `json:"launchableAgents"`
	HostOS           string            `json:"hostOs"`
	HostArch         string            `json:"hostArch"`
}

// Cache owns one source directory under ~/.coslash/remotes/<id>/.
type Cache struct {
	Root string
}

func NewCache(root string) *Cache {
	if root == "" {
		root = settings.Home()
	}
	return &Cache{Root: root}
}

func (c *Cache) sourceDir(sourceID string) (string, error) {
	if !settings.ValidRemoteID(sourceID) {
		return "", fmt.Errorf("invalid remote source id %q", sourceID)
	}
	return filepath.Join(c.Root, "remotes", sourceID), nil
}

func (c *Cache) snapshotPath(sourceID string) (string, error) {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snapshot.json"), nil
}

// Load reads and validates a cached snapshot. Corrupt files are ignored.
func (c *Cache) Load(sourceID string) (CachedSnapshot, bool, error) {
	path, err := c.snapshotPath(sourceID)
	if err != nil {
		return CachedSnapshot{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CachedSnapshot{}, false, nil
		}
		return CachedSnapshot{}, false, err
	}
	var cached CachedSnapshot
	if err := json.Unmarshal(data, &cached); err != nil {
		return CachedSnapshot{}, false, nil
	}
	if _, err := remoteviewv1.Marshal(cached.View); err != nil {
		return CachedSnapshot{}, false, nil
	}
	return cached, true, nil
}

// Store atomically replaces the last-good snapshot file.
func (c *Cache) Store(sourceID string, cached CachedSnapshot) error {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return err
	}
	remotes := filepath.Join(c.Root, "remotes")
	if err := os.MkdirAll(remotes, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(remotes, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	path, err := c.snapshotPath(sourceID)
	if err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// RemoveSource deletes only that source directory.
func (c *Cache) RemoveSource(sourceID string) error {
	dir, err := c.sourceDir(sourceID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func age(fetchedAtMs int64, now time.Time) time.Duration {
	if fetchedAtMs <= 0 {
		return FreshnessInterval + time.Second
	}
	return now.Sub(time.UnixMilli(fetchedAtMs))
}
