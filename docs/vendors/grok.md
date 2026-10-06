# Grok Build

Code: `collector/internal/vendors/grok/` and `collector/internal/grokcli/`. The package reads local Grok Build sessions from disk, and `grokcli` finds the CLI and builds an isolated home for synthesis and review.

## Storage

- The root is `$GROK_HOME`, else `~/.grok`. Sessions live in `<root>/sessions/<URL-encoded-cwd>/<session-id>/`. The session id is a UUIDv7. Observed: Grok 1.0.46, 2026-10-01, commit 3e284baa.
- Windows uses the same layout under `~/.grok`. The CLI can be in `~/.grok/bin/grok.exe` while `grok` is not on `PATH`. `grokcli.Executable()` and `launch.GrokExecutable()` check that path. Observed: Grok 1.0.46 on Windows build 26100, 2026-10-05, commit d5cb0e8c.
- When the encoded directory name is longer than 255 bytes, Grok writes a slug plus a hash and puts the original path in a `.cwd` file. The macOS sample did not contain this case. Observed: unknown version, 2026-10-01, `xai-org/grok-build` source.
- Indexes outside the session body: `<root>/active_sessions.json` (pid per open session), `sessions/session_search.sqlite` (FTS of titles and prompts), `worktrees.db`, and `models_cache.json`. Only `active_sessions.json` is read. Observed: Grok 1.0.46, 2026-10-01, commit 1348f537.
- The collector reads only `chat_format_version` 1. Version 0 is the legacy format and is skipped. Observed: Grok 1.0.46, 2026-10-01, commit 3e284baa.
- The field map was checked against the public writers in `xai-org/grok-build` at `SOURCE_REV` `559751fdcec02d413e4c57c8832ab275e4f44980`. Subagent, compaction, `plan.json`, and `plan.md` shapes come from that source, not from a captured local session. The fixtures in `testdata/` follow that source schema.

## Format quirks

