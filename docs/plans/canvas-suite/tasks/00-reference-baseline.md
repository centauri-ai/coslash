# Task 00 — Archive and Characterize the Legacy Reference

## Objective

Make Fleetlog commit `c13a3ef01438193dcdcd2e387300e69ae3c27437` recoverable and convert its intended behavior into sanitized fixtures and visual evidence before translation begins.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted locally at `b20c698369b91b9bb11a928722e64d3c776a3f8b` as the frozen Fleetlog reference. It was not merged into coSlash because the repositories have unrelated histories; downstream dependency use is approved.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/00.js`](../task-status/00.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

- 2026-08-08T21:17:20Z — `codex-root-task-00` claimed and started Task 00 on branch `codex/canvas-task-00-reference-baseline`, worktree `/Users/helu/code/product/fleetlog-canvas-task-00`, base `c13a3ef01438193dcdcd2e387300e69ae3c27437`. Created local archive ref `archive/canvas-legacy-c13a3ef`. Current focus: characterize build failures and watcher ownership, then produce sanitized fixture and visual evidence.
- 2026-08-08T21:31:19Z — Committed result `1d1f8da96a089f53382dabc9ab58ebcd9e9e00d6`: repaired all 12 TypeScript build errors, added sanitized Claude/Codex/DaGama/Atlas/event/artifact/interrupted-run fixtures, added four fixture contract tests, and documented watcher ownership. Full result: 63 files and 827 tests pass; build and lint pass; fixture secret scan is clean; five low-FD controller/reconciliation repetitions pass.
- 2026-08-08T21:31:19Z — Blocked. The exact baseline has local archive ref `archive/canvas-legacy-c13a3ef`, but worker pushes are prohibited and no remote ref is verified. The browser skill reported no available browser, so the required sanitized light/dark visual matrix cannot be captured. Unlock: master publishes the archive ref without force and connects an in-app Browser or Chrome instance; then capture the matrix and rerun validation.
- 2026-08-08T21:37:31Z — Operator reclassified remote archive publication and visual capture as non-blocking post-implementation follow-ups. Committed that classification at `b20c698369b91b9bb11a928722e64d3c776a3f8b` and moved Task 00 to review. Validated fixtures may unblock downstream implementation after master review/merge; Task 18 retains final archive and visual-parity verification.

## Context

The commit is one local commit ahead of the remote branch. Its tests mostly pass, but the combined WIP has 12 TypeScript build errors and emitted `EMFILE` watcher errors. It is a behavioral reference, not code that can be merged into coSlash.

## Prerequisites

- Access to the legacy Fleetlog checkout and its exact source SHA.
- An archive branch approved by the master agent.
- Read `MASTER_PLAN.md`, `ACCEPTANCE.md`, and this task.

## Owned outputs

- Legacy archive/stabilization branches assigned by the master.
- `docs/plans/canvas-suite/fixtures/`.
- Future coSlash `collector/internal/plugins/canvas/testdata/legacy/` fixture payloads.
- No changes to production coSlash code.

## Work

1. Archive the exact commit without force-updating an existing branch.
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

- Source SHA is recoverable remotely.
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

## Worker report — review — updated 2026-08-08T21:37:31Z

```yaml
task_id: "00"
task_title: "Reference baseline"
final_state: review
agent: { id: "codex-root-task-00", runtime: "Codex coding agent" }
timing:
  claimed_at_utc: "2026-08-08T21:17:20Z"
  started_at_utc: "2026-08-08T21:17:20Z"
  finished_at_utc: "2026-08-08T21:37:31Z"
  reported_at_utc: "2026-08-08T21:37:31Z"
git:
  branch: "codex/canvas-task-00-reference-baseline"
  worktree: "/Users/helu/code/product/fleetlog-canvas-task-00"
  base_sha: "c13a3ef01438193dcdcd2e387300e69ae3c27437"
  result_sha: "b20c698369b91b9bb11a928722e64d3c776a3f8b"
  local_archive_ref: "archive/canvas-legacy-c13a3ef"
summary: "All code, fixture, test, and watcher characterization work is committed and ready for review; remote archival and browser screenshots are explicit non-blocking post-implementation follow-ups."
delivered:
  - "12 TypeScript build-error repairs with semantic notes."
  - "Sanitized composite Claude/Codex session fixtures."
  - "DaGama board and complete lifecycle events."
  - "Atlas v1/v2 boards and attributed committee events."
  - "Synthetic artifacts and interrupted_migration fixtures."
  - "Executable fixture validation and watcher characterization."
