# Task 03 — Safe Filesystem and Durable Event Store

## Objective

Implement the shared filesystem and event-log foundation used by DaGama, Atlas, persistence, and migration.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/03.js`](../task-status/03.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

## Dependencies

- Task 01 merged.

## Owned paths

- `collector/internal/plugins/canvas/runfs/` only.

## Required behavior

- Canonical scoped paths and symlink refusal.
- Atomic temp-write, chmod, sync, rename, and directory fsync.
- Append-only JSONL events with intent-before-effect ordering.
- Torn final-line detection/recovery without hiding mid-log corruption.
- Exclusive monotonically increasing sequence allocation.
- Bounded reads and explicit file modes.
- No recursive deletion API accepting unresolved variables, globs, or broad roots.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/runfs/...
go vet ./internal/plugins/canvas/runfs/...
```

Cover concurrent writers, crashes between write/sync/rename, traversal, symlinked parents/files, oversized input, torn tail, corrupted middle events, and cancellation.

## Exit gate

- Race tests pass.
- APIs are usable without DaGama/Atlas imports.
- No shell commands implement filesystem operations.

## Report back

```markdown
Task: 03 RunFS/event store
Status: complete | partial | blocked
Branch/base/result SHA:
APIs delivered:
Failure and crash semantics:
Tests and race results:
Files changed:
Contract deviations:
New issues/risks:
Recommended consumers now unblocked:
```

Return it to the master for central recording.
