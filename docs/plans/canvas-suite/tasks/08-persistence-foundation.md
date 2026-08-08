# Task 08 — Server-Backed Workspace Persistence

## Objective

Replace new browser-origin-bound Canvas state with safe, revisioned state under `~/.coslash`.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/08.js`](../task-status/08.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

## Dependencies

- Tasks 01 and 03.

## Owned paths

- `collector/internal/plugins/canvas/persistence/`.
- `frontend/src/plugins/canvas/api/persistence.ts` and related new tests.

## State covered

- Selected `{agent,id}` session.
- Canvas layout, locks, collapse state, zoom if persisted, pins, checkpoints, and experiment metadata.
- Atlas/DaGama unsaved drafts and recent project/board/run selections.
- Turn-analysis cache metadata/results.

## Required behavior

- Atomic revisioned GET/PUT contracts.
- Schema/version normalization and explicit corruption reporting.
- Debounced clients with generation counters so stale responses cannot overwrite newer edits.
- Size/count/age bounds for caches and checkpoints.
- No credentials or raw terminal streams.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/persistence/...

cd ../frontend
npm test -- src/plugins/canvas/api
```

Cover first write, conflicts, concurrent clients, stale completion, corrupt files, permission failures, quotas, pruning, and restart.

## Exit gate

- No new functional state depends exclusively on localStorage.
- APIs are ready for task 17 import.
- Failed persistence leaves the active UI usable and visibly unsaved.

## Report back

```markdown
Task: 08 Persistence foundation
Status: complete | partial | blocked
Branch/base/result SHA:
Schemas/APIs delivered:
Conflict and pruning policy:
Tests and results:
Contract deviations:
New issues/risks:
Recommended tasks now unblocked:
```
