package opencode

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var databasePathLookupTimeout = 2 * time.Second
var commandContext = exec.CommandContext

func Root() (string, error) {
	return RootContext(context.Background())
}

func RootContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome != "" {
		return filepath.Join(dataHome, "opencode", "opencode.db"), nil
	}
	if executable, err := exec.LookPath("opencode"); err == nil {
		lookupCtx, cancel := context.WithTimeout(ctx, databasePathLookupTimeout)
		defer cancel()
		if output, err := commandContext(lookupCtx, executable, "db", "path").Output(); err == nil {
			path := strings.TrimSpace(string(output))
			if filepath.IsAbs(path) {
				return filepath.Clean(path), nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}

func open() (*sql.DB, error) {
	return openContext(context.Background())
}

func openContext(ctx context.Context) (*sql.DB, error) {
	path, err := RootContext(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := readOnlyDatabaseDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := validateSchemaContext(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func readOnlyDatabaseDSN(path string) string {
	urlPath := path
	if runtime.GOOS == "windows" {
		urlPath = filepath.ToSlash(path)
		if filepath.VolumeName(path) != "" && !strings.HasPrefix(urlPath, "/") {
			urlPath = "/" + urlPath
		}
	}
	return (&url.URL{
		Scheme: "file",
		Path:   urlPath,
		// does not use immutable=1, to ignore WAL files
		RawQuery: url.Values{
			"mode":          {"ro"},   // read-only
			"_query_only":   {"1"},    // prevent accidental writes
			"_busy_timeout": {"1000"}, // waits briefly if another process holds a lock
		}.Encode(),
	}).String()
}

func validateSchemaContext(ctx context.Context, db *sql.DB) error {
	_, err := sessionSourceContext(ctx, db)
	if err != nil {
		return err
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session'`).Scan(&count); err != nil {
		return err
	}
	checks := []string{}
	if count != 0 {
		checks = append(checks, `SELECT m.id, m.session_id, m.time_created, m.data, p.id, p.message_id, p.time_updated, p.data, t.session_id, t.content, t.status, t.position FROM message AS m, part AS p, todo AS t WHERE 0`)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_v2'`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		checks = append(checks, `SELECT id, session_id, type, seq, time_created, data FROM session_message WHERE 0`)
	}
	for _, query := range checks {
		statement, err := db.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("unsupported OpenCode database schema; update OpenCode or coSlash to a compatible version: %w", err)
		}
		if err := statement.Close(); err != nil {
			return err
		}
	}
	return nil
}

func sessionSourceContext(ctx context.Context, db *sql.DB) (string, error) {
	var v1, v2 bool
	for _, table := range []struct {
		name  string
		found *bool
	}{{"session", &v1}, {"session_v2", &v2}} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table.name).Scan(&count); err != nil {
			return "", err
		}
		*table.found = count != 0
	}
	if !v1 && !v2 {
		return "", fmt.Errorf("unsupported OpenCode database schema: no session table")
	}
	projection := `id, parent_id, directory, COALESCE(title, '') AS title, summary_files,
		summary_diffs, agent, model, cost, time_created, time_updated, time_archived`
	parts := []string{}
	if v2 {
		parts = append(parts, `SELECT `+projection+`, 1 AS v2 FROM session_v2`)
	}
	if v1 {
		query := `SELECT ` + projection + `, 0 AS v2 FROM session`
		if v2 {
			query += ` WHERE NOT EXISTS (SELECT 1 FROM session_v2 WHERE session_v2.id = session.id)`
		}
		parts = append(parts, query)
	}
	source := `WITH sessions AS (` + strings.Join(parts, ` UNION ALL `) + `)`
	statement, err := db.PrepareContext(ctx, source+`, validated AS (
		SELECT id, parent_id, directory, title, summary_files, summary_diffs,
			agent, model, cost, time_created, time_updated, time_archived, v2 FROM sessions WHERE 0
	) SELECT * FROM validated`)
	if err != nil {
		return "", fmt.Errorf("unsupported OpenCode database schema; update OpenCode or coSlash to a compatible version: %w", err)
	}
	if err := statement.Close(); err != nil {
		return "", err
	}
	return source, nil
}
