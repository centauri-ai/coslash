# Claude Code

Code: `collector/internal/vendors/claude/`. This package reads Claude Code CLI, background, Desktop, and Dynamic Workflow transcripts from `~/.claude`.

## Storage

- Root transcripts are `~/.claude/projects/<cwd-slug>/<session-uuid>.jsonl`. The slug is lossy, so `cwd` comes from the rows, not from the directory name.
- Task and Agent subagents are `<cwd-slug>/<root-uuid>/subagents/agent-<id>.jsonl`. Each one has an `agent-<id>.meta.json` beside it with `description`, `agentType`, and `toolUseId`.
- Dynamic Workflow agents are `<root-uuid>/subagents/workflows/<run-id>/agent-<id>.jsonl`. The same run directory also holds `journal.jsonl` and other non-agent JSONL files, which discovery must skip.
- The workflow state file is in a different tree: `<root-uuid>/workflows/<run-id>.json`, not under `subagents/`. Labels and lifecycle come from its `workflowProgress`, keyed by `agentId`.
- Live state is in `~/.claude/sessions/*.json` (each file names a `pid`, which coSlash validates). Background jobs are in `~/.claude/jobs/<id>/state.json`.
- Claude Desktop titles are outside `~/.claude`: `<UserConfigDir>/Claude/claude-code-sessions/<a>/<b>/*.json`, joined by `cliSessionId`. This is `~/Library/Application Support` on macOS and `%APPDATA%` on Windows (`metadata_test.go` `TestLoadMetadataUsesUserConfigDirectory`).
- Claude Desktop SSH workspaces leave a Mac-side mirror at `projects/ssh-<uuid>/<uuid>.jsonl`. The same session also exists on the SSH host.
- On Windows, the same session UUID can occur under two project slugs. coSlash keeps the root with the newest mtime. If the mtimes are equal, it keeps the lexically first path (`TestParseFilesDeduplicatesRootsBySessionID`).

## Format quirks

- Background re-home: Claude Code copies a terminal session to a new file with a new session UUID 30 s to 3 min after start. The copy keeps every row `uuid`, `message.id`, and `timestamp`, and adds `sessionKind: "bg"`. Without a fix, one conversation shows as two cards. Observed: 2.1.233 to 2.1.234, 2026-08-18, c2e572d5.
- `sessionKind: "bg"` alone does not identify a copy. Some sessions are background from birth and share no rows with other files. The real signal is row-uuid containment plus the `bg` tag on the successor. Observed: 2.1.234, 2026-08-18, c2e572d5.
- Containment must use only rows that carry `message`. The predecessor can keep a trailing `system`/`informational` row that the copy never takes, so all-row containment fails. Observed: unknown, 2026-08-28, c2e572d5.
- Equal conversation-row sets must also collapse. This occurs when the copy lands before the next turn, or when its only new turn has no usage. coSlash orders equal sets by total row count. Observed: unknown, 2026-08-28, c2e572d5.
- A re-home supersedes only the closest root that it contains. If it took every contained root, it would remove the ancestor of a deliberate `claude --resume` branch. Observed: unknown, 2026-08-28, c2e572d5.
- The re-home does not copy the `subagents/` directory. Child transcripts stay keyed to the old UUID, so coSlash re-points them to the surviving root. The survivor copied the `tool_use` rows, so spawn keys still match. Observed: unknown, 2026-08-31, a8a7beb7.
- Tradeoff: if a future Claude Code version stops writing the `bg` tag on re-homes, duplicate cards come back. Observed: unknown, 2026-08-28, c2e572d5.
- Fork and resume copies repeat the parent rows verbatim, usage included. Token ownership goes to the upstream file by `message.id` containment, then file birthtime, then id count. An unresolved tie keeps full usage on both sides, so the failure mode is over-count, never under-count (`fork.go`). Observed: unknown, 2026-07-31, 10d4b6ab.
- File birthtime exists only on darwin `Stat_t`. Other platforms fall back to mtime for fork ordering. Observed: n/a, 2026-08-04, 121adb2a.
- Streamed assistant rows repeat the same `message.id` with usage. coSlash counts usage once per id. Rows with model `<synthetic>` carry no billable usage and are skipped. Observed: unknown, 2026-07-31, 10d4b6ab.
- Usage can report cache writes as tiered `cache_creation.ephemeral_1h/5m` or only as an untiered `cache_creation_input_tokens` total. coSlash adds the untiered remainder to the 5 m bucket (`types.go` `untieredCacheCreation`). Observed: unknown, 2026-07-31, 10d4b6ab.
- An interrupted turn has no end marker. Claude Code injects a user row that starts with `[Request interrupted by user`, and coSlash uses it to close the turn. Observed: unknown, 2026-07-31, 10d4b6ab.
- `stop_reason` values `tool_use` and `pause_turn` do not end a turn. Any other assistant `stop_reason` ends it and supplies the recap text. Observed: unknown, 2026-07-31, 10d4b6ab.
- A subagent transcript opens mid-turn. Its task prompt is a meta row, so coSlash starts every child in turn. A forked skill has no other prompt row. Observed: unknown, 2026-08-07, b9107999.
- `gitBranch` is `HEAD` on a detached checkout. coSlash ignores that value and keeps the last real branch. Observed: unknown, 2026-07-31, 10d4b6ab.
- Dynamic Workflow agent `.meta.json` files have only `agentType: "workflow-subagent"` and `spawnDepth`. They have no `description` or `toolUseId`, so names must come from the workflow state file. Observed: 2.1.220, 2026-07-29, 10d4b6ab.
- The workflow state file is written once when the run stops. A run killed mid-flight leaves its agents on `state: "progress"` forever. coSlash reports such agents as aborted when the run has a `durationMs` (`workflow.go`). Observed: unknown, 2026-07-29, 10d4b6ab.
- An approved `ExitPlanMode` result carries the plan in `toolUseResult.plan` and the plan file in `filePath`. That `filePath` is not a code edit. Rejected exits carry no plan. Observed: unknown, 2026-09-23, 6683e2d9.
- Waiting on a tool permission prompt comes from Claude Code itself: it writes `status: "waiting"` to `~/.claude/sessions/*.json`. Only an unanswered `AskUserQuestion` comes from the transcript. Observed: unknown, 2026-08-18, 5f9b7ff0.
- A live session file with `status: "idle"` and a `statusUpdatedAt` older than 1 hour is ignored, even when the pid is alive (`metadata.go` `idleStatusTTL`). The reason is not recorded. Observed: unknown, 2026-07-31, 10d4b6ab.
- A Claude Desktop SSH session appears twice: once from the Mac-side mirror and once from the SSH host. coSlash marks the mirror only when the directory is `ssh-<uuid>` and the file is `<uuid>.jsonl` with a valid UUID. The list hides the mirror only when the matching SSH card exists. Observed: unknown, 2026-09-28, ab523a36.
- The mirror flag must survive root deduplication. If an ordinary root with the same UUID wins the mtime tie-break, it keeps the mirror identity (`TestCollectPreservesDesktopSSHMirrorIdentityAcrossDuplicateRoots`). Observed: unknown, 2026-10-02, 8c77ed5c.

