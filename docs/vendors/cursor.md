# Cursor

Code: `collector/internal/vendors/cursor/`. This package reads local Cursor IDE and Cursor CLI transcripts and joins them with Cursor's SQLite side stores. Remote and SDK sessions are out of scope.

## Storage

- Transcripts for both lanes are `~/.cursor/projects/<workspace-slug>/agent-transcripts/<uuid>/<uuid>.jsonl`. Subagents are `agent-transcripts/<parent-uuid>/subagents/<uuid>.jsonl`.
- The transcript does not say if it came from the IDE or the CLI. The lane is side-store evidence only (242f39b0).
- IDE side store: `<globalStorage>/state.vscdb`, tables `composerHeaders` and `cursorDiskKV`. `globalStorage` is `~/Library/Application Support/Cursor/User/globalStorage` on macOS and `%APPDATA%\Cursor\User\globalStorage` on Windows.
- `cursorDiskKV` keys are `composerData:<id>` and `bubbleId:<id>:<bubble>`. Key case varies, so queries match the id segment with `LOWER()` (`TestCursorKeyQueryMatchesWholeIDSegmentIgnoringCase`).
- Other IDE stores in the same directory: `conversation-search.db` (titles, prefer `source = 'local'`). Summaries are in `~/.cursor/ai-tracking/ai-code-tracking.db`, table `conversation_summaries`.
- CLI side store: `~/.cursor/chats/<md5(cwd)>/<uuid>/store.db`, with `meta.json` beside it. The `meta` row with key `'0'` holds hex-encoded JSON with `agentId`, `name`, `lastUsedModel`, and `subagentInfo`.
- Session ids can be mixed case on disk. coSlash makes every id lowercase before it joins anything (`TestCursorPathIDsAreCanonical`, 4261ed07).
- All stores open read-only with `mode=ro`, `_query_only=1`, and a 1 s busy timeout, because Cursor holds them open.

## Format quirks

- SDK transcripts use an `agent-<uuid>` directory and file name. coSlash excludes them before parsing and does not read SDK stores. The SDK store has no parent id, so SDK subagents cannot be rebuilt. Observed: unknown, 2026-09-16, faffd465.
- An id that appears in both the IDE and the CLI stores gets no lane. coSlash then clears every lane-specific field (model, cwd, times, usage, edits, commits) for that id. Joins use stable ids only, never timestamps. Observed: unknown, 2026-09-16, 242f39b0.
- Rows have no timestamp field. The parser reads only `role`, `type`, `message`, `status`, and `error` (`types.go` `transcriptRecord`). Observed: unknown, 2026-09-14, 781c1e6d.
- CLI user rows wrap the prompt as `<timestamp>Friday, Jul 10, 2026, 10:29 AM (UTC-7)</timestamp><user_query>...</user_query>`. This minute-precision local time is the only in-transcript time (`parse.go` `parseTimestamp`). Observed: unknown, 2026-09-14, 781c1e6d.
- IDE user rows in the sample have neither `<timestamp>` nor `<user_query>`. They start with blocks such as `<attached_files>` or `<external_links>`. IDE start and end times must come from `composerHeaders` (`metadata.go`). Observed: unknown, 2026-09-16, 242f39b0.
- The IDE sample transcript has no `tool_use` blocks. IDE tool activity, terminal commands, and diffs are in `bubbleId` rows and `composerData`, not in the JSONL. Observed: unknown, 2026-09-16, 242f39b0.
- `turn_ended` is rare. The CLI sample has 38 user rows and one `turn_ended`. A turn with no end record still gets its last text-only assistant reply as a recap (`TestParseTranscriptAddsRepliesWithoutTurnEndedRecords`). Observed: unknown, 2026-09-18, 1a11d720.
- For a live session, the trailing reply is not final yet. coSlash adds it as a recap only after the session is no longer live. Observed: unknown, 2026-09-18, a582950a.
- `turn_ended.status = "error"` is the only reliable error terminal. It overrides a store that still looks open (`source.go`). Observed: unknown, 2026-09-16, f787cebd.
- Transcripts have tool intent but no tool results. Commit attempts and PR URLs in assistant prose are not reported from the transcript (`TestParseTranscriptDoesNotReportUnverifiedCommitAttempts`, `TestParseTranscriptDoesNotCountPullRequestURLsFromAssistantProse`). Observed: unknown, 2026-09-18, d04b09f3.
- Verified commits come from IDE bubbles. Older terminal bubbles are `run_terminal_cmd` with `gitCheckpoint` and `afterGitCheckpoint`. Newer ones are `run_terminal_command_v2`, with `result` as JSON `{output, ...}` and often no checkpoint. A v2 commit counts only when the output shows a hash. `git commit --quiet && git rev-parse HEAD` is supported. Observed: unknown, 2026-09-22, 5a8e0bad, fb6520fc.
- The shell tool has two names: `Shell` (CLI) and `run_terminal_cmd`. `ApplyPatch` input can be a raw string, not an object. Patches use the `*** Update File:` and `*** Move to:` format. Observed: unknown, 2026-09-14, 781c1e6d.
- Model names use Cursor's own form, for example a version before the family and effort suffixes such as `-thinking` or `-high`. coSlash normalizes them to catalog ids and prefixes Grok with `xai/`. The label `default` means unknown (`metadata.go` `normalizeCursorModel`). Observed: unknown, 2026-09-14, 242f39b0.
- One IDE chat can use many models. Each bubble has `modelInfo.modelName`, so coSlash keeps a bounded history and picks the newest by bubble time, then by key. Observed: unknown, 2026-09-22, 980f1163, 512a3731.
- `composerData.contextTokensUsed` is current context occupancy, not cumulative usage. It must never fill `Tokens`. The CLI stores no reliable token or cost total, so those stay unknown, not zero. Observed: unknown, 2026-09-16, 760f60a4.
- IDE cost is the sum of `usageData.*.costInCents`. If one entry is missing or negative, the whole cost is unknown. Observed: unknown, 2026-09-16, 242f39b0.
- Cursor has no compaction event. The IDE `composerData.latestConversationSummary` is the only seed, and it applies to the IDE lane only. Observed: unknown, 2026-09-17, 0cddba9c.
- An IDE question is pending when a bubble has `toolFormerData.name = "ask_question"` and `additionalData.status = "pending"`. That bubble stays after the chat ends, so Waiting applies only when the session is also live (`TestLoadIDEModelsMarksOnlyLivePendingQuestionsWaiting`). Observed: unknown, 2026-09-21, 2b22acd2.
- IDE parent links are in `composerHeaders.value.subagentInfo.parentComposerId`. CLI parent links are in the store `subagentInfo.parentAgentId`. IDE family loading repeats the query until no new id appears. Observed: unknown, 2026-09-18, 8eb09eed.
- Large id lists must be batched. Each id adds two OR terms, and SQLite has a default expression-depth limit (`metadata.go`). Observed: unknown, 2026-09-18, a246d70f.

