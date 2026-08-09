package sessioncanvas

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
)

func TestMetadataRenamerUpdatesClaudeStateAtomically(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "live.json")
	if err := os.WriteFile(path, []byte(`{"sessionId":"`+sharedID+`","name":"old","extra":{"keep":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	renamer := MetadataRenamer{Home: home}
	if err := renamer.Rename(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID}, "new"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if json.Unmarshal(data, &record) != nil || record["name"] != "new" || record["nameSource"] != "user" || record["extra"] == nil {
		t.Fatalf("renamed record = %s", data)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err=%v", info.Mode(), err)
	}
}

func TestMetadataRenamerAppendsCodexIndexAndRefusesSymlink(t *testing.T) {
	home := t.TempDir()
	renamer := MetadataRenamer{Home: home}
	identity := contracts.SessionIdentity{Agent: "codex", ID: sharedID}
	if err := renamer.Rename(context.Background(), identity, "new name"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "session_index.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"id":"`+sharedID+`"`) || !strings.Contains(string(data), `"thread_name":"new name"`) {
		t.Fatalf("index = %s", data)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err=%v", info.Mode(), err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if err := renamer.Rename(context.Background(), identity, "unsafe"); err == nil {
		t.Fatal("rename followed a symlinked index")
	}
	if data, _ := os.ReadFile(other); string(data) != "unchanged" {
		t.Fatalf("symlink target changed: %q", data)
	}
}

func TestMetadataRenamerReportsUnsupportedSession(t *testing.T) {
	renamer := MetadataRenamer{Home: t.TempDir()}
	err := renamer.Rename(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID}, "new")
	if !errors.Is(err, ErrRenameUnsupported) {
		t.Fatalf("error = %v", err)
	}
}

func TestCollectorResolverKeepsDuplicateBareIDsVendorScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	claudeDirectory := filepath.Join(home, ".claude", "projects", "fixture")
	codexDirectory := filepath.Join(home, ".codex", "sessions", "2026", "08", "08")
	for _, directory := range []string{claudeDirectory, codexDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	claudeRow, _ := json.Marshal(map[string]any{
		"sessionId": sharedID, "cwd": cwd, "type": "user", "timestamp": "2026-08-08T00:00:00Z",
		"message": map[string]any{"content": "claude request"},
	})
	if err := os.WriteFile(filepath.Join(claudeDirectory, sharedID+".jsonl"), append(claudeRow, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	codexRow, _ := json.Marshal(map[string]any{
		"timestamp": "2026-08-08T00:00:00Z", "type": "session_meta",
		"payload": map[string]any{"id": sharedID, "cwd": cwd, "originator": "codex-tui"},
	})
	if err := os.WriteFile(filepath.Join(codexDirectory, "rollout-2026-08-08T00-00-00-"+sharedID+".jsonl"), append(codexRow, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := CollectorResolver{}
	for _, agent := range []string{"claude", "codex"} {
		resolved, err := resolver.Resolve(context.Background(), contracts.SessionIdentity{Agent: agent, ID: sharedID})
		if err != nil {
			t.Fatalf("resolve %s: %v", agent, err)
		}
		if resolved.Session.Agent != agent || resolved.Session.ID != sharedID || !strings.Contains(resolved.TranscriptPath, "."+agent) {
			t.Fatalf("resolved %s to %#v", agent, resolved)
		}
	}
}
