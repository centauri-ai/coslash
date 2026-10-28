# Codex

Code: `collector/internal/vendors/codex/`. This package reads Codex CLI rollouts, archived rollouts, and the shared thread-name index from `~/.codex`.

## Storage

- Active rollouts are `~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<uuid>.jsonl`. Archived rollouts are flat in `~/.codex/archived_sessions/`.
- A session can exist in both trees. The active copy wins, so an archive move does not make duplicate cards or family members. A fork parent that is no longer in the active tree is looked up again in the archive (`TestLocalDiscoveryIncludesArchivedFamiliesAndDeduplicatesActiveCopies`, 2e48020c, 0bfe1727).
- Subagents and forks are not in subdirectories. They are ordinary rollouts in the same date tree. Parentage comes only from the first `session_meta` row, so family grouping reads every header first and leaves bodies closed (`family.go` `HeadersSource`).
- Thread names come from `~/.codex/session_index.jsonl`. One id can occur on more than one row. Complete backup keeps all matching rows, and a malformed row makes attribution incomplete (`metadata.go` `ReadSessionIndexRows`).
- No status file exists. Liveness means that a Codex process holds the rollout open. macOS uses `lsof -c codex`. Windows uses Restart Manager in chunks of 128 files, then binary-splits a chunk that a `codex.exe` process of the current user holds (548a2431).

## Format quirks

