-- migrations/0001_threads.sql
CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    rollout_path TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    source TEXT NOT NULL,
    model_provider TEXT NOT NULL,
    cwd TEXT NOT NULL,
    title TEXT NOT NULL,
    sandbox_policy TEXT NOT NULL,
    approval_mode TEXT NOT NULL,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    has_user_event INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0,
    archived_at INTEGER,
    git_sha TEXT,
    git_branch TEXT,
    git_origin_url TEXT
);

CREATE INDEX idx_threads_created_at ON threads(created_at DESC, id DESC);
CREATE INDEX idx_threads_updated_at ON threads(updated_at DESC, id DESC);
CREATE INDEX idx_threads_archived ON threads(archived);
CREATE INDEX idx_threads_source ON threads(source);
CREATE INDEX idx_threads_provider ON threads(model_provider);

-- migrations/0002_logs.sql
CREATE TABLE logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts INTEGER NOT NULL,
    ts_nanos INTEGER NOT NULL,
    level TEXT NOT NULL,
    target TEXT NOT NULL,
    message TEXT,
    module_path TEXT,
    file TEXT,
    line INTEGER
);

CREATE INDEX idx_logs_ts ON logs(ts DESC, ts_nanos DESC, id DESC);

-- migrations/0003_logs_thread_id.sql
ALTER TABLE logs ADD COLUMN thread_id TEXT;

CREATE INDEX idx_logs_thread_id ON logs(thread_id);

-- migrations/0004_thread_dynamic_tools.sql
CREATE TABLE thread_dynamic_tools (
    thread_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    input_schema TEXT NOT NULL,
    PRIMARY KEY(thread_id, position),
    FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE CASCADE
);

CREATE INDEX idx_thread_dynamic_tools_thread ON thread_dynamic_tools(thread_id);

-- migrations/0005_threads_cli_version.sql
ALTER TABLE threads ADD COLUMN cli_version TEXT NOT NULL DEFAULT '';

-- migrations/0006_memories.sql
CREATE TABLE stage1_outputs (
    thread_id TEXT PRIMARY KEY,
    source_updated_at INTEGER NOT NULL,
    raw_memory TEXT NOT NULL,
    rollout_summary TEXT NOT NULL,
    generated_at INTEGER NOT NULL,
    FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE CASCADE
);

CREATE INDEX idx_stage1_outputs_source_updated_at
    ON stage1_outputs(source_updated_at DESC, thread_id DESC);

CREATE TABLE jobs (
    kind TEXT NOT NULL,
    job_key TEXT NOT NULL,
    status TEXT NOT NULL,
    worker_id TEXT,
    ownership_token TEXT,
    started_at INTEGER,
    finished_at INTEGER,
    lease_until INTEGER,
    retry_at INTEGER,
    retry_remaining INTEGER NOT NULL,
    last_error TEXT,
    input_watermark INTEGER,
    last_success_watermark INTEGER,
    PRIMARY KEY (kind, job_key)
);

CREATE INDEX idx_jobs_kind_status_retry_lease
    ON jobs(kind, status, retry_at, lease_until);

-- migrations/0007_threads_first_user_message.sql
ALTER TABLE threads ADD COLUMN first_user_message TEXT NOT NULL DEFAULT '';

UPDATE threads
SET first_user_message = title
WHERE first_user_message = '' AND has_user_event = 1 AND title <> '';

-- migrations/0008_backfill_state.sql
CREATE TABLE backfill_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    status TEXT NOT NULL,
    last_watermark TEXT,
    last_success_at INTEGER,
    updated_at INTEGER NOT NULL
);

INSERT INTO backfill_state (id, status, last_watermark, last_success_at, updated_at)
VALUES (
    1,
    'pending',
    NULL,
    NULL,
    CAST(strftime('%s', 'now') AS INTEGER)
)
ON CONFLICT(id) DO NOTHING;

-- migrations/0009_stage1_outputs_rollout_slug.sql
ALTER TABLE stage1_outputs
ADD COLUMN rollout_slug TEXT;

-- migrations/0010_logs_process_id.sql
ALTER TABLE logs ADD COLUMN process_uuid TEXT;

