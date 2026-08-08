# Task 01 — Freeze Plugin Contracts and Create the Skeleton

## Objective

Create the new compile-time plugin directories and frozen interfaces that allow backend and frontend work to proceed independently.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/01.js`](../task-status/01.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

## Context

coSlash has no runtime feature-plugin framework. This task creates a Canvas-specific compile-time boundary, not a generic loader. Existing coSlash integration files remain master-owned.

## Prerequisites

- Current coSlash main checkout.
- Read `CONTRACTS.md` and `FILE_OWNERSHIP.md`.

## Owned paths

- `collector/internal/plugins/canvas/plugin.go`.
- `collector/internal/plugins/canvas/contracts/`.
- Empty plugin subdirectory package documentation/skeletons.
- `frontend/src/plugins/canvas/index.tsx` and typed contract files.
- Do not edit existing coSlash files, dependency manifests, or central monitoring documents.

## Work

1. Define backend lifecycle and dependency interfaces with no functional side effects.
2. Define frontend destination, renderer, session action, and settings/diagnostic entry types.
3. Define stable error, terminal, workspace, board, and run contract fixtures.
4. Create package boundaries for shared runtime, Session Canvas, DaGama, Atlas, persistence, and migration.
5. Ensure product-specific code cannot import Vite server modules.
6. Document any contract ambiguity for the master; do not guess and silently freeze it.

## Tests

```sh
cd collector
go test ./internal/plugins/canvas/...

cd ../frontend
npx tsc -b
```

The empty plugin must compile without being registered in coSlash yet.

## Exit gate

- Contracts match `CONTRACTS.md`.
- New packages compile.
- No existing coSlash file changed.
- Tasks 02–08 can branch from the result.

## Report back

```markdown
Task: 01 Plugin contracts
Status: complete | partial | blocked
Branch/base/result SHA:
New contract files:
Tests and results:
Ambiguities resolved:
Contract deviations requested:
New issues/risks:
Recommended tasks now unblocked:
```

Return this to the master; do not edit `STATUS.md`, `REPORTS.md`, `ISSUES.md`, or `DECISIONS.md`.