- A fork rollout filename is `<root-uuid>_<thread-uuid>`. The thread that the file holds is always the last UUID in the name. Observed: unknown, 2026-09-20, 7d3cf4ac.
- A fork inlines one `session_meta` row per ancestor, and Codex can label all of them with the root id. The header check accepts any id that the filename names, and the rollout's own meta is the newest row whose `payload.id` is a filename id (`TestForkedRolloutPrefersItsOwnInlinedMeta`, `TestChainedForkedRolloutPrefersNewestMatchingMeta`). Observed: unknown, 2026-09-21, 12a71657.
- For a subagent, `payload.session_id` is the parent's id. Only `payload.id` (equal to the filename UUID) is the rollout's own id. Observed: unknown, 2026-08-06, 10d4b6ab.
- A resumed rollout appends a new `session_meta` row with the current branch and cwd. The newest own row must win, or the inspector shows the branch from creation time. Observed: unknown, 2026-08-06, 10d4b6ab.
- `token_count` totals are cumulative, not per event. A rollout can switch models, so coSlash takes deltas and assigns each delta to the model of the latest `turn_context`. A sample with no model is dropped, never put in an empty-string bucket. Observed: unknown, 2026-07-28, 10d4b6ab.
- Fresh input is `input - cached - cache_write` per delta, clamped at zero. Cache-read and cache-write totals move independently, so the raw difference can go negative (`parse.go` `tokenBuckets`). Observed: unknown, 2026-07-28, 10d4b6ab.
- A fork replays the parent's whole `token_count` sequence. coSlash streams the parent only until the first sample that differs, and drops the shared prefix from the fork. If the parent is missing or unreadable, the fork keeps full usage, so the failure mode is over-count (`fork.go`). Observed: unknown, 2026-07-31, 10d4b6ab.
- Parent rollouts can have very long lines with inline tool output. The fork prefix reader uses `json.Decoder` because a `bufio.Scanner` line cap fails silently and double-counts (`fork.go`). Observed: unknown, 2026-07-31, 10d4b6ab.
- Full-history subagents (`fork_turns: "all"`) replay the parent's open `task_started`. With depth counting, the child stayed Running after its `task_complete`. coSlash now counts overlapping starts once (`parse_lifecycle_test.go`). Observed: unknown, 2026-08-20, 1a762cda, 92ae1e3a.
- Codex keeps a finished child rollout open. An open file alone does not prove that a child is running (1a762cda).
- Compaction appears as a top-level `compacted` row and as an `event_msg` `context_compacted`. Both count. Observed: unknown, 2026-09-20, 808fbdfa.
- Two event shapes exist: legacy `event_msg` types (`user_message`, `agent_message`, `sub_agent_activity`) and newer `item_completed` items (`UserMessage`, `AgentMessage`, `FileChange`, `SubAgentActivity`). The parser handles both. Observed: unknown, 2026-08-13, d42e4a16.
- `/review` writes the generated review prompt as two identical `user_message` events. coSlash ignores user messages between `entered_review_mode` and `exited_review_mode` and records one `/review` prompt. Observed: unknown, 2026-07-28, 10d4b6ab.
- Review-style prompts wrap the user text after `## My request for Codex:`. coSlash keeps only the text after that marker (`parse.go` `promptText`). Observed: unknown, 2026-07-31, 10d4b6ab.
- The `codex-auto-review` permission check runs as a child rollout with `source.subagent.other == "guardian"`. coSlash drops it, so it never shows as a subagent. Observed: unknown, 2026-08-14, a0860902.
- Subagent rollouts have no description field. The name comes from a `NEW_TASK` agent message recipient, then the first prompt, then `agent_nickname`. Observed: unknown, 2026-07-31, 10d4b6ab.
- coSlash does not derive an error count from rollouts, so `Errors` is always 0 (`parse.go` comment). Commit and PR detection trust only explicit exit codes: `CommandExecution.exit_code` or an `exit_code` in the function output. Observed: unknown, 2026-08-18, 0138bc6b.
- In code mode, the `exec` tool input is a script that calls `tools.exec_command(...)`. Commands come from a regex over that script, and success is an output block that starts with `Script completed`. Observed: unknown, 2026-08-18, 0138bc6b.
- A created PR is a successful `CommandExecution` for `gh pr create`, or a `::git-create-pr{... url="..."}` directive in a final answer. Failed attempts do not count. Observed: unknown, 2026-08-18, 1677a01e.
- `pullRequestLinks` hold the URLs in the output of a successful create: `CommandExecution` or an `exec_command` function output with `exit_code` 0 for `gh pr create` (also `rtk proxy gh pr create`), the directive URL, or the output of a `function_call` whose name ends with `create_pull_request`. The MCP call is not in any local rollout, so its shape (`function_call` name plus output, failed when the output has `isError` true) follows the Codex tool-call format and is covered only by `TestPullRequestLinksFromSuccessfulCreates`. Observed: synthetic fixture, vendor version unknown, 2026-10-26.
- A code-mode `exec` script can run several `tools.exec_command` calls. The `custom_tool_call_output` is a `Script completed` block and then one block for each call. A block is the JSON result of its call (`exit_code`, `output`) or the plain text of a call that has no exit code. A script always completes, so the inner `exit_code` decides, and a failed `gh pr create` that prints an existing PR URL gives no link. The n-th command pairs with the n-th block, and only when the counts are equal. Observed: Codex CLI rollouts, 2026-10-26, `TestCodeModeScriptPairsEachCommandWithItsResult`.
- PR links change the parsed content, so `ParserVersion` moved to `codex-3`, and to `codex-4` when code-mode pairing changed. The PR links of a session come from the parse, so a cached parse reparses without new transcript activity.
- Waiting on approval: the rollout has the escalation request (`sandbox_permissions: "require_escalated"`) but not the allow-once decision. coSlash runs `codex execpolicy check` with the active `*.rules` files to decide if a prompt was shown. It keeps Waiting until the tool output arrives. Observed: unknown, 2026-08-18, 2637abbf, cfb2af60.
- Plan mode has two signals: `turn_context.collaboration_mode.mode` and `task_started.collaboration_mode_kind`. A plan-mode final answer becomes a plan only when it is wrapped in `<proposed_plan>` or starts with a `# Plan` heading. `# Planetary motion` is not a plan (`TestPlanModeMetadataAndHeadingBoundary`). Observed: unknown, 2026-10-02, 5d17a012.

## Command outputs

- A `function_call` or `custom_tool_call` row whose input holds a `cmd` pairs with the `function_call_output` or `custom_tool_call_output` row that has the same `call_id`. A command without a result row has no output. A failed command is still an output. Subagent rollouts use the same pairing, so their commands carry outputs too. `CommandLog.NoteCall` and `AttachOutput` hold the pairing, and `session.TruncateOutput` applies the 16 KiB cap. Observed: codex-cli 0.164.0, 2026-10-23.
- The `output` field has three shapes. It is a plain string, a JSON string such as `{"output":"...","metadata":{"exit_code":1}}`, or, in code mode, an array of text blocks. The first block is a header (`Script completed\nWall time 0.2 seconds\nOutput:\n`) and the next block is a JSON string `{"chunk_id":...,"exit_code":0,"output":"..."}`. coSlash unwraps the `output` of a JSON shape and keeps the header text. An empty `output` inside a wrapper stays an empty string (`decodeCodexCommandResult`). Observed: codex-cli 0.164.0, 2026-10-23.
- `event_msg` `exec_command_end` and `item_completed` `CommandExecution` carry `aggregated_output` too. coSlash does not read it for command outputs. The rollout result row is the single source, so a command never counts twice. Observed: codex-cli 0.164.0, 2026-10-23: no `exec_command_end` row in recent rollouts.
- Command outputs change the parsed content, so `ParserVersion` moved to `codex-2`.

