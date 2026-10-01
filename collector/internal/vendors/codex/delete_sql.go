package codex

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite"
)

func safeDeletePath(root, path string) error {
	if !filepath.IsAbs(root) {
		return errors.New("relative vendor root")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path outside vendor root")
	}
	current := root
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in artifact path")
		}
	}
	return nil
}

// Exact ownership from Codex 0.159.3 migrations; nil denotes shared bookkeeping.
var deleteDBOwners = map[string]map[string][]string{
	"state_5.sqlite": {
		"threads": {"id"}, "thread_dynamic_tools": {"thread_id"}, "thread_spawn_edges": {"parent_thread_id", "child_thread_id"},
		"thread_attachments": {"thread_id"}, "thread_artifacts": {"thread_id"},
		"backfill_state": nil, "remote_control_enrollments": nil, "external_agent_config_imports": nil, "thread_sections": nil,
		"rollout_migration_state": nil, "rollout_migration_skipped_rollouts": nil,
		"projects": nil, "project_roots": nil, "project_idempotency_keys": nil,
	},
	"agent_message_board_1.sqlite": {"channels": {"board"}, "posts": {"board"}, "subscriptions": {"board"}, "subscription_opt_outs": {"board"}, "deleted_boards": nil},
	"logs_2.sqlite":                {"logs": {"thread_id"}},
	"goals_1.sqlite":               {"thread_goals": {"thread_id"}, "thread_goal_continuation_deferrals": {"thread_id"}},
	"memories_1.sqlite":            {"stage1_outputs": {"thread_id"}, "jobs": {"job_key"}, "consolidation_progress": nil},
	"memories_v2_1.sqlite":         {"stage1_outputs": {"thread_id"}, "jobs": {"job_key"}, "consolidation_progress": nil},
	"queue_1.sqlite":               {"queued_items": {"thread_id"}, "queued_thread_revisions": {"thread_id"}},
	"thread_history_1.sqlite":      {"thread_turns": {"thread_id"}, "thread_items": {"thread_id"}, "thread_history_projection_state": {"thread_id"}, "thread_realtime_items": {"thread_id"}},
}

func deleteDatabaseFiles(ctx context.Context, root string, ids map[string]bool, paths []string, absent, remove bool) error {
	if err := safeDeletePath(root, root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sqlite") {
			continue
		}
		owners, ok := deleteDBOwners[name]
		if !ok {
			return errors.New("unsupported Codex database")
		}
		path := filepath.Join(root, name)
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			if err := safeDeletePath(root, path+suffix); err != nil {
				return err
			}
		}
		uri, err := deleteDatabaseURI(path)
		if err != nil {
			return err
		}
		if remove {
			uri = strings.Replace(uri, "mode=ro", "mode=rw", 1)
		}
		db, err := sql.Open("sqlite", uri)
		if err != nil {
			return err
		}
		if remove {
			err = cleanupDeleteDB(ctx, db, owners, ids, paths, name == "agent_message_board_1.sqlite")
		} else {
			err = checkDeleteDB(ctx, db, owners, ids, paths, absent, false)
		}
		if err := errors.Join(err, db.Close()); err != nil {
			return err
		}
	}
	return nil
}

