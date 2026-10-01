package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path, err := effectiveDatabaseRoot(ctx, home)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// Collection retains its fallback; destructive callers require a verified root.
	if path != "" {
		return path, nil
	}
	return "", err
}

func effectiveDatabaseRoot(ctx context.Context, home string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dataRoot := filepath.Join(home, ".local", "share", "opencode")
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		if !filepath.IsAbs(data) {
			return "", errors.New("invalid OpenCode data root")
		}
		dataRoot = filepath.Join(data, "opencode")
	}
	fallback := filepath.Join(dataRoot, "opencode.db")
	if override := os.Getenv("OPENCODE_DB"); override != "" {
		if override == ":memory:" {
			return "", errors.New("unverified OpenCode data root")
		}
		if filepath.IsAbs(override) {
			return filepath.Clean(override), nil
		}
		if clean := filepath.Clean(override); clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", errors.New("invalid OpenCode data root")
		}
		return filepath.Join(dataRoot, override), nil
	}
	executable, err := exec.LookPath("opencode")
	if err != nil {
		return fallback, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, databasePathLookupTimeout)
	defer cancel()
	isolated, err := os.MkdirTemp("", "coslash-opencode-root-")
	if err != nil {
		return fallback, err
	}
	defer os.RemoveAll(isolated)
	cmd := commandContext(lookupCtx, executable, "db", "path")
	cmd.Dir = isolated
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "OPENCODE_") && !strings.HasPrefix(key, "XDG_") && key != "HOME" && key != "USERPROFILE" && key != "HOMEDRIVE" && key != "HOMEPATH" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for key, value := range map[string]string{
		"HOME": isolated, "USERPROFILE": isolated, "OPENCODE_TEST_HOME": isolated,
		"HOMEDRIVE": filepath.VolumeName(isolated), "HOMEPATH": strings.TrimPrefix(isolated, filepath.VolumeName(isolated)),
		"XDG_DATA_HOME": filepath.Join(isolated, "data"), "XDG_CONFIG_HOME": filepath.Join(isolated, "config"), "XDG_CACHE_HOME": filepath.Join(isolated, "cache"), "XDG_STATE_HOME": filepath.Join(isolated, "state"),
		"OPENCODE_CONFIG_DIR": filepath.Join(isolated, "config", "opencode"), "OPENCODE_CONFIG_CONTENT": `{"plugin":[],"mcp":{}}`,
		"OPENCODE_DISABLE_AUTOUPDATE": "1", "OPENCODE_DISABLE_MODELS_FETCH": "1", "OPENCODE_DISABLE_CHANNEL_DB": os.Getenv("OPENCODE_DISABLE_CHANNEL_DB"),
	} {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := boundedCommandOutput(cmd, 4096)
	if err != nil {
		return fallback, err
	}
	path := strings.TrimSpace(string(output))
	if !filepath.IsAbs(path) {
		return fallback, fmt.Errorf("unverified OpenCode database path")
	}
	path = filepath.Clean(path)
	if sameDirectory(filepath.Dir(path), filepath.Join(isolated, "data", "opencode")) {
		return filepath.Join(dataRoot, filepath.Base(path)), nil
	}
	if sameDirectory(filepath.Dir(path), dataRoot) {
		return path, nil
	}
	return fallback, fmt.Errorf("unverified OpenCode data root")
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("OpenCode process output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func boundedCommandOutput(cmd *exec.Cmd, limit int) ([]byte, error) {
	output := &boundedOutput{limit: limit}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
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
