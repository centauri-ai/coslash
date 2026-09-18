package cursor

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestCommitObservationsRequireCompletedCommitCommand(t *testing.T) {
	const before = "1111111111111111111111111111111111111111"
	const after = "2222222222222222222222222222222222222222"
	checkpointOnly := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + after + `"}}},"toolFormerData":{"name":"edit_file","status":"completed"}}`
	if got := commitObservationsFromIDEBubble(checkpointOnly); len(got) != 0 {
		t.Fatalf("checkpoint-only observations = %v, want none", got)
	}

	commit := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + after + `"}}},"toolFormerData":{"name":"run_terminal_cmd","status":"completed","rawArgs":"{\"command\":\"git commit -m 'ship it'\"}","result":"[main 2222222] ship it"}}`
	got := commitObservationsFromIDEBubble(commit)
	if len(got) != 1 || got[0].Hash != after {
		t.Fatalf("commit observations = %v, want hash %s", got, after)
	}

	checkout := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"3333333333333333333333333333333333333333"}}},"toolFormerData":{"name":"run_terminal_cmd","status":"completed","rawArgs":"{\"command\":\"git commit -m temp && git checkout other\"}","result":"[main 2222222] temp"}}`
	if got := commitObservationsFromIDEBubble(checkout); len(got) != 0 {
		t.Fatalf("compound command observations = %v, want none", got)
	}
}

func TestLoadMetadataForSessionsReturnsOnlyRequestedIDs(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	target := "00000000-0000-4000-8000-000000000001"
	other := "00000000-0000-4000-8000-000000000002"
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value, createdAt, lastUpdatedAt) VALUES
		(?, '{"name":"Target"}', 10, 20), (?, '{"name":"Other"}', 30, 40)`, target, other); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadMetadataForSessions(home, []string{target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup(target); got == nil || got.Name != "Target" || got.LastActivityAt != 20 {
		t.Fatalf("target metadata = %#v", got)
	}
	if got := metadata.Lookup(other); got != nil {
		t.Fatalf("unrequested metadata = %#v, want nil", got)
	}
}

func TestLoadMetadataForSessionsCanonicalizesStoredIDs(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	id := "abcdefab-cdef-4abc-8def-abcdefabcdef"
	upperID := strings.ToUpper(id)
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value, createdAt, lastUpdatedAt) VALUES (?, '{"name":"Uppercase"}', 10, 20)`, upperID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`,
		"bubbleId:"+upperID+":1", `{"createdAt":100,"modelInfo":{"modelName":"gpt-5"}}`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadMetadataForSessions(home, []string{id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup(id); got == nil || got.Name != "Uppercase" || got.Model != "gpt-5" || got.LastActivityAt != 20 {
		t.Fatalf("canonical metadata = %#v", got)
	}
	if got := metadata.Lookup(upperID); got != nil {
		t.Fatalf("uppercase metadata key = %#v, want nil", got)
	}
}

func TestLoadIDEModelsOrdersNumericTimestampsDeterministically(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	const id = "00000000-0000-4000-8000-000000000001"
	for _, row := range []struct{ key, value string }{
		{"bubbleId:" + id + ":z", `{"createdAt":200,"modelInfo":{"modelName":"gpt-5"}}`},
		{"bubbleId:" + id + ":a", `{"createdAt":200,"modelInfo":{"modelName":"gpt-4"}}`},
		{"bubbleId:" + id + ":old", `{"createdAt":100,"modelInfo":{"modelName":"gpt-3"}}`},
	} {
		if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`, row.key, row.value); err != nil {
			t.Fatal(err)
		}
	}

	metadata := vendors.EmptySessionMetadata()
	loadIDEModelsDB(metadata, db, nil)
	if got := metadata.Session(id).Model; got != "gpt-5" {
		t.Fatalf("model = %q, want newest model with deterministic key tie-break", got)
	}
}

func createMetadataTestDB(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE composerHeaders (
		composerId TEXT PRIMARY KEY,
		value TEXT,
		createdAt INTEGER,
		lastUpdatedAt INTEGER
	); CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`)
	return err
}
