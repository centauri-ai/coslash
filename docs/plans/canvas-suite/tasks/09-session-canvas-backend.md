# Task 09 — Session Canvas Backend and Actions

## Objective

Assemble Session Canvas backend routes from the shared projection, persistence, terminal, and execution services.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/09.js`](../task-status/09.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

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
