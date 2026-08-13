package codex

import (
	"bufio"
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
	live, err := liveSessions()
	if err != nil {
		return nil, err
	}
	names, err := loadThreadNames()
	if err != nil {
		return nil, err
	}
	metadata := vendors.EmptySessionMetadata()
	metadata.Names = names
	for id := range live {
		metadata.Live[id] = "interactive"
	}
	return metadata, nil
}

func liveSessions() (map[string]struct{}, error) {
	openCodexSessions, err := exec.Command("lsof", "-a", "-c", "codex", "-Fn").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) || errors.As(err, &exitErr) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	live := map[string]struct{}{}
	for line := range strings.SplitSeq(string(openCodexSessions), "\n") {
		if !strings.HasPrefix(line, "n") || !strings.HasSuffix(line, ".jsonl") {
			continue
		}
		if id := SessionIDFromRollout(line[1:]); id != "" {
			live[id] = struct{}{}
		}
	}
	return live, nil
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
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".codex", "session_index.jsonl")
	names := map[string]string{}
	file, err := os.Open(path)
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