changed_files:
  - "docs/plans/canvas-suite/fixtures/**"
  - "frontend/vite/legacy-canvas-fixtures.test.ts"
  - "frontend/src/pages/fleetlog/components/AtlasCanvas.tsx"
  - "frontend/src/pages/fleetlog/components/atlas/CommitteeStatusPane.tsx"
  - "frontend/src/pages/fleetlog/components/atlas/SeatInfoPane.tsx"
  - "frontend/src/pages/fleetlog/lib/atlas-board.ts"
  - "frontend/vite/atlas/board-policy.ts"
  - "frontend/vite/atlas/pipeline.test.ts"
  - "frontend/vite/atlas/runs.ts"
  - "frontend/vite/dagama/runs.ts"
acceptance_gates:
  - { gate: "Exact source SHA has a non-force local archive ref", result: passed, evidence: "archive/canvas-legacy-c13a3ef resolves to c13a3ef01438193dcdcd2e387300e69ae3c27437" }
  - { gate: "Source SHA recoverable remotely", result: deferred, evidence: "Local immutable ref exists; master-owned remote publication is tracked for Task 18/final sign-off and does not block fixture consumers" }
  - { gate: "Build/test failures classified", result: passed, evidence: "fixtures/characterization.md" }
  - { gate: "Fixtures sanitized", result: passed, evidence: "10 JSON/JSONL files parse; secret scan has no matches" }
  - { gate: "Visual evidence indexed", result: deferred, evidence: "fixtures/visual/README.md records the full matrix; capture awaits a browser and is retained for Task 18 parity sign-off" }
tests:
  - { command: "npm run lint", result: passed, evidence: "Exit 0; eight warnings" }
  - { command: "npm run format:check", result: failed, evidence: "90 pre-existing files remain; baseline was 97" }
  - { command: "npm test", result: passed, evidence: "63 files, 827 tests" }
  - { command: "npm run build", result: passed, evidence: "TypeScript and Vite build succeeded" }
  - { command: "ulimit -n 256; npm test", result: passed, evidence: "823 baseline tests; no EMFILE" }
  - { command: "ulimit -n 64; npm test", result: passed, evidence: "823 baseline tests; no EMFILE" }
  - { command: "five repeated low-FD controller/pipeline/reconcile runs", result: passed, evidence: "235 test executions; no EMFILE or lingering process" }
  - { command: "npx vitest run vite/legacy-canvas-fixtures.test.ts", result: passed, evidence: "4 tests post-commit" }
decisions:
  - "Treat EMFILE as parallel evaluation pressure on current evidence, not a reproduced per-attempt leak."
  - "Keep the repository-wide formatting backlog documented rather than rewriting unrelated legacy files."
contract_deviations: []
issues:
  - { id: null, severity: P2, status: deferred, summary: "Remote archive publication pending", impact: "No impact on local fixture consumers; required before final sign-off", owner: master, recommendation: "Publish the local archive ref without force" }
  - { id: null, severity: P2, status: deferred, summary: "Visual matrix capture pending", impact: "No impact on backend/runtime work; final UI parity evidence remains", owner: master/operator, recommendation: "Connect Browser or Chrome and capture only sanitized fixtures" }
  - { id: null, severity: P2, status: open, summary: "Legacy format backlog", impact: "format:check remains red on 90 files", owner: legacy-stabilization, recommendation: "Handle separately if required" }
blockers: []
post_implementation:
  remaining_work:
    - "Capture Session/DaGama/Atlas light/dark empty, editing, running, gate, failure, and completed states."
    - "Verify the remote archive ref."
  improvements:
    - "Repeat lifecycle accounting in the assembled Go implementation during Task 18."
  known_issues:
    - "Legacy format:check reports 90 files."
  follow_up_tasks:
    - "Task 18: verify remote archival and complete the sanitized visual-parity matrix."
rollback_notes:
  - "The stabilization result is isolated on its task branch; deleting neither the local archive nor source data is required."
next_task_recommendations: ["06", "07", "11", "14"]
central_updates_requested: { status: true, reports: true, issues: true, decisions: true }
```