CREATE INDEX idx_logs_process_uuid ON logs(process_uuid);

-- migrations/0011_logs_partition_prune_indexes.sql
CREATE INDEX idx_logs_thread_id_ts ON logs(thread_id, ts DESC, ts_nanos DESC, id DESC);

CREATE INDEX idx_logs_process_uuid_threadless_ts ON logs(process_uuid, ts DESC, ts_nanos DESC, id DESC)
WHERE thread_id IS NULL;

-- migrations/0012_logs_estimated_bytes.sql
ALTER TABLE logs ADD COLUMN estimated_bytes INTEGER NOT NULL DEFAULT 0;

UPDATE logs
SET estimated_bytes =
    LENGTH(CAST(COALESCE(message, '') AS BLOB))
    + LENGTH(CAST(level AS BLOB))
    + LENGTH(CAST(target AS BLOB))
    + LENGTH(CAST(COALESCE(module_path, '') AS BLOB))
    + LENGTH(CAST(COALESCE(file, '') AS BLOB));

-- migrations/0013_threads_agent_nickname.sql
ALTER TABLE threads ADD COLUMN agent_nickname TEXT;
ALTER TABLE threads ADD COLUMN agent_role TEXT;

-- migrations/0014_agent_jobs.sql
CREATE TABLE agent_jobs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    instruction TEXT NOT NULL,
    output_schema_json TEXT,
    input_headers_json TEXT NOT NULL,
    input_csv_path TEXT NOT NULL,
    output_csv_path TEXT NOT NULL,
    auto_export INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    last_error TEXT
);

CREATE TABLE agent_job_items (
    job_id TEXT NOT NULL,
    item_id TEXT NOT NULL,
    row_index INTEGER NOT NULL,
    source_id TEXT,
    row_json TEXT NOT NULL,
    status TEXT NOT NULL,
    assigned_thread_id TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    result_json TEXT,
    last_error TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    completed_at INTEGER,
    reported_at INTEGER,
    PRIMARY KEY (job_id, item_id),
    FOREIGN KEY(job_id) REFERENCES agent_jobs(id) ON DELETE CASCADE
);

CREATE INDEX idx_agent_jobs_status ON agent_jobs(status, updated_at DESC);
CREATE INDEX idx_agent_job_items_status ON agent_job_items(job_id, status, row_index ASC);

-- migrations/0015_agent_jobs_max_runtime_seconds.sql
ALTER TABLE agent_jobs
ADD COLUMN max_runtime_seconds INTEGER;

-- migrations/0016_memory_usage.sql
ALTER TABLE stage1_outputs ADD COLUMN usage_count INTEGER;
ALTER TABLE stage1_outputs ADD COLUMN last_usage INTEGER;

-- migrations/0017_phase2_selection_flag.sql
ALTER TABLE stage1_outputs
ADD COLUMN selected_for_phase2 INTEGER NOT NULL DEFAULT 0;

-- migrations/0018_phase2_selection_snapshot.sql
ALTER TABLE stage1_outputs
ADD COLUMN selected_for_phase2_source_updated_at INTEGER;
ALTER TABLE threads ADD COLUMN memory_mode TEXT NOT NULL DEFAULT 'enabled';

-- migrations/0019_thread_dynamic_tools_defer_loading.sql
ALTER TABLE thread_dynamic_tools
ADD COLUMN defer_loading INTEGER NOT NULL DEFAULT 0;

-- migrations/0020_threads_model_reasoning_effort.sql
ALTER TABLE threads ADD COLUMN model TEXT;
ALTER TABLE threads ADD COLUMN reasoning_effort TEXT;

-- migrations/0021_thread_spawn_edges.sql
CREATE TABLE thread_spawn_edges (
    parent_thread_id TEXT NOT NULL,
    child_thread_id TEXT NOT NULL PRIMARY KEY,
    status TEXT NOT NULL
);

CREATE INDEX idx_thread_spawn_edges_parent_status
    ON thread_spawn_edges(parent_thread_id, status);

-- migrations/0022_threads_agent_path.sql
ALTER TABLE threads ADD COLUMN agent_path TEXT;

