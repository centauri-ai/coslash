package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// rolloutID matches a UUID embedded in a rollout filename, e.g.
// rollout-2026-07-10T14-11-18-019f4dde-db5b-7100-bdc0-09b5aaaac56f.jsonl
var rolloutID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// rolloutIDs returns every UUID a rollout filename carries, in order. A forked
// rollout is named <root-session-id>_<thread-id>, so the thread the file
// actually holds is always the last one.
func rolloutIDs(path string) []string {
	return rolloutID.FindAllString(filepath.Base(path), -1)
}

func SessionIDFromRollout(path string) string {
	ids := rolloutIDs(path)
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

func readHeader(path string) (string, string, error) {
	return readHeaderSource(vendors.LocalReadSource, path)
}

func readHeaderSource(source vendors.ReadSource, path string) (string, string, error) {
	return readHeaderSourceContext(context.Background(), source, path)
}

func readHeaderSourceContext(ctx context.Context, source vendors.ReadSource, path string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	file, err := source.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	var row codexRow
	if err := json.NewDecoder(contextReader{ctx: ctx, reader: file}).Decode(&row); err != nil {
		return "", "", fmt.Errorf("%w: %w", vendors.ErrInvalidData, err)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	ids := rolloutIDs(path)
	if row.Type != "session_meta" {
		return "", "", fmt.Errorf("%w: first row type %q is not session_meta", vendors.ErrInvalidData, row.Type)
	}
	if len(ids) == 0 {
		return "", "", fmt.Errorf("%w: rollout filename has no session ID", vendors.ErrInvalidData)
	}
	threadID := ids[len(ids)-1]
	// A fork inlines its ancestors' session_meta rows and may label every one
	// of them with the root ID, so this first row only has to name a thread the
	// filename names too.
	if !slices.Contains(ids, row.Payload.ID) {
		return "", "", fmt.Errorf(
			"%w: header session ID %q does not identify filename thread ID %q",
			vendors.ErrInvalidData, row.Payload.ID, threadID,
		)
	}
	if row.Payload.ID != threadID {
		// An ancestor wrote this row, so its parentage is not the fork's.
		return threadID, "", nil
	}
	return threadID, row.Payload.ParentThreadID, nil
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
	return FilesContext(context.Background())
}

func FilesContext(ctx context.Context) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return filesForHomeSourceContext(ctx, vendors.LocalReadSource, home)
}

func FilesSource(source vendors.ReadSource, root string) ([]string, error) {
	return FilesSourceContext(context.Background(), source, root)
}

func FilesSourceContext(ctx context.Context, source vendors.ReadSource, root string) ([]string, error) {
	return vendors.JSONLFilesUnderSourceContext(ctx, source, root)
}

// filesForHomeSourceContext lists Codex rollouts from both supported trees.
// Active copies win when a session is present in both locations, so a move to
// archived storage cannot create duplicate cards or duplicate family members.
func filesForHomeSourceContext(ctx context.Context, source vendors.ReadSource, home string) ([]string, error) {
	active, err := FilesSourceContext(ctx, source, SessionsRoot(home))
	if err != nil {
		return nil, err
	}
	archived, err := FilesSourceContext(ctx, source, ArchivedDir(home))
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(active)+len(archived))
	seen := make(map[string]struct{}, len(active)+len(archived))
	for _, group := range [][]string{active, archived} {
		for _, file := range group {
			if id := SessionIDFromRollout(file); id != "" {
				if _, exists := seen[id]; exists {
					continue
				}
				seen[id] = struct{}{}
			}
			files = append(files, file)
		}
	}
	return files, nil
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
	selected, _ := FilesSinceSourceContext(context.Background(), source, files, live, since)
	return selected
}

func FilesSinceSourceContext(
	ctx context.Context,
	source vendors.ReadSource,
	files []string,
	live map[string]string,
	since int64,
) ([]string, error) {
	byID := make(map[string]string, len(files))
	children := map[string][]string{}
	selected := map[string]struct{}{}
	queue := []string{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, parentID, err := readHeaderSourceContext(ctx, source, file)
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := selected[file]; ok {
			result = append(result, file)
		}
	}
	return result, nil
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
	return ScanContext(context.Background())
}

func ScanContext(ctx context.Context) (*vendors.SourceScan, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return ScanSourceContext(ctx, vendors.LocalReadSource, root)
}

func ScanSource(source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	return ScanSourceContext(context.Background(), source, root)
}

func ScanSourceContext(ctx context.Context, source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	return vendors.ScanSourceContext(ctx, source, root)
}
