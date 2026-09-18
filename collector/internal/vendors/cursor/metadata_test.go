package cursor

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitObservationsRequireCompletedCommitCommand(t *testing.T) {
	const before = "1111111111111111111111111111111111111111"
	const after = "2222222222222222222222222222222222222222"
	checkpointOnly := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + after + `"}}},"toolFormerData":{"name":"edit_file","status":"completed"}}`
	if got := commitObservationsFromIDEBubble(checkpointOnly); len(got) != 0 {
		t.Fatalf("checkpoint-only observations = %v, want none", got)
	}

	commit := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + after + `"}}},"toolFormerData":{"name":"run_terminal_cmd","status":"completed","rawArgs":"{\"command\":\"git commit -m 'ship it'\"}"}}`
	got := commitObservationsFromIDEBubble(commit)
	if len(got) != 1 || got[0].Hash != after {
		t.Fatalf("commit observations = %v, want hash %s", got, after)
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
	)`)
	return err
}
