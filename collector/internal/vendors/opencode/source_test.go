package opencode

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListAndExactFamilyUseSameMemberOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("OPENCODE_DB", filepath.Join(home, "opencode.db"))
	setOpenCodeVersionDetectorForTest(t, func() (string, error) { return "2.0.30", nil })
	original := loadProcessesContext
	loadProcessesContext = func(context.Context) ([]tuiProcess, error) { return nil, nil }
	t.Cleanup(func() { loadProcessesContext = original })
	db, err := sql.Open("sqlite", os.Getenv("OPENCODE_DB"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_root', NULL, '/work', 'root', NULL, NULL, NULL, NULL, 0, 1, 1, NULL)`,
		`INSERT INTO session_v2 VALUES ('ses_z', 'ses_root', '/work', 'z', NULL, NULL, NULL, NULL, 0, 1, 2, NULL)`,
		`INSERT INTO session_v2 VALUES ('ses_a', 'ses_root', '/work', 'a', NULL, NULL, NULL, NULL, 0, 1, 2, NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	listed, _, err := CollectContext(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	exact, _, err := GetSessionFamily("ses_root")
	if err != nil {
		t.Fatal(err)
	}
	var listIDs, exactIDs []string
	for _, item := range listed {
		listIDs = append(listIDs, item.Session.ID)
	}
	for _, item := range exact {
		exactIDs = append(exactIDs, item.Session.ID)
	}
	if !reflect.DeepEqual(listIDs, []string{"ses_root", "ses_a", "ses_z"}) || !reflect.DeepEqual(listIDs, exactIDs) {
		t.Fatalf("list = %v, exact = %v", listIDs, exactIDs)
	}
}

func TestCollectionDistinguishesUnavailableLivenessFromNoLiveProcess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	path, err := pluginPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("installed"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(home, "opencode.db")
	t.Setenv("OPENCODE_DB", dbPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_incomplete', NULL, '/work', 'incomplete', NULL, NULL, NULL, NULL, 0, 100, 200, NULL)`,
		`INSERT INTO session_message VALUES ('a', 'ses_incomplete', 'assistant', 1, 200, 200, '{"time":{"created":200}}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	original := loadProcessesContext
	t.Cleanup(func() { loadProcessesContext = original })
	for _, processErr := range []error{nil, errors.New("process enumeration failed")} {
		loadProcessesContext = func(context.Context) ([]tuiProcess, error) { return nil, processErr }
		parsed, metadata, err := CollectContext(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(parsed) != 1 || parsed[0].StatusHint == nil || *parsed[0].StatusHint != "busy" {
			t.Fatalf("collection lost the incomplete turn: %#v", parsed)
		}
		if metadata.LivenessChecked != (processErr == nil) {
			t.Fatalf("collection liveness checked = %t; process error = %v", metadata.LivenessChecked, processErr)
		}
		_, metadata, err = GetSessionFamily("ses_incomplete")
		if err != nil {
			t.Fatal(err)
		}
		if metadata.LivenessChecked != (processErr == nil) {
			t.Fatalf("exact family liveness checked = %t; process error = %v", metadata.LivenessChecked, processErr)
		}
	}
}
