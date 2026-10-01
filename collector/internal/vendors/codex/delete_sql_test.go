package codex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sqlDeleteRootID = "11111111-2222-4333-8444-555555555555"
const sqlDeleteChildID = "66666666-7777-4888-8999-aaaaaaaaaaaa"
const sqlDeleteNeighborID = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"

func newDeleteSQLDB(t *testing.T, root, name, schema string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDeleteDatabaseURI(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`C:\Users\synthetic\.codex\state_5.sqlite`, "file:///C:/Users/synthetic/.codex/state_5.sqlite?mode=ro"},
		{"/synthetic/with space/state_5.sqlite", "file:///synthetic/with%20space/state_5.sqlite?mode=ro"},
	} {
		got, err := deleteDatabaseURI(tc.path)
		if err != nil || got != tc.want {
			t.Fatalf("URI = %q, %v; want %q", got, err, tc.want)
		}
	}
	if _, err := deleteDatabaseURI(`\\server\share\state_5.sqlite`); err == nil {
		t.Fatal("UNC root accepted")
	}
}

func TestDeleteDatabaseOwnershipMapPreservesNeighbors(t *testing.T) {
	for name, owners := range deleteDBOwners {
		if name == "agent_message_board_1.sqlite" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			db := newDeleteSQLDB(t, root, name, ``)
			for table, columns := range owners {
				if columns == nil {
					continue
				}
				definitions := make([]string, len(columns))
				for i, column := range columns {
					definitions[i] = column + " TEXT"
				}
				if table == "threads" {
					definitions = append(definitions, "rollout_path TEXT")
				}
				if table == "jobs" {
					definitions = append(definitions, "kind TEXT", "status TEXT", "worker_id TEXT", "ownership_token TEXT", "started_at INTEGER", "finished_at INTEGER", "lease_until INTEGER", "retry_at INTEGER", "retry_remaining INTEGER NOT NULL DEFAULT 3", "last_error TEXT", "input_watermark INTEGER", "last_success_watermark INTEGER")
				}
				if table == "stage1_outputs" {
					definitions = append(definitions, "selected_for_phase2 INTEGER NOT NULL DEFAULT 0")
				}
				if _, err := db.Exec(`CREATE TABLE ` + table + ` (` + strings.Join(definitions, ",") + `)`); err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{sqlDeleteRootID, sqlDeleteChildID, sqlDeleteNeighborID} {
					values := make([]any, len(columns))
					placeholders := make([]string, len(columns))
					for i := range columns {
						values[i] = id
						placeholders[i] = "?"
					}
					insertColumns := append([]string(nil), columns...)
					if table == "jobs" {
						insertColumns = append(insertColumns, "kind")
						values = append(values, "memory_stage1")
						placeholders = append(placeholders, "?")
					}
					if _, err := db.Exec(`INSERT INTO `+table+` (`+strings.Join(insertColumns, ",")+`) VALUES (`+strings.Join(placeholders, ",")+`)`, values...); err != nil {
						t.Fatal(err)
					}
				}
				if table == "jobs" {
					if _, err := db.Exec(`INSERT INTO jobs(job_key,kind) VALUES (?,'unrelated')`, sqlDeleteRootID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := deleteDatabaseFiles(context.Background(), root, map[string]bool{sqlDeleteRootID: true, sqlDeleteChildID: true}, nil, false, true); err != nil {
				t.Fatal(err)
			}
			for table, columns := range owners {
				if columns == nil {
					continue
				}
				var count int
				if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+columns[0]+`=?`, sqlDeleteNeighborID).Scan(&count); err != nil || count != 1 {
					t.Fatalf("neighbor changed in %s: %d %v", table, count, err)
				}
				query := `SELECT count(*) FROM ` + table + ` WHERE ` + columns[0] + ` IN (?,?)`
				if table == "jobs" {
					query += ` AND kind='memory_stage1'`
				}
				if err := db.QueryRow(query, sqlDeleteRootID, sqlDeleteChildID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("owned rows remain in %s: %d %v", table, count, err)
				}
			}
			if _, ok := owners["jobs"]; ok {
				var count int
				if err := db.QueryRow(`SELECT count(*) FROM jobs WHERE kind='unrelated' AND job_key=?`, sqlDeleteRootID).Scan(&count); err != nil || count != 1 {
					t.Fatal("non-session job removed")
				}
			}
		})
	}
}

