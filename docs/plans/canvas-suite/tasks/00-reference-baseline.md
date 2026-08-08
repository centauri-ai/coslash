# Task 00 — Archive and Characterize the Legacy Reference

## Objective

Verify the master-created remote archive for Fleetlog commit `c13a3ef01438193dcdcd2e387300e69ae3c27437` and convert its intended behavior into sanitized fixtures and visual evidence before translation begins.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/00.js`](../task-status/00.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

## Context

The commit is one local commit ahead of the remote branch. Its tests mostly pass, but the combined WIP has 12 TypeScript build errors and emitted `EMFILE` watcher errors. It is a behavioral reference, not code that can be merged into coSlash.

## Prerequisites

- Access to the legacy Fleetlog checkout and its exact source SHA.
- A non-force-pushed remote archive ref created by the master agent, recorded in `STATUS.md`, and verified to resolve to the exact source SHA.
- Read `MASTER_PLAN.md`, `ACCEPTANCE.md`, and this task.

## Owned outputs

- A legacy stabilization branch assigned by the master; the worker never creates or publishes the archive ref.
- `docs/plans/canvas-suite/fixtures/`.
- Future coSlash `collector/internal/plugins/canvas/testdata/legacy/` fixture payloads.
- No changes to production coSlash code.

## Work

1. Verify that the master-created remote archive ref resolves to the exact source SHA; stop and report the missing prerequisite if it does not.
2. Create a separate stabilization branch; do not modify the archive.
3. Classify and minimally repair the 12 build errors, documenting every semantic change.
4. Instrument watcher creation/close paths and determine whether `EMFILE` is a leak or parallel-test pressure.
5. Run targeted Canvas, DaGama, and Atlas tests and manual representative flows.
6. Export sanitized boards, events, run states, prompts, artifacts, committee attempts, and interrupted states.
7. Capture light/dark screenshots for empty, editing, running, gate, failure, and completed surfaces.

## Tests

```sh
cd frontend
npm run lint
npm run format:check
npm test
npm run build
```

Also run controller tests repeatedly with watcher counts before/after. Never work around the failure by raising limits without explaining resource ownership.

## Exit gate

- The master-created remote archive ref is recorded and independently verified to resolve to the exact source SHA.
- All build/test failures are classified.
- Fixtures contain no credentials, private prompts, repository secrets, or user-identifying paths.
- Visual and behavioral evidence is indexed for downstream tasks.

## Report back

Do not edit central monitoring files. Return this block to the master, who updates `STATUS.md`, appends it to `REPORTS.md`, and records issues/decisions:

```markdown
Task: 00 Reference baseline
Status: complete | partial | blocked
Branch/base/result SHA:
Archive location:
Build/test results:
Fixtures and visual evidence produced:
Watcher conclusion:
Behavioral fixes made on stabilization branch:
New issues/risks:
Decisions requested:
Recommended tasks now unblocked:
```