-- migrations/0023_drop_logs.sql
PRAGMA auto_vacuum = INCREMENTAL;

DROP TABLE IF EXISTS logs;

-- migrations/0024_remote_control_enrollments.sql
CREATE TABLE remote_control_enrollments (
    websocket_url TEXT NOT NULL,
    account_id TEXT NOT NULL,
    app_server_client_name TEXT NOT NULL,
    server_id TEXT NOT NULL,
    environment_id TEXT NOT NULL,
    server_name TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (websocket_url, account_id, app_server_client_name)
);

-- migrations/0025_thread_timestamps_millis.sql
ALTER TABLE threads ADD COLUMN created_at_ms INTEGER;
ALTER TABLE threads ADD COLUMN updated_at_ms INTEGER;

CREATE TEMP TABLE thread_timestamp_migration AS
SELECT
    id,
    CASE
        WHEN created_at < 1577836800000 THEN created_at * 1000
        ELSE created_at
    END AS created_at_base_ms,
    CASE
        WHEN updated_at < 1577836800000 THEN updated_at * 1000
        ELSE updated_at
    END AS updated_at_base_ms
FROM threads;

WITH RECURSIVE
ordered_created AS (
    SELECT
        id,
        created_at_base_ms,
        ROW_NUMBER() OVER (ORDER BY created_at_base_ms, id) AS row_number
    FROM thread_timestamp_migration
),
assigned_created(row_number, id, created_at_ms) AS (
    SELECT row_number, id, created_at_base_ms
    FROM ordered_created
    WHERE row_number = 1
    UNION ALL
    SELECT
        ordered_created.row_number,
        ordered_created.id,
        MAX(ordered_created.created_at_base_ms, assigned_created.created_at_ms + 1)
    FROM ordered_created
    JOIN assigned_created ON ordered_created.row_number = assigned_created.row_number + 1
)
UPDATE threads
SET created_at_ms = (
    SELECT created_at_ms
    FROM assigned_created
    WHERE assigned_created.id = threads.id
);

WITH RECURSIVE
ordered_updated AS (
    SELECT
        id,
        updated_at_base_ms,
        ROW_NUMBER() OVER (ORDER BY updated_at_base_ms, id) AS row_number
    FROM thread_timestamp_migration
),
assigned_updated(row_number, id, updated_at_ms) AS (
    SELECT row_number, id, updated_at_base_ms
    FROM ordered_updated
    WHERE row_number = 1
    UNION ALL
    SELECT
        ordered_updated.row_number,
        ordered_updated.id,
        MAX(ordered_updated.updated_at_base_ms, assigned_updated.updated_at_ms + 1)
    FROM ordered_updated
    JOIN assigned_updated ON ordered_updated.row_number = assigned_updated.row_number + 1
)
UPDATE threads
SET updated_at_ms = (
    SELECT updated_at_ms
    FROM assigned_updated
    WHERE assigned_updated.id = threads.id
);

DROP TABLE thread_timestamp_migration;

CREATE TRIGGER threads_created_at_ms_after_insert
AFTER INSERT ON threads
WHEN NEW.created_at_ms IS NULL
BEGIN
    UPDATE threads
    SET created_at_ms = NEW.created_at * 1000
    WHERE id = NEW.id;
END;

CREATE TRIGGER threads_updated_at_ms_after_insert
AFTER INSERT ON threads
WHEN NEW.updated_at_ms IS NULL
BEGIN
    UPDATE threads
    SET updated_at_ms = NEW.updated_at * 1000
    WHERE id = NEW.id;
END;

CREATE TRIGGER threads_created_at_ms_after_update
AFTER UPDATE OF created_at ON threads
WHEN NEW.created_at != OLD.created_at
 AND NEW.created_at_ms IS OLD.created_at_ms
BEGIN
    UPDATE threads
    SET created_at_ms = NEW.created_at * 1000
    WHERE id = NEW.id;
END;

CREATE TRIGGER threads_updated_at_ms_after_update
AFTER UPDATE OF updated_at ON threads
WHEN NEW.updated_at != OLD.updated_at
 AND NEW.updated_at_ms IS OLD.updated_at_ms
