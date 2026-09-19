package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const rolloutIDPattern = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`

// rolloutID matches a UUID embedded in a rollout filename, e.g.
// rollout-2026-07-10T14-11-18-019f4dde-db5b-7100-bdc0-09b5aaaac56f.jsonl
var rolloutID = regexp.MustCompile(rolloutIDPattern)

var forkedRolloutIDs = regexp.MustCompile(`(` + rolloutIDPattern + `)_(` + rolloutIDPattern + `)\.jsonl$`)

func SessionIDFromRollout(path string) string {
	_, threadID := rolloutIDsFromPath(path)
	return threadID
}

// Forked rollouts use <root-session-id>_<thread-id>; session_meta may keep the
// root ID instead of the final thread identity.
func rolloutIDsFromPath(path string) (rootID, threadID string) {
	base := filepath.Base(path)
	if ids := forkedRolloutIDs.FindStringSubmatch(base); len(ids) == 3 {
		return ids[1], ids[2]
	}
	id := rolloutID.FindString(base)
	if id == "" {
		return "", ""
	}
	return id, id
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
		return "", "", fmt.Errorf("%w: %w", vendors.ErrInvalidData, err)
	}
	rootID, threadID := rolloutIDsFromPath(path)
	if row.Type != "session_meta" {
		return "", "", fmt.Errorf("%w: first row type %q is not session_meta", vendors.ErrInvalidData, row.Type)
	}
	if threadID == "" {
		return "", "", fmt.Errorf("%w: rollout filename has no session ID", vendors.ErrInvalidData)
	}
	if row.Payload.ID == threadID {
		return threadID, row.Payload.ParentThreadID, nil
	}
	if rootID != threadID && row.Payload.ID == rootID && sharedRootIdentifiesFork(row.Payload, rootID) {
		return threadID, row.Payload.ParentThreadID, nil
	}
	return "", "", fmt.Errorf(
		"%w: header session ID %q does not identify filename thread ID %q",
		vendors.ErrInvalidData, row.Payload.ID, threadID,
	)
}

func sharedRootIdentifiesFork(payload codexPayload, rootID string) bool {
	if payload.HistoryBase.ThreadID != "" {
		return payload.SessionID == "" || payload.SessionID == rootID
	}
	source, _ := jsonString(payload.Source)
	return payload.SessionID == rootID &&
		source == "vscode" &&
		payload.ThreadSource == "user" &&
		payload.HistoryMode == "paginated" &&
		(payload.Originator == "Codex Desktop" || payload.Originator == "codex_work_desktop")
}

func IsRootRollout(path string) (bool, error) {
	_, parentID, err := readHeader(path)
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