- `updates.jsonl` is the authoritative conversation. It is JSON-RPC, and the payload is `params.update`. `chat_history.jsonl` and `events.jsonl` are second copies that the collector does not parse. Observed: Grok 1.0.46, 2026-10-01, commit 3e284baa.
- `signals.json` and `usage.json` are written only at turn end. During an open turn, `signals.json` keeps stale zeros and `usage.json` can be absent. As a result, an open turn adds its own `tool_call` rows to the flushed count and leaves tokens, cost, and context fill at the last flushed turn. Observed: Grok 1.0.46, 2026-10-01, commit 3e284baa, `TestParseOpenTurnCountsToolCallsAndLeavesTokensUnknown`.
- `usage.json` `session` is the sum of per-turn deltas. Do not add `turn_completed.usage` on top of it. A `turn_completed` row with stop reason `rate_limit` can have an empty `usage` object. Observed: Grok 1.0.46, 2026-10-01, commit 3e284baa.
- `usage.inputTokens` is the sum of prompt tokens across model calls, not the context fill. Context fill comes from `signals.contextTokensUsed`, which is a pre-sampling estimate. On one session, context was 75694 tokens while session input was 1005673. Observed: Grok 1.0.46, 2026-10-01, `TestParseFinishedSessionUsesUsageTokensAndSignalsContextFill`.
- Cost is `costUsdTicks / 1e10`. The `costIsPartial` and `usageIsIncomplete` flags are omitted when false. If either flag is true, or ticks are missing, coSlash records no cost. Observed: Grok 1.0.46, 2026-10-01, `TestPartialUsageJSONDoesNotRecordCost`.
- `sessionDurationSeconds` is the time that the Grok process was alive for the session. Resume continues the clock. It is not wall time and not model-active time. One session stored 304 seconds against about 417 seconds of wall time. Observed: Grok 1.0.46, 2026-10-01, commit f0d00394.
- Grok writes each edit diff twice: once while the tool runs and once on the completed `tool_call_update`. Only the completed update counts. An empty `oldText` marks a new file. Observed: unknown version, 2026-10-01, commit 6f96d7d0, `TestParseFileEditCountsCompletedDiffOnce`.
- A `user_message_chunk` with `_meta.hideFromScrollback` is an injected system reminder. It starts a model turn, but it is not the user prompt. A chunk with `_meta.bashCommand` is a `!cmd` shell row and starts no turn. Observed: unknown version, 2026-10-01, commit 38220b48.
- Grok Build 1.0.46 can omit `params.update.status` on a tool event and put `Pending` in `params._meta.updateParams.status`. Statuses can also be PascalCase, for example `InProgress`. The parser uses the metadata value only as a fallback and lowercases it. Without this fallback, a shell permission prompt showed Active instead of Waiting. Observed: Grok 1.0.46, 2026-10-06, commit 1342101a, `TestGrokToolPermissionMetadataStatus`.
- An `ask_user_question` tool call can arrive with no status. The parser treats it as pending, so the session is Waiting. A pending tool that moves to `in_progress` clears Waiting. Observed: Grok 1.0.46, 2026-10-05, commits 0f42527a and ce909efc.
- Plan approval is not in `updates.jsonl`. It is `awaiting_plan_approval` in `plan_mode.json`. Observed: unknown version, 2026-10-05, commit 0f42527a.
- `plan.md` is the plan text. `plan.json` is only the todo list, and `goal/plan.md` is the goal contract. A `cancelled` todo is dropped because `Todo.Done` is a boolean. Observed: unknown version, 2026-10-01, commit fa95c033.
- The compaction seed is the `compacted_history` user item in the newest `compaction_checkpoints/*.json` by `created_at`. It is not the system prompt or the `user_info` prefix. Observed: unknown version, 2026-10-01, commit d2fabe48.
- `summary.json` has three similar title fields. `generated_title` is the display name. `session_summary` is a legacy copy of the title. `last_turn_summary` is the one-line synopsis. Observed: Grok 1.0.46, 2026-10-01, commit 3e284baa.
- Entrypoint is not stored directly. `session_kind: headless` or `prompt_context.json` `is_non_interactive` marks a headless run. Observed: Grok 1.0.46, 2026-10-05, commit 0f42527a.
- Child sessions (`session_kind` `subagent`, `subagent_resume`, `subagent_fork`) are normal session directories. The child `summary.json` often omits `parent_session_id`. The reliable link is the parent `subagents/<id>/meta.json` `child_session_id`. A `fork` or `worktree` session keeps its parent link but stays a top-level row. Observed: unknown version, 2026-10-01 to 2026-10-05, commits 805d0bbc, 4bad748e, fdb8b22f, `TestSubagentLinksWhenChildSummaryOmitsParent`.
- The `since` window must treat a parent and its children as one family. A parent resumed today otherwise loses an older child, and a recent child of an old parent is dropped. Observed: unknown version, 2026-10-01, commit 261779d8.
- An interrupted response leaves its `agent_message_chunk` rows in the log, and the retry starts with a new `user_message_chunk`. The parser clears buffered assistant text at each `tool_call` and each `user_message_chunk`. As a result, only the final reply of a turn becomes the recap, and a directed handoff does not settle on a partial reply. Observed: unknown version, 2026-10-05, commits 4c94327b and f8d8ce0a, `TestCompletedAssistantChunksSettleDirectedHandoff`.
- Commit and PR detection ignores `--dry-run` only when the flag is outside quotes. A commit message that contains the text `--dry-run` still counts. Observed: unknown version, 2026-10-05, commit fdb8b22f, `TestQuotedDryRunTextDoesNotHideACommit`.
- Metadata files can be read while Grok rewrites them. `readJSON` decodes into a temporary value first, so a truncated file does not leave a partial value. JSON files over 64 MiB are rejected before decode. Observed: unknown version, 2026-10-05, commits bee6d1d3 and 9d73f57e.

## Status