BEGIN
    UPDATE threads
    SET updated_at_ms = NEW.updated_at * 1000
    WHERE id = NEW.id;
END;

CREATE INDEX idx_threads_created_at_ms ON threads(created_at_ms DESC, id DESC);
CREATE INDEX idx_threads_updated_at_ms ON threads(updated_at_ms DESC, id DESC);

-- migrations/0026_thread_dynamic_tools_namespace.sql
ALTER TABLE thread_dynamic_tools
ADD COLUMN namespace TEXT;

-- migrations/0027_threads_cwd_sort_indexes.sql
CREATE INDEX idx_threads_archived_cwd_created_at_ms ON threads(archived, cwd, created_at_ms DESC, id DESC);
CREATE INDEX idx_threads_archived_cwd_updated_at_ms ON threads(archived, cwd, updated_at_ms DESC, id DESC);

-- migrations/0028_device_key_bindings.sql
CREATE TABLE device_key_bindings (
    key_id TEXT PRIMARY KEY NOT NULL,
    account_user_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- migrations/0029_thread_goals.sql
CREATE TABLE thread_goals (
    thread_id TEXT PRIMARY KEY NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    goal_id TEXT NOT NULL,
    objective TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('active', 'paused', 'budget_limited', 'complete')),
    token_budget INTEGER,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    time_used_seconds INTEGER NOT NULL DEFAULT 0,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

-- migrations/0030_threads_thread_source.sql
ALTER TABLE threads ADD COLUMN thread_source TEXT;

-- migrations/0031_drop_device_key_bindings.sql
DROP TABLE IF EXISTS device_key_bindings;

-- migrations/0032_threads_preview.sql
ALTER TABLE threads ADD COLUMN preview TEXT NOT NULL DEFAULT '';

UPDATE threads
SET preview = first_user_message
WHERE preview = '' AND first_user_message <> '';

UPDATE threads
SET preview = (
    SELECT thread_goals.objective
    FROM thread_goals
    WHERE thread_goals.thread_id = threads.id
)
WHERE preview = ''
    AND EXISTS (
        SELECT 1
        FROM thread_goals
        WHERE thread_goals.thread_id = threads.id
            AND thread_goals.objective <> ''
    );

-- migrations/0033_thread_goal_stopped_statuses.sql
PRAGMA foreign_keys=OFF;

CREATE TABLE thread_goals_new (
    thread_id TEXT PRIMARY KEY NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    goal_id TEXT NOT NULL,
    objective TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN (
        'active',
        'paused',
        'blocked',
        'usage_limited',
        'budget_limited',
        'complete'
    )),
    token_budget INTEGER,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    time_used_seconds INTEGER NOT NULL DEFAULT 0,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

INSERT INTO thread_goals_new (
    thread_id,
    goal_id,
    objective,
    status,
    token_budget,
    tokens_used,
    time_used_seconds,
    created_at_ms,
    updated_at_ms
)
SELECT
    thread_id,
    goal_id,
    objective,
    status,
    token_budget,
    tokens_used,
    time_used_seconds,
    created_at_ms,
    updated_at_ms
FROM thread_goals;

DROP TABLE thread_goals;
ALTER TABLE thread_goals_new RENAME TO thread_goals;

PRAGMA foreign_keys=ON;

-- migrations/0034_drop_thread_goals.sql
DROP TABLE IF EXISTS thread_goals;

-- migrations/0035_drop_memory_tables.sql
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS stage1_outputs;

-- migrations/0036_threads_visible_sort_indexes.sql
CREATE INDEX idx_threads_visible_created_at_ms
    ON threads(archived, created_at_ms DESC)
    WHERE preview <> '';

CREATE INDEX idx_threads_visible_updated_at_ms
    ON threads(archived, updated_at_ms DESC)
    WHERE preview <> '';

-- migrations/0037_remote_control_enrollments_enabled.sql
ALTER TABLE remote_control_enrollments
ADD COLUMN remote_control_enabled INTEGER;

-- migrations/0038_external_agent_config_imports.sql
CREATE TABLE external_agent_config_imports (
    import_id TEXT PRIMARY KEY,
    completed_at_ms INTEGER NOT NULL,
    successes TEXT NOT NULL,
    failures TEXT NOT NULL
);

-- migrations/0039_threads_recency_at.sql
ALTER TABLE threads ADD COLUMN recency_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE threads ADD COLUMN recency_at_ms INTEGER NOT NULL DEFAULT 0;

UPDATE threads
SET recency_at = updated_at,
    recency_at_ms = updated_at_ms;

-- Older binaries can open databases migrated by newer binaries. Seed recency
-- when one of those binaries inserts a thread without the new columns.
CREATE TRIGGER threads_recency_at_after_insert
AFTER INSERT ON threads
WHEN NEW.recency_at_ms = 0
BEGIN
    UPDATE threads
    SET recency_at = NEW.updated_at,
        recency_at_ms = COALESCE(NEW.updated_at_ms, NEW.updated_at * 1000)
    WHERE id = NEW.id;
END;

CREATE INDEX idx_threads_recency_at_ms
    ON threads(recency_at_ms DESC, id DESC);

CREATE INDEX idx_threads_archived_cwd_recency_at_ms
    ON threads(archived, cwd, recency_at_ms DESC, id DESC);

CREATE INDEX idx_threads_visible_recency_at_ms
    ON threads(archived, recency_at_ms DESC, id DESC)
    WHERE preview <> '';

-- migrations/0040_threads_history_mode.sql
ALTER TABLE threads ADD COLUMN history_mode TEXT NOT NULL DEFAULT 'legacy';

-- migrations/0041_threads_name.sql
ALTER TABLE threads ADD COLUMN name TEXT;

-- migrations/0042_drop_agent_jobs.sql
DROP TABLE IF EXISTS agent_job_items;
DROP TABLE IF EXISTS agent_jobs;

-- migrations/0043_threads_is_pinned.sql
ALTER TABLE threads ADD COLUMN is_pinned INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_threads_pinned_recency_at_ms
    ON threads(archived, recency_at_ms DESC, id DESC)
    WHERE is_pinned = 1 AND preview <> '';

-- migrations/0044_external_agent_config_imports_provider_id.sql
ALTER TABLE external_agent_config_imports
ADD COLUMN provider_id TEXT;

-- migrations/0045_threads_section.sql
CREATE TABLE thread_sections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

INSERT INTO thread_sections (id, name)
VALUES ('01984de2-8f74-7c91-a3b2-5c5e937cf318', 'Pinned');

ALTER TABLE threads ADD COLUMN thread_section_id TEXT
    REFERENCES thread_sections(id) ON DELETE SET NULL;

CREATE INDEX idx_threads_section_recency_at_ms
    ON threads(archived, thread_section_id, recency_at_ms DESC, id DESC)
    WHERE thread_section_id IS NOT NULL AND preview <> '';

-- migrations/0046_threads_section_order.sql
ALTER TABLE threads ADD COLUMN section_position INTEGER;
ALTER TABLE threads ADD COLUMN section_entered_at_ms INTEGER;

UPDATE threads
SET section_position = ranked.position,
    section_entered_at_ms = threads.recency_at_ms
FROM (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY thread_section_id
               ORDER BY recency_at_ms DESC, id DESC
           ) * 1000000 AS position
    FROM threads
    WHERE thread_section_id IS NOT NULL
) AS ranked
WHERE threads.id = ranked.id;

