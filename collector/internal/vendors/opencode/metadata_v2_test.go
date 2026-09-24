package opencode

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLiveCandidatesIncludeV2AndKeepLegacyWithoutDuplicateMessages(t *testing.T) {
	db := testDB(t)
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('shared', NULL, '/work', 'current', NULL, NULL, NULL, NULL, 0, 100, 200, NULL)`,
		`INSERT INTO session_message VALUES ('v2-user', 'shared', 'user', 1, 150, '{"text":"new prompt"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := loadLiveCandidatesContext(context.Background(), db)
	if err != nil || len(candidates) != 1 || candidates[0].id != "shared" || candidates[0].createdAt != 100 ||
		len(candidates[0].userMessages) != 1 || candidates[0].userMessages[0] != 150 {
		t.Fatalf("v2 candidates = %#v, error = %v", candidates, err)
	}
	if _, live := matchLiveSessions([]tuiProcess{{startedAt: 90, directory: "/work"}}, candidates)["shared"]; !live {
		t.Fatal("active v2 session was not discovered")
	}

	for _, statement := range []string{
		`CREATE TABLE session (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`INSERT INTO session VALUES ('shared', NULL, '/old', 'old', NULL, NULL, NULL, NULL, 0, 50, 100, NULL)`,
		`INSERT INTO session VALUES ('legacy', NULL, '/legacy', 'legacy', NULL, NULL, NULL, NULL, 0, 60, 100, NULL)`,
		`INSERT INTO message VALUES ('old-user', 'shared', 75, '{"role":"user"}')`,
		`INSERT INTO message VALUES ('legacy-user', 'legacy', 80, '{"role":"user"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err = loadLiveCandidatesContext(context.Background(), db)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("mixed candidates = %#v, error = %v", candidates, err)
	}
	if candidates[0].id != "legacy" || len(candidates[0].userMessages) != 1 || candidates[0].userMessages[0] != 80 ||
		candidates[1].id != "shared" || candidates[1].directory != filepath.Clean("/work") || len(candidates[1].userMessages) != 1 || candidates[1].userMessages[0] != 150 {
		t.Fatalf("mixed candidates = %#v", candidates)
	}
}