## Resume and launch

- Resume is `claude --resume <uuid>`. Start fresh passes the handoff with `--append-system-prompt-file <path>` and never with `--resume`.
- Claude Code turns transcript saving off when it inherits `CLAUDE_CODE_CHILD_SESSION`. A session launched from an app that started inside Claude Code then never appears in coSlash. Launches remove the marker list in `agentexec/env.go`, and the interactive command also runs `unset CLAUDE_CODE_CHILD_SESSION`. Observed: 2.1.284, 2026-09-28, ab8ead40, 199b4013.
- Remove markers by exact name, never by prefix. `CLAUDE_CODE_USE_FOUNDRY` and credential variables share the `CLAUDE_CODE_` prefix (ab8ead40).
- `CLAUDE_CODE_MESSAGING_TOKEN` is a token for the parent session. This is a second reason to keep it out of launched agents (ab8ead40).
- On SSH hosts, a non-interactive SSH command does not read `.bashrc`. Remote launches prepend `$HOME/.local/bin` to `PATH` and source `~/.agent-keys.sh` when it exists. Observed: unknown, 2026-09-22, 60375cea.
- Synthesis uses `-p --tools "" --safe-mode --no-session-persistence` (`synthesis/runner.go`). `--no-session-persistence` keeps synthesis runs out of `~/.claude/projects`, so they do not show as sessions.
- Review uses `--permission-mode plan --safe-mode --restricted --strict-mcp-config --tools Read,Glob,Grep` (`launch/launch.go`).

## Open questions

- SSH Claude sessions have no full-session records, so the inspector shows only the bounded summary. Only Codex has complete remote records today.
- It is not known if a launched Claude uses an inherited `CLAUDE_CODE_SESSION_ID` for its own tools.
- It is not known why Claude Code leaves idle live session files with a live pid, which is the case the 1-hour TTL handles.
- The compaction seed keeps only sections 1, 7, 8, and 9 of the compact summary (`seed.go`). The source of that numbering is not recorded.
