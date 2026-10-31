package syncv4

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func TestSSHClaudeAndCodexV4HTTPRoundTrip(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".claude", "projects", "fixture")
			row := fmt.Sprintf(`{"sessionId":%q,"type":"user","timestamp":"2026-09-28T11:00:00Z","cwd":"/remote/project","message":{"content":"synthetic remote prompt"}}`, fixtureRootID) + "\n"
			if agent == "codex" {
				dir = filepath.Join(home, ".codex", "sessions")
				row = fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"cwd":"/remote/project","timestamp":"2026-09-28T11:00:00Z"}}`, fixtureRootID) + "\n" + `{"type":"event_msg","timestamp":"2026-09-28T11:00:01Z","payload":{"type":"user_message","message":"synthetic remote prompt"}}` + "\n"
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fixtureRootID+".jsonl"), []byte(row), 0o600); err != nil {
				t.Fatal(err)
			}
			spool := t.TempDir()
			manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, OpenSource: func(context.Context, sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
				return sessionbackupproducer.SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
			}})
			prepared, err := manager.Prepare(t.Context(), sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceSSH, SourceID: "r_0123456789abcdef", Agent: agent, SessionID: fixtureRootID})
			if err != nil {
				t.Fatal(err)
			}
			runV4HTTPResume(t, manager, prepared, spool, "Fixture SSH "+agent, false)
		})
	}
}
