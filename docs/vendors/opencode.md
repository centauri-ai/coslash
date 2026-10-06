# OpenCode

Code: `collector/internal/vendors/opencode/`. The package reads OpenCode sessions from its shared SQLite database and manages a small coSlash plugin that records permission and client state.

## Storage

- OpenCode keeps all sessions in one shared SQLite database, not one transcript per session. The default path is `~/.local/share/opencode/opencode.db`. coSlash uses the same fallback on Windows. Observed: OpenCode 1.18.4, 2026-08-10, commit 9603e871.
- Path resolution order: `OPENCODE_DB` (absolute, or relative to `<data home>/opencode`), then `XDG_DATA_HOME`, then `opencode db path` with a 2-second timeout, then the default. OpenCode v2 honors `OPENCODE_DB`. Before this order, coSlash missed isolated v2 databases. Observed: OpenCode 2.0.18, 2026-09-28, commit ea925ff1, `TestOpenV2DatabaseOverride`, `TestRootV2RelativeDatabaseOverride`.
- The database is opened with `mode=ro`, `_query_only=1`, and a 1-second busy timeout. It does not use `immutable=1`, because that flag ignores the WAL file and misses recent writes. Observed: 2026-08-11, `collector/internal/vendors/opencode/sqlite.go`.
- A Windows drive path must become `file:///C:/...` in the SQLite URI. Observed: 2026-09-22, `TestReadOnlyDatabaseDSNWindowsURIShapes`.
- Do not use the database file mtime as a session revision. Any session can change that file. Each session uses its own `time_updated` instead. Observed: 2026-08-10, commit 3682f8e6.
- The managed plugin lives at `$XDG_CONFIG_HOME/opencode/plugins/coslash-plugin.js` (default `~/.config/...`). It writes state to `~/.coslash/opencode-permissions/` and `~/.coslash/opencode-clients/`. Collection installs the plugin when it is missing, so `Collect` has a side effect outside the database. Observed: 2026-09-21, commit f557cce8, `TestCollectRetriesPluginInstallation`.

## Format quirks

- OpenCode v1 and v2 can share one database. v1 uses `session`, `message`, `part`, and `todo`. v2 uses `session_v2` and `session_message`. When the same id is in both, the v2 row wins and the v1 row is hidden. Observed: OpenCode 2.0.18, 2026-09-23, commit de40ba0b, `TestMixedOpenCodeSchemasPreferV2AndKeepV1`.
- The v2 CLI ships in a different npm package. `opencode-ai` resolved to v1.18.33 while `@opencode/cli` resolved to v2.0.18. A shim can make `opencode` still run v1. Observed: 2026-09-28.
- v2 beta and preview builds print `0.0.0-beta-...` or `0.0.0-next-...` for `--version`. coSlash treats both as major version 2. Other `0.x` dev strings stay unknown, and an unknown version blocks the plugin install. Observed: 2026-09-28, `TestOpenCodeMajor`.
- OpenCode v2 opens its shared log file even for `--version`. On Windows, a running TUI can lock that file. The version probe uses a temporary `XDG_DATA_HOME`. Observed: 2026-09-21, `collector/internal/vendors/opencode/plugin.go`.
- `session_v2.time_updated` lags real activity. It was earlier than the first user message in a real run. Last activity and incremental polling use the newest `session_message.time_updated`. Observed: OpenCode 2.0.18, 2026-09-28, commit ea925ff1.
- v2 completed tools put result text in `state.content[]`, not the v1 `state.output`. The tool name is in `name`, not `tool`. Without this, shell output for commit detection was lost. Observed: OpenCode 2.0.18, 2026-09-28, commit ea925ff1.
- v2 streaming tool input can be a string instead of an object. A strict decode skipped the whole session as malformed. A `streaming` tool counts as busy. Observed: OpenCode 2.0.18, 2026-09-28, commit ea925ff1, `TestV2IncompleteCompactionAndStreamingTool`.
- v2 stores standalone shell activity as `session_message` rows of type `shell`. They carry `status` (`running`, `exited`, `timeout`, `killed`) and `exit`. A `running` shell keeps the session busy. Observed: OpenCode 2.0.18, 2026-09-28, commit a22959d4, `TestV2StandaloneShellState`.
- v2 compaction rows have `running` or `completed` status and a `summary`. Only completed rows count. Observed: OpenCode 2.0.18, 2026-09-28, commit ea925ff1.
- A v2 `idle` row is a completion marker. The parser maps it to a completed assistant message. Observed: OpenCode 2.0.18, 2026-09-28, `collector/internal/vendors/opencode/parse.go`.
- An assistant message with `agent: plan` is a plan-mode response. v2 rows carry the agent name, and a lost name showed the plan as an ordinary recap. Observed: OpenCode 2.0.18, 2026-09-28, commit cc9939f3.
- Reasoning tokens are added to output tokens. Cost comes from the persisted per-message `cost`, so a model with no price entry does not show a false `$0`. Observed: OpenCode 1.18.4, 2026-08-11, commit 93c5908c.
- The database does not record whether a session came from OpenCode Desktop or the CLI. The managed plugin reads `OPENCODE_CLIENT` and writes `desktop` or `cli` per session. Sessions that started without the plugin have no entrypoint. Observed: 2026-08-13 to 2026-09-03, commit 8ac89595.
- A v2 session row with malformed content skips only its own family. Other sessions still load. Observed: 2026-09-28, `TestMalformedV2ContentSkipsOnlyItsFamily`.

