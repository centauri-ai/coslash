# Pi

Code: `collector/internal/vendors/pi/`. The package reads local Pi JSONL transcripts without mutation and reads runtime evidence that a managed Pi extension writes.

## Storage

- Default transcripts live in `~/.pi/agent/sessions/--<encoded-cwd>--/<timestamp>_<session-id>.jsonl`. The session id and `cwd` come from the header line, not from the file name. Observed: Pi 0.99.1, 2026-09-30, commit f8f91131.
- Pi has four storage overrides: `PI_CODING_AGENT_DIR`, `PI_CODING_AGENT_SESSION_DIR`, the `sessionDir` setting (global `settings.json` or project `.pi/settings.json`), and `--session-dir`. coSlash cannot see a `--session-dir` flag. It finds such a root only through the transcript path in runtime evidence or through `COSLASH_PI_SESSION_ROOTS`. Observed: Pi 0.99.1, 2026-09-30, commit f8f91131.
- A project `sessionDir` is relative to the project directory, and `~` expands to home. Project settings are found from the collector cwd and from each transcript header `cwd`, including headers with an unsupported schema. Observed: 2026-10-01, commit d71bb08c, `TestUnsupportedHeaderDiscoversProjectOverride`.
- `--no-session` runs leave no transcript. Observed: Pi 0.99.1, 2026-09-30.
- Two transcripts with the same header id are both skipped and reported. coSlash never picks one by file name order. Observed: 2026-10-01, `collector/internal/vendors/pi/source.go`, `TestSourceCustomIdentityDedupAndConflict`.
- Runtime evidence lives in `$COSLASH_HOME/pi-runtime/` (live owners) and `$COSLASH_HOME/pi-history/` (retained exited owners). The managed extension is `<agent dir>/extensions/coslash-extension.ts`. Observed: 2026-09-30, commit 535a7167.
- On Windows, the managed install is `~/.pi/agent/bin/pi.cmd`, and that directory is often not on `PATH`. `vendors.PiExecutable` checks `PATH` first, then that path. Observed: Pi 1.0.0 on Windows, 2026-10-02, commit 6c205897, `collector/internal/vendors/pi_executable_windows_test.go`.

## Format quirks

- Only header `version` 3 is supported. Other versions are skipped and reported. The transcript schema version is not the Pi CLI release version. Observed: Pi 0.99.1 to 1.0.0, 2026-09-30, commit 0a6b8eab.
- Do not use Pi's `SessionManager.open()` to read a transcript. It can append a newline to repair the tail and can migrate old formats in place. The parser reads raw JSONL in memory. An incomplete final record is accepted and flagged, which makes usage unavailable. Observed: Pi 0.99.1, 2026-09-30, commit 0a6b8eab, `collector/internal/vendors/pi/parse.go`, `TestReadOnlyFinalRecords`.
- A session is a tree of entries. Branches live inside one file. The live leaf can move without a file write, so the last persisted entry is not always the active branch. Current-state fields (model, context, summary) follow the leaf from verified runtime evidence when live owners agree. The digest shows all branches in time order. Observed: Pi 0.99.1, 2026-09-30, commit 5e0c67ae, `TestRuntimeLeafChangesCollectedCurrentStateOnly`.
- `parentSession` in a header marks a fork or a clone, not a subagent. Mapping it to `ParsedSession.ParentID` hides the fork. Pi has no native subagents, and extension subagents are out of scope. Observed: Pi 0.99.1, 2026-09-30 to 2026-10-01, commit 0a6b8eab.
- A fork copies parent history, including usage records. Summing parent and fork double-counts the inherited work. Inherited usage is removed only when it matches a verified parent by identity and payload. If provenance cannot be verified, the fork tokens and cost are unavailable, not zero. Observed: Pi 0.99.1, 2026-09-30, commit 5e0c67ae, `TestProjectionForkAttribution`.
- A selected-path clone can regenerate label entries and rewrite parent links around them. Whole-entry equality is not a valid inheritance test. Parent rewrites can bypass only label entries, and `firstKeptEntryId` can also skip removed labels. Observed: Pi 0.99.1, 2026-10-01, commit 530e2a84, `collector/internal/vendors/pi/fork.go`.
- A fork parent can be outside the discovered roots. Fork ancestors are parsed for usage evidence even when they are older than the `since` window. Unrelated old transcripts stay unread. Observed: 2026-10-01, commits c75788fe and fd2cb9d3, `TestIncrementalCollectionKeepsExternalIntermediateAncestor`.
- Usage exists outside assistant messages: standalone usage, tool results, compactions, and branch summaries. Some of it has no model attribution. Nested tool usage is already in the enclosing result, so counting both double-counts. Observed: Pi 0.99.1, 2026-09-30, commit 5e0c67ae.
- `cacheWrite1h` is a subset of `cacheWrite`, and reasoning is a subset of output. coSlash records `cacheWrite - cacheWrite1h` as ordinary cache creation. Observed: Pi 0.99.1, 2026-09-30, commit 1b2ecc71, `TestUsageOneHourCacheIsNotZero`.
- The selected model and the response model can differ. Attribution uses `provider` plus `responseModel`, else `model`. Recorded costs are Pi's price estimates, and zero can mean missing pricing. Observed: Pi 0.99.1, 2026-09-30, commit 0a6b8eab.
- Context tokens are post-response occupancy, so they include generated output. After a compaction, context edit, or branch summary, context is unknown until the next assistant usage. Observed: 2026-09-30, `collector/internal/vendors/pi/projection.go`.
- The `edit` tool sends an `edits` array of `{oldText, newText}`. Older transcripts use top-level `oldText` and `newText`. Both shapes can appear in one call. A failed tool result is not an edit. A `write` call has no previous content, so coSlash invents no deletion count or new-file flag. Observed: Pi 0.99.1, 2026-09-30, commit 66e624c9, `TestProjectionNativeEditArrays`, `TestProjectionFailedWritesAreNotEdits`.
- Transcript reading is bounded: 64 MiB per file, 16 MiB per record, 100000 entries. Observed: 2026-10-01, commit 8b80557b.

