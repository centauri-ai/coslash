package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// rolloutID matches the session UUID embedded in a rollout filename, e.g.
// rollout-2026-07-10T14-11-18-019f4dde-db5b-7100-bdc0-09b5aaaac56f.jsonl
var rolloutID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func SessionIDFromRollout(path string) string {
	return rolloutID.FindString(filepath.Base(path))
}

type Header struct {
	ID       string
	ParentID string
}

// ReadHeader reads only the rollout's first session_meta row. Codex writes the
// rollout's own metadata first, including parent_thread_id for subagents; fork
// history may contain more session_meta rows later, but they are not needed for
// file selection.
func ReadHeader(path string) (Header, error) {
	row, err := session.ReadFirstJSONL[codexRow](path)
	if err != nil {
		return Header{}, err
	}
	id := SessionIDFromRollout(path)
	if row.Type != "session_meta" || id == "" || row.Payload.ID != id {
		return Header{}, fmt.Errorf("first row is not this rollout's session metadata")
	}
	return Header{ID: id, ParentID: row.Payload.ParentThreadID}, nil
}

// root/subagents: ~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<session-uuid>.jsonl
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

func Files() ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return vendors.JSONLFilesUnder(root)
}