## Skills

- Explicit loads are `response_item` user messages whose `input_text` starts with `<skill>`, followed by `<name>`, `<path>`, and the captured instruction body. The available `<skill_instructions>` catalog is not a load. The parser removes only the envelope delimiters and preserves body formatting. Evidence supplied for Codex rollouts, vendor version unknown, 2026-10-27; `TestExplicitSkillsPreserveHistory`.
- Implicit loads require a successful result paired by `call_id` with a clear single-file `cat` or `sed -n` read of `SKILL.md`. Code-mode capture accepts only a complete sequence of direct `text(await tools.exec_command({...}))` emissions with a literal command first and optional literal execution settings. It requires matching result counts and each command's own successful exit code. General scripts, variable results, branches, duplicate command keys, and extra emissions are skipped because source command order cannot establish output attribution (`TestCodeModeSkillsSkipAmbiguousScripts`). Listings, writes, mentions, missing results, and ambiguous shell expressions do not count. Only captured output is retained, including partial reads; the parser never opens current skill files to reconstruct history (`TestSkillReadsRequireSuccessfulEvidence`, `TestCodeModeSkillsPairAllCommands`).
- Native `skills.read` counts only when its resource directly identifies `SKILL.md` and its successful result contains text. Reference resources and catalog listings do not count. Opaque resource identifiers without a recognizable main instruction path are deliberately skipped (`TestNativeSkillResourceRead`).
- Skills contain only name and content. Exact pairs deduplicate in observed order through `session.SkillLog`; different historical bodies with the same name remain separate. Ordinary children, archived/backup parsing, and the parse cache use the same vendor parser (`TestExplicitSkillsPreserveHistory`, `TestSkillBackupFamilyAndCache`). Captured skills change parsed content, so `ParserVersion` is `codex-6` (invalidating previously misattributed code-mode skills).

## Resume and launch

- Resume is `codex resume <uuid>`, with `resume` as a subcommand, not a flag.
- Local Start fresh passes the handoff as `-c "developer_instructions=$(cat <file>)"`. The file holds the handoff as a JSON-encoded string (`launch/local_posix.go`).
- Remote Start fresh writes a temporary profile `$CODEX_HOME/coslash-<name>.config.toml` with `developer_instructions` and runs `codex --profile coslash-<name>`. The handoff never goes on the SSH command line (bdda4a10).
- On SSH hosts, a non-interactive SSH command does not read `.bashrc`, so `codex` can resolve to `/usr/bin/codex` and skip a user wrapper that loads provider keys. Remote launches prepend `$HOME/.local/bin` to `PATH`. Observed: 0.155.1, 2026-09-22, 60375cea.
- Codex sets session markers (`CODEX_SESSION_ID`, `CODEX_THREAD_ID`, `CODEX_SANDBOX`, and more). coSlash removes them from launched agents by exact name. `CODEX_HOME` and credential variables share the prefix and must stay. Observed: 0.158.0, 2026-09-28, ab8ead40.
- A Codex `shell_environment_policy.inherit = "core"` setting drops `COSLASH_HOME` from commands, so plugin skills fall back to `~/.coslash` (797d03c6).
- Synthesis uses `exec --json --ephemeral --ignore-user-config --ignore-rules --skip-git-repo-check --sandbox read-only --output-schema <file>`. `--ephemeral` keeps synthesis runs out of `~/.codex/sessions`, so they do not show as sessions.

## Open questions

- Fork presentation is not decided. Counters, timeline rows, and commands in a fork include inherited history.
- It is not known if Codex will add exit codes for function calls. The `Errors: 0` constant assumes that it does not (`parse.go`).
- Allow-once approval decisions are not in the rollout. If Codex adds `serverRequest/resolved` events, Waiting can clear earlier (`parse.go` comment).