CREATE INDEX idx_threads_section_position
    ON threads(archived, thread_section_id, section_position ASC, id ASC)
    WHERE thread_section_id IS NOT NULL AND preview <> '';

-- migrations/0047_rollout_migration_state.sql
CREATE TABLE rollout_migration_state (
    migration_id TEXT PRIMARY KEY,
    last_checked_thread_created_at INTEGER,
    last_checked_thread_id TEXT,
    updated_at INTEGER NOT NULL
);

CREATE TABLE rollout_migration_skipped_rollouts (
    migration_id TEXT NOT NULL,
    rollout_path TEXT NOT NULL,
    rollout_size_bytes INTEGER NOT NULL,
    rollout_modified_at_ns INTEGER NOT NULL,
    skip_reason TEXT NOT NULL,
    skipped_at INTEGER NOT NULL,
    PRIMARY KEY (migration_id, rollout_path)
);

-- migrations/0048_thread_section_appearance.sql
ALTER TABLE thread_sections ADD COLUMN appearance TEXT;

-- migrations/0049_projects.sql
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    position INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE project_roots (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (project_id, position)
);

CREATE TABLE project_idempotency_keys (
    key TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL
);

ALTER TABLE threads ADD COLUMN project_id TEXT
    REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX idx_projects_position
    ON projects(position ASC, id ASC);
