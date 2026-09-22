package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const maxSessionIndexRowBytes = 1 << 20

// LoadMetadata reads liveness from open rollout handles and names from
// session_index.jsonl. Live rollouts get the "interactive" convention so
// resolveStatus applies the busy/idle refinement.
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

// LoadLiveSessions returns the Codex session IDs whose rollouts are open.
func LoadLiveSessions() (map[string]struct{}, error) {
	return LoadLiveSessionsContext(context.Background())
}

func LoadLiveSessionsContext(ctx context.Context) (map[string]struct{}, error) {
	return loadLiveSessionsContext(ctx)
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

// SessionIndexPath is the only shared Codex metadata path admitted by the
// complete-backup contract.
func SessionIndexPath(home string) string {
	return filepath.Join(home, ".codex", "session_index.jsonl")
}

// ReadSessionIndexRows returns exact matching row bytes, including their
// original line terminator. Any malformed or duplicate row makes attribution
// incomplete; callers must not silently omit it from a complete backup.
func ReadSessionIndexRows(source vendors.ReadSource, home string, ids map[string]bool) (map[string][]byte, bool, error) {
	return ReadSessionIndexRowsContext(context.Background(), source, home, ids)
}

func ReadSessionIndexRowsContext(ctx context.Context, source vendors.ReadSource, home string, ids map[string]bool) (map[string][]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	rows := map[string][]byte{}
	file, err := source.Open(SessionIndexPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return rows, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(contextReader{ctx: ctx, reader: file}, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, true, err
		}
		line, readErr := readBoundedIndexLine(reader)
		if errors.Is(readErr, vendors.ErrInvalidData) {
			return nil, true, readErr
		}
		if len(bytes.Trim(line, " \t\r\n")) > 0 {
			var entry sessionIndexEntry
			if err := json.Unmarshal(line, &entry); err != nil {
				return nil, true, fmt.Errorf("%w: malformed session index row", vendors.ErrInvalidData)
			}
			id, ok := jsonString(entry.ID)
			if !ok || id == "" {
				return nil, true, fmt.Errorf("%w: unattributable session index row", vendors.ErrInvalidData)
			}
			if ids[id] {
				if _, duplicate := rows[id]; duplicate {
					return nil, true, fmt.Errorf("%w: duplicate attributed session index row", vendors.ErrInvalidData)
				}
				rows[id] = append([]byte(nil), line...)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, true, readErr
		}
	}
	return rows, true, nil
}

func readBoundedIndexLine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, 64*1024)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxSessionIndexRowBytes {
			return nil, fmt.Errorf("%w: session index row exceeds limit", vendors.ErrInvalidData)
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
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
	return loadThreadNamesSourceContext(ctx, vendors.LocalReadSource, SessionIndexPath(home))
}

func LoadRemoteMetadata(source vendors.ReadSource, home string) (*vendors.SessionMetadata, error) {
	names, err := loadThreadNamesSource(source, SessionIndexPath(home))
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