## Liveness

- macOS IDE: `lsof -c Cursor` shows `AgentStores/cursor_agent_stores/<id>/.sync/index.sqlite` (or `-wal`, `-shm`) for an open chat.
- macOS CLI: `ps` finds `.../cursor-agent/versions/<v>/index.js` processes, then `lsof` on those pids shows `~/.cursor/chats/<hash>/<id>/store.db`.
- Windows IDE: no per-chat file signal is used. If `Cursor.exe` runs, only the chat in `ItemTable` key `cursor/glass.selectedAgent` is live. Observed: unknown, 2026-09-21, 2b1c7bbf.
- Windows CLI: `node.exe` from `%LOCALAPPDATA%\cursor-agent\versions\<v>\` with `--resume <id>` on its command line, or a store held open by that process. Only the 256 newest stores are probed, ranked by the newest of `store.db`, `-wal`, and `-shm`. Observed: unknown, 2026-09-22, bd514246.
- Windows can run the CLI from an MSIX virtual path `Packages\<pkg>\LocalCache\Local\cursor-agent\...`. coSlash accepts it only when it is the same file as the real path (`TestCursorAgentExecutableRequiresSameFileForVirtualAlias`).

## Resume and launch

- Only CLI sessions can resume. Cursor has no command to reopen a specific IDE chat, so an IDE session gets **Open Cursor** on its workspace (`cursor --reuse-window <dir>`).
- The CLI command is `cursor-agent`. The shorter `agent` name is used only when it resolves into a `cursor-agent/versions/` install, because `agent` is a generic name (`settings.go` `CursorExecutable`). On Windows, the CLI is `agent.ps1`, which runs through `powershell.exe -ExecutionPolicy Bypass -File`. Observed: unknown, 2026-09-22, 9d42bb2c.
- The CLI files each chat store under `md5(<launch directory>)`, and coSlash resumes from that directory. It can be an ancestor of the recorded cwd, so `ResumeDirectory` walks up from the cwd and from edited file paths until the MD5 matches. Observed: unknown, 2026-09-22, 9d42bb2c, 6174ea6c.
- Start fresh into Cursor CLI copies the handoff to the clipboard. The interactive relay waits for the text `0 in` from the Cursor TUI, not for bracketed paste mode, before it sends input. Observed: unknown, 2026-09-28, 6be0c7b9, a673d2ec.
- Synthesis and review run with `CURSOR_DATA_DIR` set to a scratch directory that has a `.cursor/cli.json` deny list. This keeps the runs out of the user's Cursor history and blocks shell, write, web, and MCP tools. Synthesis also denies `Read`. Observed: unknown, 2026-09-19, 2044b667.
- `--sandbox` is `enabled` on macOS and `disabled` on Windows for synthesis and review. The reason for Windows is not recorded.

## Open questions

- It is not known which persisted Cursor states map to Waiting and terminal status for CLI sessions. IDE status fields can be unset.
- It is not known what evidence proves that IDE cost entries are complete enough for aggregate reports.
- Remote Cursor collection and SDK support are deferred.
- The meaning of CLI user rows that start with `Run the following command:` and have no `<user_query>` is not confirmed. They count as turns today (`parse.go`).
