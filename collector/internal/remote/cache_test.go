package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestCacheStoreLoadRemove(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	id := "r_0123456789abcdef"
	name := "remote task"
	cached := snapshotForCache([]*session.Session{{
		Agent: "claude", ID: "session-1", Name: &name, StartedAt: 100,
		LastActivityTime: 200, SessionDetails: session.SessionDetails{Turns: 2},
	}}, []AgentCoverage{{Agent: "claude", CandidateFiles: 1, SelectedFiles: 1}}, nil, 0, 300, 20)
	if err := cache.Store(id, cached); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := cache.Load(id)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if loaded.FetchedAtMs != 300 || len(loaded.sessions()) != 1 || loaded.sessions()[0].Turns != 2 {
		t.Fatalf("loaded %+v", loaded)
	}
	if err := cache.RemoveSource(id); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.Load(id); err != nil || ok {
		t.Fatalf("expected missing after remove: ok=%v err=%v", ok, err)
	}
}

func TestCacheSchemaCannotPersistTranscriptOrRemotePaths(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	id := "r_0123456789abcdef"
	secret := "SECRET_TRANSCRIPT_VALUE"
	remotePath := "/home/alice/private-project"
	snapshot := snapshotForCache([]*session.Session{{
		Agent: "codex", ID: "session-1", WorkingDirectory: remotePath,
		SessionDetails: session.SessionDetails{
			FirstPrompt: &secret, Commands: []string{secret},
			Digest:    []session.DigestEntry{{Description: secret}},
			FileEdits: []session.FileEdit{{Path: remotePath}},
		},
	}}, []AgentCoverage{{Agent: "codex", CandidateFiles: 1}}, nil, 0, 1, 1)
	if err := cache.Store(id, snapshot); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "remotes", id, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), remotePath) {
		t.Fatalf("sensitive remote data reached cache: %s", data)
	}
}

func TestCacheIgnoresCorruptOrUnknownVersion(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	id := "r_0123456789abcdef"
	dir := filepath.Join(root, "remotes", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"{nope", `{"version":99,"sessions":[]}`} {
		if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := cache.Load(id); err != nil || ok {
			t.Fatalf("expected ignored cache: ok=%v err=%v", ok, err)
		}
	}
}