CREATE INDEX idx_threads_project_id
    ON threads(project_id, archived, created_at_ms DESC, id DESC)
    WHERE project_id IS NOT NULL;

-- migrations/0050_threads_section_empty_preview_indexes.sql
DROP INDEX idx_threads_section_recency_at_ms;
DROP INDEX idx_threads_section_position;

CREATE INDEX idx_threads_section_recency_at_ms
    ON threads(archived, thread_section_id, recency_at_ms DESC, id DESC)
    WHERE thread_section_id IS NOT NULL;

CREATE INDEX idx_threads_section_position
    ON threads(archived, thread_section_id, section_position ASC, id ASC)
    WHERE thread_section_id IS NOT NULL;

-- migrations/0051_thread_artifacts.sql
CREATE TABLE thread_artifacts (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    artifact_type TEXT NOT NULL,
    identity_key TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (thread_id, artifact_type, identity_key)
);

CREATE INDEX idx_thread_artifacts_thread_created_id
    ON thread_artifacts(thread_id, created_at, id);

-- migrations/0052_projects_recency.sql
CREATE INDEX idx_threads_project_recency
    ON threads(project_id, recency_at_ms DESC)
    WHERE archived = 0 AND project_id IS NOT NULL;

-- migrations/0053_threads_originator.sql
ALTER TABLE threads ADD COLUMN originator TEXT;

-- migrations/0054_threads_daybreak_enabled.sql
ALTER TABLE threads ADD COLUMN daybreak_enabled BOOLEAN;

-- migrations/0055_thread_attachments.sql
ALTER TABLE thread_artifacts RENAME TO thread_attachments;
ALTER TABLE thread_attachments RENAME COLUMN artifact_type TO attachment_type;

DROP INDEX idx_thread_artifacts_thread_created_id;
CREATE INDEX idx_thread_attachments_thread_created_id
    ON thread_attachments(thread_id, created_at, id);

-- migrations/0056_threads_creator_identity.sql
ALTER TABLE threads ADD COLUMN creator_user_id TEXT;
ALTER TABLE threads ADD COLUMN creator_account_id TEXT;

-- migrations/0057_cleanup_guardian_thread_metadata.sql
-- Guardian prompts are synthetic review context, not user-authored messages.
-- Keep the SQLite projection small; rollout JSONL remains canonical.
-- Legacy automatic titles were empty or copied first_user_message; preserve a
-- different non-empty title because it may have been set explicitly.
UPDATE threads
SET title = CASE
        WHEN trim(title) = '' OR trim(title) = trim(first_user_message)
            THEN 'Guardian review'
        ELSE title
    END,
    name = CASE
        WHEN trim(COALESCE(name, '')) != ''
            THEN name
        WHEN trim(title) != ''
            AND trim(title) != trim(COALESCE(first_user_message, ''))
            THEN title
        ELSE 'Guardian review'
    END,
    preview = 'Approval review',
    first_user_message = ''
WHERE source = '{"subagent":{"other":"guardian"}}';

-- migrations/0058_threads_archive_sort_indexes.sql
-- Archived listings include empty previews and cannot use the visible-only indexes.
CREATE INDEX idx_threads_archive_created_at_ms
    ON threads(archived, created_at_ms DESC, id DESC)
    WHERE archived = 1;

CREATE INDEX idx_threads_archive_updated_at_ms
    ON threads(archived, updated_at_ms DESC, id DESC)
    WHERE archived = 1;

CREATE INDEX idx_threads_archive_recency_at_ms
    ON threads(archived, recency_at_ms DESC, id DESC)
    WHERE archived = 1;
