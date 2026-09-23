package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// LoadMetadata reads liveness from lsof (the only signal Codex leaves — no pid
// file, no status field) and names from session_index.jsonl. Live rollouts
// get the "interactive" convention so resolveStatus applies the busy/idle
// refinement.
func LoadMetadata() (*vendors.SessionMetadata, error) {
	return LoadMetadataContext(context.Background())
}

func LoadMetadataContext(ctx context.Context) (*vendors.SessionMetadata, error) {
	live, err := LoadLiveSessionsContext(ctx)
	if err != nil {
		return nil, err
	}
	names, err := loadThreadNamesContext(ctx)
	if err != nil {
		return nil, err
	}
	metadata := vendors.EmptySessionMetadata()
	for id, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		metadata.Session(id).Name = name
	}
	for id := range live {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		metadata.Session(id).Live = "interactive"
	}
	return metadata, nil
}

// LoadLiveSessions returns the Codex session IDs that lsof reports as open.
func LoadLiveSessions() (map[string]struct{}, error) {
	return LoadLiveSessionsContext(context.Background())
}

func LoadLiveSessionsContext(ctx context.Context) (map[string]struct{}, error) {
	openCodexSessions, err := exec.CommandContext(ctx, "lsof", "-a", "-c", "codex", "-Fn").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) || errors.As(err, &exitErr) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return LiveSessionIDs(string(openCodexSessions)), nil
}

// LiveSessionIDs reads rollout session IDs from `lsof -Fn` output.
func LiveSessionIDs(output string) map[string]struct{} {
	live := map[string]struct{}{}
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.HasPrefix(line, "n") || !strings.HasSuffix(line, ".jsonl") {
			continue
		}
		if id := SessionIDFromRollout(line[1:]); id != "" {
			live[id] = struct{}{}
		}
	}
	return live
}

type sessionIndexEntry struct {
	ID         json.RawMessage `json:"id"`
	ThreadName json.RawMessage `json:"thread_name"`
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func loadThreadNames() (map[string]string, error) {
	return loadThreadNamesContext(context.Background())
}

func loadThreadNamesContext(ctx context.Context) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadThreadNamesSourceContext(ctx, vendors.LocalReadSource, filepath.Join(home, ".codex", "session_index.jsonl"))
}

func LoadRemoteMetadata(source vendors.ReadSource, home string) (*vendors.SessionMetadata, error) {
	names, err := loadThreadNamesSource(source, filepath.Join(home, ".codex", "session_index.jsonl"))
	if err != nil {
		return nil, err
	}
	metadata := vendors.EmptySessionMetadata()
	for id, name := range names {
		metadata.Session(id).Name = name
	}
	return metadata, nil
}

func loadThreadNamesSource(source vendors.ReadSource, path string) (map[string]string, error) {
	return loadThreadNamesSourceContext(context.Background(), source, path)
}

func loadThreadNamesSourceContext(ctx context.Context, source vendors.ReadSource, path string) (map[string]string, error) {
	names := map[string]string{}
	file, err := source.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		log.Printf("session index %q: %v; continuing without thread names", path, err)
		return names, nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry sessionIndexEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			log.Printf("session index %q: skipping malformed row: %v", path, err)
			continue
		}
		id, okID := jsonString(entry.ID)
		threadName, okName := jsonString(entry.ThreadName)
		if okID && id != "" && okName {
			names[id] = threadName
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("session index %q: read error: %v; using partial thread names", path, err)
	}
	return names, nil
}
