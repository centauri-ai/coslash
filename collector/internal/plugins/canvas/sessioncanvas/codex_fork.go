package sessioncanvas

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	codexvendor "github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

const (
	codexForkIdentityTimeout = 3 * time.Second
	codexForkPollInterval    = 25 * time.Millisecond
	maxCodexRollouts         = 100_000
	maxCodexHeaderBytes      = 64 << 10
	failedForkStopTimeout    = 2 * time.Second
)

type codexForkSnapshot struct {
	root  string
	known map[string]struct{}
}

func stopFailedFork(terminals TerminalService, terminalID string) {
	ctx, cancel := context.WithTimeout(context.Background(), failedForkStopTimeout)
	defer cancel()
	_ = terminals.Stop(ctx, terminalID)
}

func snapshotCodexFork(transcriptPath string) (codexForkSnapshot, error) {
	root, err := codexSessionsRoot(transcriptPath)
	if err != nil {
		return codexForkSnapshot{}, err
	}
	known := map[string]struct{}{}
	if err := visitCodexRollouts(root, func(path, id string) error {
		known[id] = struct{}{}
		return nil
	}); err != nil {
		return codexForkSnapshot{}, err
	}
	return codexForkSnapshot{root: root, known: known}, nil
}

func awaitCodexForkChild(ctx context.Context, snapshot codexForkSnapshot, parentID string) (string, error) {
	timer := time.NewTimer(codexForkIdentityTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(codexForkPollInterval)
	defer ticker.Stop()
	for {
		child, err := findCodexForkChild(snapshot, parentID)
		if err != nil {
			return "", err
		}
		if child != "" {
			return child, nil
		}
		select {
		case <-ctx.Done():
			return "", errors.New("session canvas: Codex fork identity cancelled")
		case <-timer.C:
			return "", errors.New("session canvas: Codex fork identity timed out")
		case <-ticker.C:
		}
	}
}

func findCodexForkChild(snapshot codexForkSnapshot, parentID string) (string, error) {
	child := ""
	err := visitCodexRollouts(snapshot.root, func(path, id string) error {
		if _, existed := snapshot.known[id]; existed {
			return nil
		}
		forkedFrom, ok := readCodexForkHeader(path, id)
		if !ok || forkedFrom != parentID {
			return nil
		}
		if child != "" && child != id {
			return errors.New("session canvas: Codex fork identity is ambiguous")
		}
		child = id
		return nil
	})
	return child, err
}

func codexSessionsRoot(transcriptPath string) (string, error) {
	absolute, err := filepath.Abs(transcriptPath)
	if err != nil {
		return "", errors.New("session canvas: Codex transcript path is invalid")
	}
	current := filepath.Dir(filepath.Clean(absolute))
	for range 8 {
		if filepath.Base(current) == "sessions" {
			root, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", errors.New("session canvas: Codex sessions root is unavailable")
			}
			return root, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", errors.New("session canvas: Codex sessions root is invalid")
}

func visitCodexRollouts(root string, visit func(path, id string) error) error {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("session canvas: Codex sessions are unavailable")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "rollout-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		id := codexvendor.SessionIDFromRollout(path)
		if id == "" {
			return nil
		}
		count++
		if count > maxCodexRollouts {
			return errors.New("session canvas: too many Codex sessions")
		}
		return visit(path, id)
	})
	if err != nil {
		return err
	}
	return nil
}

func readCodexForkHeader(path, expectedID string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			ID           string `json:"id"`
			ForkedFromID string `json:"forked_from_id"`
		} `json:"payload"`
	}
	if json.NewDecoder(io.LimitReader(file, maxCodexHeaderBytes)).Decode(&row) != nil || row.Type != "session_meta" || row.Payload.ID != expectedID {
		return "", false
	}
	return row.Payload.ForkedFromID, true
}