## Status

- Transcripts cannot show whether a TUI is running. RPC `get_state` has no waiting field, and a separate RPC process cannot see another TUI. Reliable status comes only from the managed extension. A session without the extension is Unknown, not Inactive. Observed: Pi 0.99.1, 2026-09-30, commit 535a7167.
- `agent_end` arrives before Pi settles. The extension uses `agent_settled` for idle. Observed: Pi 0.99.1, 2026-09-30, commit 535a7167.
- A blocking dialog can coexist with `ctx.isIdle() == true`, so waiting (`ui_prompt_start`) wins over idle. Compaction callbacks can still see busy flags, so the extension republishes every 250 ms. Observed: Pi 0.99.1, 2026-09-30, commit 535a7167, `collector/internal/vendors/pi/coslash-extension.ts`.
- SIGKILL produces no shutdown callback, and two processes can open the same session. Each owner is verified by pid plus an OS process start identity. A reused pid proves that the recorded owner died. One owner exit does not erase another owner's evidence. On Windows, the start identity is the exact `StartTime.ToFileTimeUtc` value without rounding. Observed: Pi 0.99.1, 2026-09-30 to 2026-10-01, commit 303ba5af, `TestWindowsProcessIdentityUsesExactFiletime`.

## Resume and launch

- The CLI release gate is a minimum tested baseline of 0.99.1, and newer stable releases pass. An earlier exact allowlist (0.99.1 and 0.99.2) blocked Pi 1.0.0: status stayed Unknown, Resume and handoff returned 409, and Send returned 400. The same check is in Go (`RuntimeSupported`) and in the extension. Observed: Pi 1.0.0, 2026-10-02, commit ba01f708.
- Resume passes the absolute transcript path to `--session`, not the id. Pi accepts non-UUID ids and prefix lookup, and a global id lookup can prompt to fork. Before resume, coSlash checks that the header id, schema, and `cwd` did not change. Observed: Pi 0.99.1, 2026-09-30, commit ace08034, `collector/internal/launch/pi.go`.
- A GUI terminal can run with a different environment. The launch command sets `COSLASH_HOME`, `PI_CODING_AGENT_DIR`, and `PI_CODING_AGENT_SESSION_DIR` explicitly. Observed: 2026-10-01, `collector/internal/launch/pi.go`.
- Handoff context goes through `COSLASH_PI_HANDOFF_FILE`, and the extension appends it to the system prompt for the startup session only. A replacement runtime must not consume the notes again. The launch does not use `--append-system-prompt`. That flag suppresses automatic `APPEND_SYSTEM.md` loading, and a missing file becomes literal path text. Observed: Pi 0.99.1, 2026-09-30, `collector/internal/vendors/pi/coslash-extension.ts`.
- Pi installs its editor submit handler before the startup `session_start` event. The extension then prints `ESC ]777;coslash-ready=<name> BEL`, and `expect` waits for that marker before it types a prompt. Observed: 2026-10-01, commit 17cf6fb4.
- Synthesis uses `--print --no-session` with all resources disabled. The system prompt goes to `--system-prompt` as a path to a private file. `--append-system-prompt " "` stops automatic `APPEND_SYSTEM.md` loading. An empty value does not work, because Windows native argument transport drops it. Observed: 2026-10-06, commit 3cf08183, `collector/internal/synthesis/runner.go`.
- Review checks Pi with `--help`, but Pi sends help to stderr in print mode. The probe keeps the isolation flags. Observed: `collector/internal/launch/launch.go`.
- Pi is local only on macOS and Windows. Linux and remote Pi are not supported. Windows was enabled after native probes passed. Observed: 2026-10-01 to 2026-10-02, commits 7f72d2f1, b7dc1a9e, 0f0c11e6.

## Open questions

- Older transcript schemas and older real Pi runtimes are not tested. Native probes passed on Pi 0.99.1, 0.99.2, and 1.0.0 only. Observed: 2026-10-05, commit 01fdcfb7.
- Accurate active duration is not available. A timestamp span includes idle time. Observed: 2026-09-30.
- On Windows, synthesis is verified only with an offline provider. A run with a real provider is not verified. Observed: Pi 1.0.0, 2026-10-06, commit 3cf08183.
- Historical sessions in an unknown override directory are found only when runtime evidence or configuration names that directory. Observed: 2026-09-30, commit f8f91131.