- A session is live only when its `active_sessions.json` pid is alive. An open turn with a dead pid is Idle, not Inactive, unless the session was Waiting. A Waiting session with a dead pid is Inactive (commit 4d75008e). A finished session stays Inactive. Observed: Grok 1.0.46 on Windows, 2026-10-05, commit 459bd350.
- A missing `active_sessions.json` means liveness was checked and nothing is live. An unreadable or malformed file leaves liveness unchecked. Observed: unknown version, 2026-10-01, commit 1348f537.

## Resume and launch

- Resume is `grok --resume <id>`. Grok 1.0.46 `--help` shows `--resume <SESSION_ID_OR_TITLE>`. Observed: Grok 1.0.46, 2026-10-05, commit b9d49664.
- A terminal that is already open does not inherit the collector environment. Without a `GROK_HOME` prefix, resume looked in the default store and Grok tried a remote restore that returned 404. `withGrokHome` now puts `GROK_HOME` on the command. Windows uses PowerShell syntax and restores the old value after exit. Observed: Grok 1.0.46, 2026-10-02, commits 05e3e45e, f92a2655, 1d1a1788.
- A Grok source session offers Start fresh, not a directed handoff. A handoff that starts from a Grok session is out of scope. A rebase once brought back the handoff dialog for Grok sources, and commit 0109084b removed it again. Grok is still a valid handoff destination. Observed: 2026-10-02 to 2026-10-06, commits bd605f10 and 0109084b.
- On macOS and Linux, a handoff into Grok types the prompt into the TUI through `expect`. The ready marker is the `Type a message...` placeholder. Observed: unknown version, 2026-10-05, `collector/internal/launch/local_posix.go`.
- Windows has no `expect` delivery. A handoff into Grok first runs a one-turn seed session with `--session-id`, `--prompt-file`, `--tools=`, and `--max-turns 1`, then resumes it with a follow-up message. The seed turn asks Grok only to acknowledge the context, so the session keeps that acknowledgment turn. Observed: unknown version, 2026-10-05, `collector/internal/launch/local_windows.go`.
- Synthesis and review run with `GROK_HOME` set to a scratch home and `GROK_MEMORY=0`, so the run adds no row to the user's session list. `PrepareHome` symlinks `auth.json` on POSIX and copies it on Windows, because file symlinks need extra privileges there. A relative `GROK_HOME` made the symlink point into the scratch directory, so all paths are made absolute first. Observed: unknown version, 2026-10-05, commits 645cc835, ad86d41b, f709d4dd.
- `--tools ""` and `--disallowed-tools` did not stop a synthesis run from reading a file outside the working directory on Windows. `--deny "*"` blocked file and shell access. Observed: Grok 1.0.46 on Windows, 2026-10-05, commit e24c6cc0.
- Review uses a custom sandbox profile `coslash-review` that extends `strict` and adds the scratch directory as read-only. The profile fails closed on macOS and Linux when the kernel policy cannot apply. Observed: unknown version, 2026-10-05, commit 0a0f72ac, `collector/internal/launch/launch.go`.
- The Grok 1.0.46 Windows sandbox does not enforce a worktree read boundary. A review probe read a canary file outside the worktree. This is disclosed in `docs/data-and-privacy.md` and is not fixed. Observed: Grok 1.0.46 on Windows, 2026-10-05, commit 7df1b240.
- A sandboxed launch of the real Grok CLI did not stay alive. Grok needs read access to its existing auth and managed-policy files. Observed: Grok 1.0.46 on Windows, 2026-10-05.
- Grok synthesis, review, and handoff destination are macOS and Windows only (`vendors.GrokSynthesisSupported`). Remote Grok is not supported. Session collection has no platform gate.

## Open questions

- The `.cwd` file for long encoded directory names is not read. Observed: 2026-10-06.
- `subagent_resume` and `subagent_fork` live entrypoints and the permission approval-to-execution transition were not exercised with a real CLI. Observed: 2026-10-06.
- Authenticated behavior of an independent new Grok home is not established. Tests used a link to the existing login. Observed: 2026-10-02.
- The `question` digest category (`ask_user_question` arguments and answer) was not seen in a local sample. Observed: 2026-10-01.
