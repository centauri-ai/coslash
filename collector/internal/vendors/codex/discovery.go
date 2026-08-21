package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// rolloutID matches the session UUID embedded in a rollout filename, e.g.
// rollout-2026-07-10T14-11-18-019f4dde-db5b-7100-bdc0-09b5aaaac56f.jsonl
var rolloutID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func SessionIDFromRollout(path string) string {
	return rolloutID.FindString(filepath.Base(path))
}

func readHeader(path string) (string, string, error) {
	return readHeaderSource(vendors.LocalReadSource, path)
}

func readHeaderSource(source vendors.ReadSource, path string) (string, string, error) {
	file, err := source.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	var row codexRow
	if err := json.NewDecoder(file).Decode(&row); err != nil {
		return "", "", err
	}
	id := SessionIDFromRollout(path)
	if row.Type != "session_meta" {
		return "", "", fmt.Errorf("first row type %q is not session_meta", row.Type)
	}
	if id == "" {
		return "", "", fmt.Errorf("rollout filename has no session ID")
	}
	if row.Payload.ID != id {
		return "", "", fmt.Errorf("header session ID %q does not match filename ID %q", row.Payload.ID, id)
	}
	return id, row.Payload.ParentThreadID, nil
}

func IsRootRollout(path string) (bool, error) {
	_, parentID, err := readHeader(path)
	return err == nil && parentID == "", err
}

func IsRootRolloutSource(source vendors.ReadSource, path string) (bool, error) {
	_, parentID, err := readHeaderSource(source, path)
	return err == nil && parentID == "", err
}

// root/subagents: ~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<session-uuid>.jsonl
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return SessionsRoot(home), nil
}

func SessionsRoot(home string) string {
	return filepath.Join(home, ".codex", "sessions")
}

func Files() ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return FilesSource(vendors.LocalReadSource, root)
}

func FilesSource(source vendors.ReadSource, root string) ([]string, error) {
	return vendors.JSONLFilesUnderSource(source, root)
}

// FilesSince keeps recent/live roots and their complete descendant graph.
func FilesSince(files []string, live map[string]string, since int64) []string {
	return FilesSinceSource(vendors.LocalReadSource, files, live, since)
}

func FilesSinceSource(
	source vendors.ReadSource,
	files []string,
	live map[string]string,
	since int64,
) []string {
	byID := make(map[string]string, len(files))
	children := map[string][]string{}
	selected := map[string]struct{}{}
	queue := []string{}
	for _, file := range files {
		id, parentID, err := readHeaderSource(source, file)
		if err != nil {
			selected[file] = struct{}{}
			continue
		}
		byID[id] = file
		if parentID != "" {
			children[parentID] = append(children[parentID], id)
			continue
		}
		info, statErr := source.Stat(file)
		_, isLive := live[id]
		if statErr != nil || isLive || info.ModTime().UnixMilli() >= since {
			queue = append(queue, id)
		}
	}
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		file, ok := byID[id]
		if !ok {
			continue
		}
		if _, seen := selected[file]; seen {
			continue
		}
		selected[file] = struct{}{}
		queue = append(queue, children[id]...)
	}
	result := make([]string, 0, len(selected))
	for _, file := range files {
		if _, ok := selected[file]; ok {
			result = append(result, file)
		}
	}
	return result
}

func FilesForRoot(files []string, rootID string) []string {
	return FilesForRootSource(vendors.LocalReadSource, files, rootID)
}

func FilesForRootSource(source vendors.ReadSource, files []string, rootID string) []string {
	byID := make(map[string]string, len(files))
	children := map[string][]string{}
	for _, file := range files {
		id, parentID, err := readHeaderSource(source, file)
		if err != nil {
			continue
		}
		byID[id] = file
		children[parentID] = append(children[parentID], id)
	}
	selected := map[string]struct{}{}
	queue := []string{rootID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		file := byID[id]
		if file == "" {
			continue
		}
		if _, exists := selected[file]; exists {
			continue
		}
		selected[file] = struct{}{}
		queue = append(queue, children[id]...)
	}
	result := make([]string, 0, len(selected))
	for _, file := range files {
		if _, ok := selected[file]; ok {
			result = append(result, file)
		}
	}
	return result
}

func Scan() (*vendors.SourceScan, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return ScanSource(vendors.LocalReadSource, root)
}

func ScanSource(source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	return vendors.ScanSource(source, root)
}
