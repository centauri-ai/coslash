# Task 01 — Freeze Plugin Contracts and Create the Skeleton

## Objective

Create the new compile-time plugin directories and frozen interfaces that allow backend and frontend work to proceed independently.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/01.js`](../task-status/01.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

- 2026-08-08T21:14:51Z — `codex-root` claimed and started Task 01 on branch `codex/canvas-task-01-contracts`, worktree `/Users/helu/code/product/coslash-task-01`, base `89adab62f546bd0bbc4143aa69d04eb4ebb92d91`. Current focus: freeze backend/frontend contracts and compile-only plugin skeletons. Next: inspect target conventions and implement the owned paths.
- 2026-08-08T21:18:54Z — Implemented the no-side-effect backend lifecycle, composite session identity, stable error/terminal/revision envelopes and fixtures, package skeletons, and disabled typed frontend entry points. `cd collector && go test ./internal/plugins/canvas/...` passed. Next: compile the frontend contracts, audit the owned-path diff, and run proportionate regressions.
- 2026-08-08T21:23:08Z — Moved to review at result `477c66303864d16b11c9ea99a7abd842d49d1d3c`. Required Go and TypeScript checks plus targeted vet, lint, format, ancestry, and allowlist audits passed. Next: master review, WebSocket subprotocol-name confirmation, central report mirroring, and merge.

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

## Worker report — 2026-08-08T21:23:08Z

```yaml
task: "01 Plugin contracts"
status: review
agent: codex-root
branch: codex/canvas-task-01-contracts
worktree: /Users/helu/code/product/coslash-task-01
base_sha: 89adab62f546bd0bbc4143aa69d04eb4ebb92d91
result_sha: 477c66303864d16b11c9ea99a7abd842d49d1d3c
summary: >-
  Added a no-side-effect compile-time Canvas plugin, lifecycle and dependency
  interfaces, composite session identity, stable API/terminal/persistence/workflow
  envelopes, matching Go and TypeScript fixtures, downstream package boundaries,
  and disabled typed frontend entry components.
changed_files:
  - collector/internal/plugins/canvas/agentexec/doc.go
  - collector/internal/plugins/canvas/artifacts/doc.go
  - collector/internal/plugins/canvas/atlas/doc.go
  - collector/internal/plugins/canvas/contracts/contracts_test.go
  - collector/internal/plugins/canvas/contracts/doc.go
  - collector/internal/plugins/canvas/contracts/errors.go
  - collector/internal/plugins/canvas/contracts/identity.go
  - collector/internal/plugins/canvas/contracts/persistence.go
  - collector/internal/plugins/canvas/contracts/terminal.go
  - collector/internal/plugins/canvas/contracts/testdata/board.json
  - collector/internal/plugins/canvas/contracts/testdata/error.json
  - collector/internal/plugins/canvas/contracts/testdata/run.json
  - collector/internal/plugins/canvas/contracts/testdata/terminal-input.json
  - collector/internal/plugins/canvas/contracts/testdata/terminal-resize.json
  - collector/internal/plugins/canvas/contracts/testdata/workspace.json
  - collector/internal/plugins/canvas/contracts/workflow.go
  - collector/internal/plugins/canvas/dagama/doc.go
  - collector/internal/plugins/canvas/migration/doc.go
  - collector/internal/plugins/canvas/persistence/doc.go
  - collector/internal/plugins/canvas/plugin.go
  - collector/internal/plugins/canvas/plugin_test.go
  - collector/internal/plugins/canvas/publication/doc.go
  - collector/internal/plugins/canvas/revision/doc.go
  - collector/internal/plugins/canvas/runfs/doc.go
  - collector/internal/plugins/canvas/sessioncanvas/doc.go
  - collector/internal/plugins/canvas/sessiondetail/doc.go
  - collector/internal/plugins/canvas/terminal/doc.go
  - collector/internal/plugins/canvas/verification/doc.go
  - frontend/src/plugins/canvas/contracts.ts
  - frontend/src/plugins/canvas/fixtures.ts
  - frontend/src/plugins/canvas/index.tsx
tests:
  - command: "cd collector && go test ./internal/plugins/canvas/..."
    result: passed
    evidence: "All Canvas, contracts, and skeleton packages compiled on the result SHA."
  - command: "cd collector && go vet ./internal/plugins/canvas/..."
    result: passed
    evidence: "No findings."
  - command: "cd frontend && npx tsc -b"
    result: passed
    evidence: "No TypeScript errors."
  - command: "cd frontend && npm run lint -- src/plugins/canvas"
    result: passed
    evidence: "oxlint completed without warnings."
  - command: "cd frontend && npm run format:check -- src/plugins/canvas"
    result: passed
    evidence: "All checked files matched Prettier style."
  - command: "git diff --check 89adab62f546bd0bbc4143aa69d04eb4ebb92d91..477c66303864d16b11c9ea99a7abd842d49d1d3c"
    result: passed
    evidence: "No whitespace errors; every changed file is under the Task 01 allowlist."
ambiguities_resolved:
  - "Board and run product bodies remain owned by Tasks 11 and 14; Task 01 freezes only legacy-compatible common envelopes."
  - "All frontend destinations remain unready and render no UI until Task 07 and integration gates enable them."
decisions_requested:
  - "Confirm exact static and token-carrying terminal WebSocket subprotocol names before Task 02/04 integration; CONTRACTS.md freezes behavior but not string values."
contract_deviations: []
issues:
  - severity: P2
    summary: "npm ci reports one high-severity advisory in the existing lockfile; manifests and locks were outside Task 01 ownership."
    owner: master/task-18
  - severity: P3
    summary: "Validation host Node 23.5.0 is below package.json's >=24 engine; required checks passed."
    owner: master/environment
remaining_work:
  - "Master review, subprotocol-name confirmation, and merge into hlu/canvas-migration."
improvements:
  - "Generate cross-language fixture parity checks if the shared contract surface grows."
known_issues:
  - "Existing dependency advisory and validation-host Node engine mismatch described above."
follow_ups:
  - "Task 02: register the compile-time plugin and guarded terminal WebSocket hook."
  - "Tasks 03, 04, 06, 07, and 08: consume these boundaries after their dependency gates open."
rollback:
  - "Do not merge, or revert result commit 477c66303864d16b11c9ea99a7abd842d49d1d3c; no existing coSlash file or persisted state changed."
central_updates_requested:
  status: true
  reports: true
  issues: true
  decisions: true
```