## Status

- Status is scoped to the latest user turn. OpenCode can leave an old assistant message with no `time.completed`, or an old `question` part as `running`, after the user starts a newer turn. A full-transcript scan kept such sessions Active or Waiting for weeks. Observed: OpenCode 1.18.x, 2026-08-14 to 2026-08-18, commit a0dddfda, `TestParseIgnoresIncompleteAssistantFromSupersededTurn`.
- A new user prompt with no assistant row yet is busy for 2 minutes only. After that it counts as abandoned. Observed: 2026-09-18, commits 149c2002 and ae8c47f4, `TestParseExpiresAbandonedPromptBeforeAssistantIsPersisted`.
- A running `question` tool with questions is Waiting, not Active. Waiting has precedence over busy. Observed: OpenCode 1.18.x, 2026-08-13, commit d0f9e4a0.
- Pending permission requests are not in the database. The plugin writes one file per request with the TUI pid. A file whose pid is dead is deleted on read. Observed: 2026-08-18, commits ea9180d2 and 49ef9eca.
- OpenCode does not expose which session a TUI currently shows, and a user can switch sessions inside the TUI. Liveness matches TUI processes to sessions by `--session`, then by working directory and timing (session created after process start, or a user message within 3 minutes of process start). Ambiguous pairs stay inactive. Observed: 2026-08-12 to 2026-09-22, `collector/internal/vendors/opencode/metadata.go`.
- The process cwd comes from `lsof` on POSIX and from the process PEB on Windows. A project argument such as `opencode ./sub` changes the effective directory. Windows compares directories without case. Observed: 2026-09-22, commits fa063106 and 4c72a7c8.
- When process listing fails, the session keeps its transcript status and liveness stays unchecked. It is not forced to Inactive. Observed: 2026-09-29, commit b4364eeb, `TestCollectionDistinguishesUnavailableLivenessFromNoLiveProcess`.

## Resume and launch

- Resume is `opencode --session <ses_...>`. Session ids match `^ses_[0-9A-Za-z]+$`, not a UUID. Observed: OpenCode 1.18.4, 2026-08-10, commit 5c7906b5.
- A fresh session with handoff passes the brief through `OPENCODE_CONFIG_CONTENT='{"instructions":["<file>"]}'`. This does not change repository files or global config. Observed: 2026-08-10, `collector/internal/launch/launch.go`.
- A handoff prompt into the TUI uses `expect` and waits for the `Ask anything` placeholder. Observed: unknown version, `collector/internal/launch/local_posix.go`.
- Review runs `opencode run --pure` with `OPENCODE_PERMISSION={"edit":"deny","bash":"deny"}`. Observed: `collector/internal/launch/launch.go`.
- Synthesis has no system-prompt or schema flag, so both go into the message. v1 uses `--dir`, `--pure`, and `--variant high`. v2 uses `--standalone` and a `#high` model suffix instead. Observed: 2026-08-17 to 2026-09-28, commit 75c351bd, `collector/internal/synthesis/runner.go`.
- Synthesis sets `OPENCODE_DB` to a scratch database, so the run adds no session to the user's list. OpenCode reads `PWD` before the real cwd, and `exec.Cmd` does not update `PWD`, so coSlash sets it. Observed: 2026-08-17, `collector/internal/synthesis/opencode.go`.
- In v2, the plugin deny-all also removes the built-in build agent. Synthesis uses a private `XDG_CONFIG_HOME` instead to exclude global plugins and MCP servers. v2 also needs the live model catalog, so `OPENCODE_DISABLE_MODELS_FETCH` is set only for v1. Observed: 2026-09-28, `collector/internal/synthesis/opencode.go`.
- The model probe in Settings must not run in the collector working directory. OpenCode loads project plugins and config from there, so an untrusted checkout can run code. The probe runs in an empty directory with `--pure`. The model list is limited to free Zen models, so the picker cannot bill an unexpected model. Observed: 2026-08-18, commits bd4dd210 and c0ec17ba, `collector/internal/vendors/opencode/models.go`.

## Open questions

- v2 standalone shell and compaction rows were not exercised in a live run. Only v2-shaped tests cover them. Observed: OpenCode 2.0.20, 2026-09-29.
- A context-window percentage is not available when the provider limit is not in the local database. Observed: 2026-08-10.
- Remote OpenCode sessions are not supported.
