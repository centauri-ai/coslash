package opencode

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func Root() (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome != "" {
		return filepath.Join(dataHome, "opencode", "opencode.db"), nil
	}
	if executable, err := exec.LookPath("opencode"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, executable, "db", "path").Output(); err == nil {
			path := strings.TrimSpace(string(output))
			if filepath.IsAbs(path) {
				return filepath.Clean(path), nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}

func open() (*sql.DB, error) {
	path, err := Root()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := (&url.URL{
		Scheme: "file",
		Path:   path,
		// does not use immutable=1, to ignore WAL files
		RawQuery: url.Values{
			"mode":          {"ro"},   // read-only
			"_query_only":   {"1"},    // prevent accidental writes
			"_busy_timeout": {"1000"}, // waits briefly if another process holds a lock
		}.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err := validateSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func validateSchema(db *sql.DB) error {
	statement, err := db.Prepare(`
		SELECT s.id, s.parent_id, s.directory, s.title, s.summary_files, s.summary_diffs,
			s.model, s.cost, s.time_updated, s.time_archived,
			m.id, m.session_id, m.time_created, m.data,
			p.id, p.message_id, p.session_id, p.time_updated, p.data,
			t.session_id, t.content, t.status, t.position
		FROM session AS s, message AS m, part AS p, todo AS t
		WHERE 0
	`)
	if err != nil {
		return fmt.Errorf("unsupported OpenCode database schema; update OpenCode or coSlash to a compatible version: %w", err)
	}
	return statement.Close()
}
