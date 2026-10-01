# Hard-delete agent sessions implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` or `superpowers:executing-plans` to implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a coSlash user permanently remove one closed, local agent session's transcript data from this machine, so the vendor cannot read or resume it.

**Architecture:** The UI sends the composite `sourceId + agent + id` identity to one guarded collector endpoint. The collector accepts only `sourceId=local`, checks the current session and liveness, calls a vendor-specific local deletion function, verifies absence, then refreshes its view. Remote sessions remain readable in coSlash but cannot be deleted through this feature.

**Tech Stack:** Go collector, React/TypeScript frontend, existing HTTP guard and `apiFetch`, existing vendor discovery, SQLite where Cursor needs it.

**Spec:** [ENG-1447](https://linear.app/centauri-ai/issue/ENG-1447/hard-delete-agent-session-transcripts-from-the-coslash-ui) and its investigation comments.

## Global constraints

- Scope: local Claude Code, Codex, Cursor IDE/CLI, and OpenCode sessions on macOS, Windows, and Linux where the collector and vendor are supported. Remote sessions are out of scope. Do not claim support for an unverified layout.
- A session must be closed before deletion. Recheck liveness locally at execution time; a stale or missing status is not proof that deletion is safe. Refuse with no data removed if active or unverifiable.
- Success means the vendor cannot read or resume that session from active local storage and coSlash no longer shows it after refresh or restart. Erasure of recoverable bytes in SQLite free pages, WAL, backups, filesystem snapshots, or vendor cloud storage is outside this scope.
- Keep all other sessions and configuration intact. Validate IDs and discovered paths; never interpolate an ID into a shell command or delete by a broad glob. Failed or partial deletion must be reported as failure, never as success.
- UI only: no `coslash delete` CLI command. The action belongs in the table row ellipsis and the board card actions for local sessions only. Confirm the irreversible operation and show a useful active-session or failure message.
- Read `collector/AGENTS.md`, `frontend/AGENTS.md`, and the closest normative README before editing. Use the existing guarded API, `apiFetch`, `sessionKey`, and shared status helpers. Do not edit generated files or `CHANGELOG.md`.
- Do not test against personal sessions. Create disposable vendor sessions and isolated home directories. Tests must run without installed vendor CLIs, network access, machine-specific paths, or wall-clock assumptions.

## Fixed boundary contract

- `DELETE /api/sessions?source=local&agent=<agent>&id=<sessionId>` with the existing per-run token; exact source, vendor, and ID are required. Reject every other source with `409 remote_action_unsupported` before any deletion work. `204` means verified absent. Other JSON errors use `writeAPIError`: `400 invalid_session`, `404 session_missing`, `409 session_active`, `409 session_unverified`, `500 session_delete_failed`. The frontend renders the returned safe `error` text and keeps the card on failure.
- Vendor packages expose `DeleteSession(ctx context.Context, home, id string) error`. The caller supplies this machine's home directory. A function returns nil only after all known active-storage artifacts for that session have been removed and its vendor read/resume path fails. Typed errors distinguish missing, active/unverified, and failed/partial deletion. Do not add a generic plugin registry.
- Deletion is one session family: include child/subagent transcripts and session-specific sidecars, but never remove shared indexes wholesale. The exact artifact map is taken from the existing collectors and confirmed with disposable sessions for each vendor and platform.

## Review focus

1. Duplicate UUID on local and remote sources or between vendors: only the requested local composite identity changes.
2. Session becomes active between list display and click: server refuses before the first write.
3. Cursor database is open, locked, or has a WAL: failure leaves other chats readable and never reports success prematurely.
4. A sidecar, child transcript, or vendor resume entry survives the main transcript: deletion remains failed until read/resume is impossible.
5. Remote card or forged remote-source API request: no Delete action in the UI and a server-side refusal before any file operation.

## PR boundaries and dependencies

Each implementation task below is one PR. Follow the [agent-tooling PR reviewer service size preflight](https://github.com/centauri-ai/agent-tooling/blob/main/.agents/skills/agent-pr-reviewer-service/SKILL.md): it has a **15-minute review budget** and normally declines a first review when the PR's reviewable scope has **more than 25 files**, **more than 1,000 added plus deleted production-source lines**, or **at least three independently shippable subsystems**. A feature spanning frontend and backend around one behavior counts as one subsystem; tests, generated files, lockfiles, vendored code, docs, and schema artifacts do not consume the production-line budget, though the reviewer still checks their adequacy. The skill permits an exception for clearly mechanical changes governed by one repeated invariant; do not rely on that exception for session deletion. Measure each PR against its parent with `git diff --stat <parent>...HEAD` and `git diff --numstat <parent>...HEAD`, classify production-source lines, and count subsystems; do not measure the whole stack against `main`. If a guardrail would be crossed, split at a tested behavior boundary before opening the PR. Never drop necessary tests or safety checks to fit it. A split becomes a separately reviewed PR and must leave the branch compiling and its advertised behavior honest.

| Stage | PR task | Depends on | Can implement in parallel with |
| --- | --- | --- | --- |
| 1 | A: Codex deletion | None | B, C, D, F |
| 1 | B: OpenCode deletion | None | A, C, D, F |
| 1 | C: Claude deletion | None | A, B, D, F |
| 1 | D: Cursor CLI deletion | None | A, B, C, F |
| 1 | E: Cursor IDE deletion | D | A, B, C, F after D is ready |
| 2 | H: Local collector endpoint | A, B, C, E | F implementation against the fixed contract |
| 3 | F: Table UI and shared confirmation | H for a working PR; fixed contract for parallel implementation | A, B, C, D, E, H |
| 3 | G: Board UI action | F | H implementation before rebasing |

The dependency column is about code. Keep PRs in **one linear stack** for review: `A -> B -> C -> D -> E -> H -> F -> G`, with each PR targeting the preceding PR's branch. The endpoint precedes the UI so a merged UI PR has a working delete action. Keep this plan document in its own docs-only PR or the accepted base, not in A's adapter diff. Independent implementers may work in parallel in isolated workspaces, but rebase each completed task onto its designated parent before opening its PR; wait for that parent to settle before reviewing a code-dependent task. Independent vendor adapters may open draft PRs and review their isolated patches while a parent settles; rebase onto the settled parent and rerun checks before declaring the PR ready. Confirm the reviewed patch and its dependencies remain unchanged; a conflict or behavior change requires independent review of that change. Review only that PR's delta against its parent. If a later rebase changes behavior or conflicts with another task, rerun focused tests and the independent review on the new head. Do not merge an ancestor until its descendants have been rebased onto the resulting base. The order is for readable PRs, not a claim that all tasks depend on one another.

Keep commits focused and granular inside each PR: one commit per independently testable behavior, with its regression test and minimal implementation together. Use a separate commit for a distinct behavior or reviewer fix. Do not mix vendor adapters, UI, formatting sweeps, or unrelated cleanup in one commit. Before opening each PR, inspect `git log --oneline <parent>..HEAD`, `git diff --stat <parent>...HEAD`, and `git diff --numstat <parent>...HEAD`; squash exploratory or formatting-only commits into the behavior they serve. Keep a reviewer fix in its own focused commit so the single re-review can inspect the exact delta. Do not add agent co-author trailers.

The master orchestrator may use Superset CLI to create isolated workspaces and terminal panes for implementer and reviewer agents. First run `superset auth whoami --json`, `superset terminals --help`, `superset hosts list --json`, `superset agents list --local --json`, and `superset workspaces list --local --json`. Require terminal `list/read/send/close`; if missing, update Superset and recheck. Run at most two implementation lanes concurrently, so this does not become a swarm; A/B and then C/D are valid waves. Create each parallel editing worker in its own branch/workspace with `superset workspaces create`, then use `superset agents create --workspace <workspace> --host <host> --agent <terminal-preset> --prompt <task-brief> --json`. Create every feature workspace with `--tag hard-delete`, including reviewer worktrees if any, to keep them together in Superset. Require `kind: terminal`; save the returned `sessionId`. Never use `--agent superset`, which is a chat session outside terminal control. Read progress with `superset terminals read --workspace ... --host ... --terminal ... --json`; send the reviewer findings back to the *same* implementer with `superset terminals send ... --text ... --json`. Track task, dependency, workspace, branch, PR/base, host, terminal ID, status, checks, and review result in a small coordinator table. Do not create a new orchestration service or Linear subissues.

After each task's implementation is done and its focused PR is open, launch an independent reviewer agent on that PR. Have it review correctness, data loss risk, and the five review-focus cases using the PR's parent as its base. Send findings to the same implementer. The implementer gets one response round; the reviewer checks that repair once. If material findings remain, mark the PR blocked instead of looping or merging. Do not run multiple editors against the same worktree. Keep worker terminals available for inspection.

After **each stage**, check out the reviewed stack tip in this worktree, start the app here, and open it in a Superset browser pane. Use `superset browser --help`, then `superset browser open --workspace <id> --url <app-url> --target new-tab --json`; inspect a screenshot, visible state, and console after a safe interaction. Stage 1 checks the existing table/board and disposable vendor sessions; no Delete UI exists yet. Stage 2 exercises the guarded endpoint from the app browser with disposable sessions: successful deletion, active refusal, remote-source refusal, and absence after refresh/restart. Stage 3 exercises the full table/board action, confirmation, cancel, remote-card exclusion, errors, desktop and narrow layouts, and keyboard focus. Use the actual app URL and token printed by this worktree's server; do not reuse an older server's URL. Browser evidence must come from the UI or API result, not from a successful browser command alone. If a target OS is unavailable, record that gate as unverified; never substitute a mock for the final platform check.

## Task A: Codex deletion PR

**Files:** Add `collector/internal/vendors/codex/delete.go` and `delete_test.go`; change Codex discovery only if needed.

**Interface:** Export `DeleteSession(ctx context.Context, home, id string) error`. Keep the mutation boundary injectable in tests without adding a global vendor framework.

- [ ] Read Codex discovery and metadata. Reconfirm `codex delete --force <id>` against the installed CLI and inspect what it leaves behind.
- [ ] Write failing isolated-home tests for exact ID, neighboring session, invalid ID, active/unverified status, CLI failure, and a remaining artifact.
- [ ] Use scoped local transactions and coordinated exact-owned file cleanup. Investigation of Codex 0.159.3 found that native deletion rewrites the shared index under only a process-local mutex and can lose a neighbor append; do not delegate to that unsafe rewrite. Match vendor writer/history locks, preserve shared append handles, retain validated ownership for partial-cleanup retries, and return success only when discovery/resume cannot find the ID.
- [ ] Run focused Go tests; inspect the PR-size and commit gates above; open the Codex-only PR for independent review.

## Task B: OpenCode deletion PR

**Files:** Add `collector/internal/vendors/opencode/delete.go` and `delete_test.go`; change OpenCode discovery only if needed.

**Interface:** Export `DeleteSession(ctx context.Context, home, id string) error`.

- [ ] Read OpenCode's current storage/discovery rules. Reconfirm `opencode session delete <id>` against the installed CLI and inspect SQLite or other storage it leaves behind.
- [ ] Write failing isolated-home tests for exact ID, neighboring session, invalid ID, active/unverified status, CLI failure, and a remaining database entry.
- [ ] Use scoped local SQLite transactions for both collector-supported layouts. Investigation of OpenCode 1.18.34 found startup migrations that mutate neighboring sessions, so do not invoke that command for deletion. Remove only proven session-specific residue; verify discovery/resume fails for the target and other sessions remain intact.
- [ ] Run focused Go tests; inspect the PR-size and commit gates; open the OpenCode-only PR for independent review.

## Task C: Claude Code deletion PR

**Files:** Add `collector/internal/vendors/claude/delete.go` and `delete_test.go`; use existing Claude discovery and metadata files as needed.

**Interface:** `DeleteSession(ctx context.Context, home, id string) error`.

- [ ] Reproduce with a disposable normal and Desktop-origin session. Desktop Delete removed only its metadata in the investigation; `claude rm` was specific to background sessions and is not the normal-session delete mechanism.
- [ ] Write failing tests for the project transcript, session-specific project directory with subagents/tool output, session environment, file history, Desktop metadata where present, neighboring UUID, malformed UUID, and read/resume after deletion. Check shared prompt history for session-specific references without deleting other entries.
- [ ] Discover matching paths by exact ID under known Claude roots. Validate every path stays under its expected root, remove only matching files/directories, update shared indexes only when an exact session record can be identified, and report any failed removal. Verify discovery/resume no longer resolves the session.
- [ ] Run focused Go tests; inspect the PR-size and commit gates; open the Claude-only PR. Reviewer checks Desktop/CLI representations, child data, and a same-named unrelated directory.

## Task D: Cursor CLI deletion PR

**Files:** Add `collector/internal/vendors/cursor/delete.go` and `delete_test.go`; reuse Cursor transcript and CLI chat discovery code.

**Interface:** Export `DeleteSession(ctx context.Context, home, id string) error` for CLI-only sessions. Refuse sessions with IDE database references until Task E extends this function.

- [ ] Create two disposable Cursor CLI sessions. Confirm the exact transcript/subagent directory and `~/.cursor/chats/*/<id>/store.db` layout used by this collector.
- [ ] Write failing tests for exact ID, neighboring session, child transcripts, active/unverified session, an IDE reference to the same ID, and failure after a partial file operation.
- [ ] Resolve and validate exact paths under known roots, refuse IDE-referenced sessions, remove the CLI store and transcript family, and verify the CLI cannot resume the target after restart.
- [ ] Run focused Go tests and a disposable Cursor CLI smoke test; inspect the PR-size and commit gates; open the CLI-only PR for independent review.

## Task E: Cursor IDE deletion PR

**Files:** Extend `collector/internal/vendors/cursor/delete.go` and `delete_test.go`; reuse existing Cursor SQLite metadata code. Add a focused SQL helper only if needed for more than one IDE database.

**Interface:** Extend `cursor.DeleteSession(ctx, home, id)` from Task D to support IDE-referenced sessions as well as CLI-only sessions.

- [ ] Create disposable Cursor IDE sessions beside another chat; inventory `globalStorage/state.vscdb`, `conversation-search.db`, workspace `state.vscdb`, transcript directories, and any CLI store for the same ID.
- [ ] Write failing tests for `composerData:<id>`, `bubbleId:<id>:*`, session-linked `agentKv`/search/workspace rows, neighboring rows, lock/WAL behavior, and unknown database layouts.
- [ ] Before writing, build an exact-ID artifact inventory and refuse active or unverifiable sessions. Apply scoped SQLite transactions, remove only target files, and verify target rows and resume paths are absent. Do not delete a whole database or make a persistent backup containing the target.
- [ ] Run focused Go tests and a disposable real Cursor IDE restart/resume check; inspect the PR-size and commit gates; open the IDE-only PR. If proof is incomplete, block this PR rather than reporting Cursor support.

## Task F: Table Delete UI PR

**Files:** Modify `frontend/src/pages/coslash/CoslashPage.tsx`, `components/CoslashLayout.tsx`, and focused tests; add one shared confirmation component only if needed. Use `lib/api.ts`.

**Interface:** Consume the fixed `DELETE /api/sessions` contract. Expose an attempt-scoped `onDelete(session: Session)` action for Task G to reuse. Identify sessions with `sessionKey`.

- [ ] Write failing table tests: Delete appears only on local rows, confirmation names the exact session, cancel makes no request, active/409 error remains visible, one in-flight request cannot double-submit, and stale completion cannot change a new selection.
- [ ] Add Delete to the local row ellipsis. Reuse the existing dialog/menu primitives; confirm before `apiFetch`, show pending state and `role="alert"` error, and keep the row visible on failure.
- [ ] On `204`, refresh sessions and clear or move inspector selection only if it still points to the deleted composite identity. On any other response, render the safe API message and allow a deliberate retry after the cause is fixed.
- [ ] Run `npm run format`, `npm run lint`, `npm test`, `npm run build`, and `npm run format:check` in `frontend/`; inspect the PR-size and commit gates; open the table-only PR for independent review.

## Task G: Board Delete UI PR

**Files:** Modify `frontend/src/pages/coslash/components/SessionBoard.tsx`, its focused tests, and the minimum prop wiring in `CoslashLayout.tsx`.

**Interface:** Reuse Task F's `onDelete(session: Session)` and confirmation state; do not add a second request implementation.

- [ ] Write failing tests: Delete is available on a local board card only, a remote card has no Delete action, clicking it does not select the card, and focus returns after confirmation or cancellation at narrow width.
- [ ] Add the card action and pass the exact session to Task F's shared delete flow. Keep table and board error/pending behavior identical.
- [ ] Run `npm run format`, `npm run lint`, `npm test`, `npm run build`, and `npm run format:check` in `frontend/`; inspect the PR-size and commit gates; open the board-only PR for independent review.

## Task H: Local endpoint PR

**Files:** Add `collector/cmd/coslash/api_delete.go` and focused tests; modify route registration in `collector/cmd/coslash/main.go` and retention wording in `docs/data-and-privacy.md` if affected. Add the smallest dispatcher in `collector/internal/collector/` only if existing agent selection cannot hold the switch.

**Interface:** Produce the fixed HTTP contract; call the reviewed Codex, OpenCode, Claude, and Cursor adapters from Tasks A-E with this machine's home and exact ID.

- [ ] Write failing HTTP tests through `httpsec.Guard`: missing token, wrong method, invalid source/agent/ID, forged remote source with the same UUID, missing session, session that becomes active after listing, status that cannot be verified, adapter failure, and successful deletion. Assert the adapter is never called for a remote source or before authorization and liveness pass.
- [ ] Require `source=local`, validate the exact agent/id and dispatch by the existing vendor names. Let the adapter prove current local ownership and recheck liveness immediately before the first write; a missing main transcript must not prevent retrying a proven partial-cleanup journal or residue. Treat uncertain status as `session_unverified`; never infer safety from an old board snapshot.
- [ ] Return `204` only after adapter verification; log bounded causes and return fixed safe JSON errors. Invalidate local derived caches or refresh so `/api/sessions` and exact detail cannot still serve the removed transcript; keep unrelated session caches intact.
- [ ] Run focused Go tests, `make check`, and `make test` in `collector/`; inspect the PR-size and commit gates; open the endpoint-only PR. Reviewer checks guard coverage, time-of-check races, partial failure, and stale cached detail.

## Final integration gate (no catchall PR)

**Files:** Only repair files implicated by a failed end-to-end check. Update any remaining user-facing help in the PR that owns the behavior.

- [ ] Rebase the reviewed stack as needed and keep each PR's own delta under the review budget. Re-read `git status`, `git diff --stat`, and `git diff --numstat`; preserve unrelated changes. Put any needed repair into the PR that owns the behavior, not a large integration PR.
- [ ] From this worktree, launch the built coSlash app and Superset browser. On disposable local sessions, confirm both table and board delete flows; active refusal; session disappears after refresh and app restart; vendor CLI/IDE cannot resume; neighboring session remains readable. Confirm remote sessions have no Delete action and forged remote requests are refused. Record exact OS/vendor combinations actually exercised.
- [ ] Inspect browser screenshots at desktop and narrow widths plus console errors. Fix visible focus, overflow, or dialog defects in the owning PR. Run `make release && make smoke`, collector checks, and frontend lint/test/build/format checks after integration edits.
- [ ] Check every PR has its independent review and at most one fix/recheck round. Publish no completion claim for a vendor/platform without the required proof; list blocked matrix cells explicitly.
