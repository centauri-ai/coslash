# Task 09 — Session Canvas Backend and Actions

## Objective

Assemble Session Canvas backend routes from the shared projection, persistence, terminal, and execution services.

## Local review outcome

Complete at 2026-08-09T04:05:05Z. Independent review fixed the production terminal-name and reconnect defects in `8d05d8c6954e5cf10072f5bf6eb1138968040a18`; the result is locally merged into `hlu/canvas-migration` at `88701fac438e1ca8343bdf6c23367420f6efe27e`.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/09.js`](../task-status/09.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

- 2026-08-09T02:54:10Z — `codex-worker-task-09` claimed Task 09 from exact base `01aa158ecc322b3dcf4b71e46d278944147ca7b6` in `/private/tmp/coslash-canvas-task-09`.
- 2026-08-09T03:08:51Z — Moved to review at `558a6e33284e36849bc516f6a0eb1e4c0152da3f`; package, Canvas, full collector race, vet, coverage, ownership, and ancestry gates passed.
- 2026-08-09T04:05:05Z — Independent review found that NUL-delimited composite identities made `terminal.Name` reject all production Session and experiment terminals. Fix `8d05d8c` now uses deterministic JSON-encoded identity input, adopts preserved tmux sessions after collector restart, and recreates exited entries. Repeated package race and post-merge Canvas race/full collector race/vet gates passed; locally merged at `88701fa` and marked complete.

## Independent review report

- Reviewer: `codex-root`
- Outcome: approved and locally merged
- Base/result/integration: `01aa158` / `8d05d8c` / `88701fa`
- Changed files: eleven Task 09 files under `collector/internal/plugins/canvas/sessioncanvas/`; review fixes touched `handler.go`, `handler_test.go`, and `types.go`.
- Tests: `go test -race -count=3 ./internal/plugins/canvas/sessioncanvas/...`, `go test -race ./internal/plugins/canvas/...`, `go test -race ./...`, `go vet ./...`, formatting/diff/ancestry/clean-worktree audits — all passed.
- Resolved issues: invalid production tmux identities; missing preserved-tmux adoption; exited registry entries returned indefinitely instead of restarting.
- Contract deviations: none.
- Remaining integration follow-up: master-owned construction/registration of `sessioncanvas.Runtime`; Task 18 retains live CLI/tmux validation.

## Dependencies

- Tasks 02, 04, 06, and 08 merged.

## Owned paths

- New Session Canvas handler/service files under `collector/internal/plugins/canvas/` assigned by the master.
- Do not modify shared package implementations or core routes.

## Work

- Register composite session detail, rename, fork, turn analysis, scoped file read/render, workspace, and terminal-creation handlers.
- Resolve agent/vendor/cwd from server-known session data; do not trust duplicates in request bodies.
- Implement same-vendor experiment fork and explicit unsupported/failure results.
- Invoke configured coSlash Claude/Codex CLI settings for structured turn analysis with bounded input/output and cache keys.
- Scope file access beneath the known session cwd; require regular files, supported types, and size caps.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/...
```

Add authenticated handler tests for methods, bodies, unknown vendor/session, duplicate IDs across vendors, vanished cwd, rename validation, prompt limits, synthesis disabled/failure, file traversal/symlinks/HTML, terminal reuse, and safe errors.

## Exit gate

- Canvas backend contract is complete without frontend assumptions.
- All routes work through a guarded test server.
- No arbitrary command or path reaches an effect.

## Report back

```markdown
Task: 09 Session Canvas backend
Status: complete | partial | blocked
Branch/base/result SHA:
Routes delivered:
Tests and security cases:
Performance observations:
Contract deviations:
New issues/risks:
Integration notes for task 10:
```
