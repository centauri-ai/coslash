package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestParseFamilyFilesSourceDoesNotExposePromptAsName(t *testing.T) {
	home := t.TempDir()
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	file := filepath.Join(home, ".codex", "sessions", "rollout-2026-07-10T14-11-18-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"` + id + `"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:11:19Z","type":"event_msg","payload":{"type":"user_message","message":"private first prompt"}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFamilyFilesSource(vendors.LocalReadSource, home, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 || parsed[0].Name != "" {
		t.Fatalf("remote parsed name exposed prompt: %#v", parsed)
	}
}