func cleanupDeleteDB(ctx context.Context, db *sql.DB, owners map[string][]string, ids map[string]bool, paths []string, board bool) error {
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkDeleteDB(ctx, tx, owners, ids, paths, false, false); err != nil {
		return err
	}
	if err := checkDeleteDB(ctx, tx, owners, ids, paths, true, true); err != nil {
		return err
	}
	if board {
		for id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO deleted_boards(board) VALUES (?)`, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func deleteDatabaseURI(path string) (string, error) {
	path = strings.ReplaceAll(path, `\`, "/")
	if strings.HasPrefix(path, "//") {
		return "", errors.New("UNC SQLite root is unsupported")
	}
	if len(path) > 1 && path[1] == ':' {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	return uri.String(), nil
}

type deleteDBReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func deleteDBTables(ctx context.Context, db deleteDBReader) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func checkDeleteDB(ctx context.Context, db deleteDBReader, owners map[string][]string, ids map[string]bool, paths []string, absent, remove bool) error {
	var executable int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type IN ('trigger','view')`).Scan(&executable); err != nil {
		return err
	}
	if executable > 0 {
		return errors.New("unsupported Codex database executable schema")
	}
	names, err := deleteDBTables(ctx, db)
	if err != nil {
		return err
	}
	if remove {
		slices.Sort(names)
	}
	for _, table := range names {
		columns, ok := owners[table]
		if table == "_sqlx_migrations" || table == "sqlite_sequence" {
			ok = true
		}
		if !ok {
			return errors.New("unsupported Codex database table")
		}
		for _, column := range columns {
			query := `SELECT count(*) FROM "` + table + `" AS owned WHERE owned."` + column + `" = ?`
			if remove {
				query = `DELETE FROM "` + table + `" AS owned WHERE owned."` + column + `"=?`
			}
			if table == "jobs" {
				query += ` AND owned.kind='memory_stage1'`

			}

			for value := range ids {
				if remove {
					if _, err := db.ExecContext(ctx, query, value); err != nil {
						return err
					}
					continue
				}
				var count int
				if err := db.QueryRowContext(ctx, query, value).Scan(&count); err != nil {
					return err
				}
				if absent && count > 0 {
					return errors.New("session database row remains")
				}
			}
		}
		if columns == nil {
			required := map[string]string{
				"_sqlx_migrations": "version,description,installed_on,success,checksum,execution_time", "sqlite_sequence": "name,seq",
				"deleted_boards": "board", "rollout_migration_state": "migration_id,last_checked_thread_created_at,last_checked_thread_id,updated_at",
				"rollout_migration_skipped_rollouts": "migration_id,rollout_path,rollout_size_bytes,rollout_modified_at_ns,skip_reason,skipped_at",
				"backfill_state":                     "id,status,last_watermark,last_success_at,updated_at",
				"remote_control_enrollments":         "websocket_url,account_id,app_server_client_name,server_id,environment_id,server_name,updated_at",
				"external_agent_config_imports":      "import_id,completed_at_ms,successes,failures", "thread_sections": "id,name",
				"projects": "id,name,metadata,position,created_at_ms,updated_at_ms", "project_roots": "project_id,position,path",
				"project_idempotency_keys": "key,project_id,created_at_ms", "consolidation_progress": "singleton,max_thread_count",
			}
			for _, column := range strings.Split(required[table], ",") {
				if column != "" {
					if _, err := db.ExecContext(ctx, `SELECT owned."`+column+`" FROM "`+table+`" AS owned LIMIT 0`); err != nil {
						return err
					}
				}
			}
		}
		if !absent && (table == "thread_spawn_edges" || table == "threads") {
			query := `SELECT parent_thread_id,child_thread_id FROM thread_spawn_edges`
			if table == "threads" {
				query = `SELECT id,rollout_path FROM threads`
			}
			rows, err := db.QueryContext(ctx, query)
			if err != nil {
				return err
			}
			for rows.Next() {
				var owner string
				var value sql.NullString
				if err := rows.Scan(&owner, &value); err != nil {
					rows.Close()
					return err
				}
				if !ids[owner] {
					continue
				}
				if table == "threads" && value.Valid && filepath.IsAbs(value.String) && !slices.Contains(paths, value.String) {
					dir, err := filepath.EvalSymlinks(filepath.Dir(value.String))
					if err == nil {
						value.String = filepath.Join(dir, filepath.Base(value.String))
					}
				}
				if (table == "threads" && value.Valid && !slices.Contains(paths, value.String)) || (table == "thread_spawn_edges" && (!value.Valid || !ids[value.String])) {
					rows.Close()
					return errors.New("database family outside verified inventory")
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}