func TestDeleteSQLBoardAndMigrationOwnership(t *testing.T) {
	root := t.TempDir()
	board := newDeleteSQLDB(t, root, "agent_message_board_1.sqlite", `CREATE TABLE deleted_boards(board TEXT PRIMARY KEY); CREATE TABLE channels(board TEXT); CREATE TABLE posts(board TEXT); CREATE TABLE subscriptions(board TEXT); CREATE TABLE subscription_opt_outs(board TEXT);`)
	for _, table := range []string{"channels", "posts", "subscriptions", "subscription_opt_outs"} {
		for _, id := range []string{sqlDeleteRootID, sqlDeleteChildID, sqlDeleteNeighborID} {
			if _, err := board.Exec(`INSERT INTO `+table+` VALUES (?)`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	state := newDeleteSQLDB(t, root, "state_5.sqlite", `CREATE TABLE rollout_migration_state(migration_id TEXT PRIMARY KEY,last_checked_thread_created_at INTEGER,last_checked_thread_id TEXT,updated_at INTEGER);`)
	if _, err := state.Exec(`INSERT INTO rollout_migration_state VALUES ('synthetic',1,?,1)`, sqlDeleteRootID); err != nil {
		t.Fatal(err)
	}
	if err := deleteDatabaseFiles(t.Context(), root, map[string]bool{sqlDeleteRootID: true, sqlDeleteChildID: true}, nil, false, true); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"channels", "posts", "subscriptions", "subscription_opt_outs"} {
		var count int
		if err := board.QueryRow(`SELECT count(*) FROM `+table+` WHERE board=?`, sqlDeleteNeighborID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("neighbor %s: %d %v", table, count, err)
		}
		if err := board.QueryRow(`SELECT count(*) FROM `+table+` WHERE board IN (?,?)`, sqlDeleteRootID, sqlDeleteChildID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("owned %s: %d %v", table, count, err)
		}
	}
	var count int
	if err := board.QueryRow(`SELECT count(*) FROM deleted_boards WHERE board IN (?,?)`, sqlDeleteRootID, sqlDeleteChildID).Scan(&count); err != nil || count != 2 {
		t.Fatal("exact tombstones missing")
	}
	if err := state.QueryRow(`SELECT count(*) FROM rollout_migration_state WHERE last_checked_thread_id=?`, sqlDeleteRootID).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration watermark removed")
	}
}

func TestDeleteSQLRefusesUnknownLayoutsAndOwnership(t *testing.T) {
	for _, schema := range []string{
		`CREATE TABLE threads(id TEXT); CREATE TABLE thread_dynamic_tools(other TEXT);`,
		`CREATE TABLE _sqlx_migrations(thread_id TEXT);`,
		`CREATE TABLE unknown(thread_id TEXT);`,
		`CREATE VIEW injected AS SELECT 1;`,
		`CREATE TABLE threads(id TEXT); CREATE TRIGGER injected AFTER DELETE ON threads BEGIN SELECT 1; END;`,
	} {
		t.Run(schema, func(t *testing.T) {
			root := t.TempDir()
			db := newDeleteSQLDB(t, root, "state_5.sqlite", schema)
			_ = db
			if err := deleteDatabaseFiles(t.Context(), root, map[string]bool{sqlDeleteRootID: true}, nil, false, false); err == nil {
				t.Fatal("unsupported layout accepted")
			}
		})
	}
	root := t.TempDir()
	db := newDeleteSQLDB(t, root, "state_5.sqlite", `CREATE TABLE threads(id TEXT,rollout_path TEXT);`)
	if _, err := db.Exec(`INSERT INTO threads VALUES (?,?)`, sqlDeleteRootID, filepath.Join(root, "outside.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := deleteDatabaseFiles(t.Context(), root, map[string]bool{sqlDeleteRootID: true}, nil, false, true); err == nil {
		t.Fatal("unproved path accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM threads`).Scan(&count); err != nil || count != 1 {
		t.Fatal("refusal mutated owned row")
	}
}

func TestDeleteSQLRollbackAndCancellation(t *testing.T) {
	root := t.TempDir()
	db := newDeleteSQLDB(t, root, "state_5.sqlite", `CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT); CREATE TABLE thread_attachments(thread_id TEXT REFERENCES threads(id) ON DELETE RESTRICT);`)
	if _, err := db.Exec(`INSERT INTO threads VALUES (?,NULL); INSERT INTO thread_attachments VALUES (?)`, sqlDeleteRootID, sqlDeleteRootID); err != nil {
		t.Fatal(err)
	}
	// This unrelated child references an owned parent and must stop deletion, including earlier owned-child deletes.
	if _, err := db.Exec(`INSERT INTO thread_attachments VALUES (?)`, sqlDeleteNeighborID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE thread_artifacts(thread_id TEXT, parent TEXT REFERENCES threads(id) ON DELETE RESTRICT); INSERT INTO thread_artifacts VALUES (?,?)`, sqlDeleteNeighborID, sqlDeleteRootID); err != nil {
		t.Fatal(err)
	}
	if err := deleteDatabaseFiles(t.Context(), root, map[string]bool{sqlDeleteRootID: true}, nil, false, true); err == nil {
		t.Fatal("constraint failure accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM thread_attachments WHERE thread_id=?`, sqlDeleteRootID).Scan(&count); err != nil || count != 1 {
		t.Fatal("transaction failure did not roll back")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := deleteDatabaseFiles(ctx, root, map[string]bool{sqlDeleteRootID: true}, nil, false, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestDeleteSQLSidecarPathGuard(t *testing.T) {
	root := t.TempDir()
	newDeleteSQLDB(t, root, "logs_2.sqlite", `CREATE TABLE logs(thread_id TEXT);`)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("synthetic neighbor"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "logs_2.sqlite-wal")); err != nil {
		t.Skip(err)
	}
	if err := deleteDatabaseFiles(t.Context(), root, map[string]bool{sqlDeleteRootID: true}, nil, false, false); err == nil {
		t.Fatal("symlink sidecar accepted")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "synthetic neighbor" {
		t.Fatal("outside file changed")
	}
}

const deleteSQLMemorySchema = `
CREATE TABLE stage1_outputs(thread_id TEXT PRIMARY KEY,source_updated_at INTEGER NOT NULL,raw_memory TEXT NOT NULL,rollout_summary TEXT NOT NULL,rollout_slug TEXT,generated_at INTEGER NOT NULL,usage_count INTEGER,last_usage INTEGER,selected_for_phase2 INTEGER NOT NULL DEFAULT 0,selected_for_phase2_source_updated_at INTEGER);
CREATE TABLE jobs(kind TEXT NOT NULL,job_key TEXT NOT NULL,status TEXT NOT NULL,worker_id TEXT,ownership_token TEXT,started_at INTEGER,finished_at INTEGER,lease_until INTEGER,retry_at INTEGER,retry_remaining INTEGER NOT NULL,last_error TEXT,input_watermark INTEGER,last_success_watermark INTEGER,PRIMARY KEY(kind,job_key));`

func TestDeleteSQLMemoryInvalidation(t *testing.T) {
	for _, version := range []string{"memories_1.sqlite", "memories_v2_1.sqlite"} {
		for _, status := range []string{"missing", "succeeded", "running"} {
			for _, selected := range []int{0, 1} {
				t.Run(version+"/"+status+"/"+string(rune('0'+selected)), func(t *testing.T) {
					root := t.TempDir()
					db := newDeleteSQLDB(t, root, version, deleteSQLMemorySchema)
					for _, row := range []struct {
						id       string
						selected int
					}{{sqlDeleteRootID, selected}, {sqlDeleteChildID, 0}, {sqlDeleteNeighborID, 1}} {
						if _, err := db.Exec(`INSERT INTO stage1_outputs(thread_id,source_updated_at,raw_memory,rollout_summary,generated_at,selected_for_phase2) VALUES (?,1,'synthetic memory','synthetic summary',1,?)`, row.id, row.selected); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := db.Exec(`INSERT INTO jobs(kind,job_key,status,retry_remaining) VALUES ('memory_stage1',?,'succeeded',3),('unrelated',?,'succeeded',3),('memory_stage1',?,'succeeded',3)`, sqlDeleteRootID, sqlDeleteRootID, sqlDeleteNeighborID); err != nil {
						t.Fatal(err)
					}
					if status != "missing" {
						if _, err := db.Exec(`INSERT INTO jobs VALUES ('memory_consolidate_global','global',?,'worker','token',10,20,30,40,1,'synthetic',200,199)`, status); err != nil {
							t.Fatal(err)
						}
					}
					if err := cleanupDeleteDB(t.Context(), db, deleteDBOwners[version], map[string]bool{sqlDeleteRootID: true, sqlDeleteChildID: true}, nil, false, 100); err != nil {
						t.Fatal(err)
					}
					var count int
					if err := db.QueryRow(`SELECT count(*) FROM stage1_outputs WHERE thread_id IN (?,?)`, sqlDeleteRootID, sqlDeleteChildID).Scan(&count); err != nil || count != 0 {
						t.Fatal("owned outputs remain")
					}
					if err := db.QueryRow(`SELECT count(*) FROM stage1_outputs WHERE thread_id=? AND selected_for_phase2=1`, sqlDeleteNeighborID).Scan(&count); err != nil || count != 1 {
						t.Fatal("selected neighbor changed")
					}
					if err := db.QueryRow(`SELECT count(*) FROM jobs WHERE (kind='unrelated' AND job_key=?) OR (kind='memory_stage1' AND job_key=?)`, sqlDeleteRootID, sqlDeleteNeighborID).Scan(&count); err != nil || count != 2 {
						t.Fatal("unrelated jobs changed")
					}
					if err := db.QueryRow(`SELECT count(*) FROM jobs WHERE kind='memory_stage1' AND job_key=?`, sqlDeleteRootID).Scan(&count); err != nil || count != 0 {
						t.Fatal("owned stage1 job remains")
					}
					var gotStatus string
					var watermark, retries, lastSuccess int64
					var retryAt sql.NullInt64
					var worker, token sql.NullString
					err := db.QueryRow(`SELECT status,input_watermark,retry_remaining,retry_at,worker_id,ownership_token,last_success_watermark FROM jobs WHERE kind='memory_consolidate_global' AND job_key='global'`).Scan(&gotStatus, &watermark, &retries, &retryAt, &worker, &token, &lastSuccess)
					if selected == 0 && status == "missing" {
						if !errors.Is(err, sql.ErrNoRows) {
							t.Fatal("nonselected output enqueued job")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					wantStatus := status
					wantWatermark := int64(200)
					wantRetry := int64(1)
					if selected != 0 {
						wantStatus = "pending"
						wantWatermark = 201
						wantRetry = 3
						if status == "missing" {
							wantWatermark = 100
						}
						if status == "running" {
							wantStatus = "running"
						}
					}
					if gotStatus != wantStatus || watermark != wantWatermark || retries != wantRetry {
						t.Fatalf("global job: %s %d %d", gotStatus, watermark, retries)
					}
					if status != "missing" {
						if !worker.Valid || worker.String != "worker" || !token.Valid || token.String != "token" || lastSuccess != 199 {
							t.Fatal("existing job ownership/baseline changed")
						}
						if selected == 0 || status == "running" {
							if !retryAt.Valid || retryAt.Int64 != 40 {
								t.Fatal("retry timing changed")
							}
						} else if retryAt.Valid {
							t.Fatal("pending retry timing not reset")
						}
					} else if worker.Valid || token.Valid || retryAt.Valid || lastSuccess != 0 {
						t.Fatal("new job has stale ownership")
					}
				})
			}
		}
	}
}

func TestDeleteSQLMemoryWatermarkAndRollbackBoundaries(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "monotonic union", true: "enqueue rollback"}[fail], func(t *testing.T) {
			root := t.TempDir()
			schema := deleteSQLMemorySchema
			if fail {
				schema = strings.Replace(schema, "status TEXT NOT NULL,", "status TEXT NOT NULL CHECK(status='succeeded'),", 1)
			}
			db := newDeleteSQLDB(t, root, "memories_1.sqlite", schema)
			for _, id := range []string{sqlDeleteRootID, sqlDeleteChildID, sqlDeleteNeighborID} {
				if _, err := db.Exec(`INSERT INTO stage1_outputs(thread_id,source_updated_at,raw_memory,rollout_summary,generated_at,selected_for_phase2) VALUES (?,1,'synthetic','synthetic',1,1)`, id); err != nil {
					t.Fatal(err)
				}
			}
			status := "pending"
			if fail {
				status = "succeeded"
			}
			if _, err := db.Exec(`INSERT INTO jobs(kind,job_key,status,retry_remaining,input_watermark,last_success_watermark) VALUES ('memory_consolidate_global','global',?,5,50,49)`, status); err != nil {
				t.Fatal(err)
			}
			err := cleanupDeleteDB(t.Context(), db, deleteDBOwners["memories_1.sqlite"], map[string]bool{sqlDeleteRootID: true, sqlDeleteChildID: true}, nil, false, 100)
			var outputs int
			var watermark, retries int64
			if queryErr := db.QueryRow(`SELECT count(*) FROM stage1_outputs`).Scan(&outputs); queryErr != nil {
				t.Fatal(queryErr)
			}
			if queryErr := db.QueryRow(`SELECT input_watermark,retry_remaining FROM jobs WHERE kind='memory_consolidate_global' AND job_key='global'`).Scan(&watermark, &retries); queryErr != nil {
				t.Fatal(queryErr)
			}
			if fail {
				if err == nil || outputs != 3 || watermark != 50 || retries != 5 {
					t.Fatalf("failed enqueue committed: %v %d %d %d", err, outputs, watermark, retries)
				}
			} else if err != nil || outputs != 1 || watermark != 101 || retries != 5 {
				t.Fatalf("selection union: %v %d %d %d", err, outputs, watermark, retries)
			}
		})
	}
}

func TestDeleteSQLMemoryRefusesIncompleteSchema(t *testing.T) {
	for _, schema := range []string{
		`CREATE TABLE stage1_outputs(thread_id TEXT,selected_for_phase2 INTEGER);`,
		`CREATE TABLE stage1_outputs(thread_id TEXT,selected_for_phase2 INTEGER);CREATE TABLE jobs(kind TEXT,job_key TEXT);`,
		`CREATE TABLE stage1_outputs(thread_id TEXT);CREATE TABLE jobs(kind TEXT,job_key TEXT);`,
	} {
		root := t.TempDir()
		newDeleteSQLDB(t, root, "memories_1.sqlite", schema)
		if err := deleteDatabaseFiles(t.Context(), root, map[string]bool{sqlDeleteRootID: true}, nil, false, false); err == nil {
			t.Fatal("incomplete memory ownership schema admitted")
		}
	}
}
