# Task 03 — Safe Filesystem and Durable Event Store

## Objective

Implement the shared filesystem and event-log foundation used by DaGama, Atlas, persistence, and migration.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted with review fixes and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/03.js`](../task-status/03.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

- 2026-08-08T21:48:56Z — `codex-root-task-03` claimed and started Task 03 on branch `codex/canvas-task-03-runfs-eventstore`, worktree `/Users/helu/code/product/coslash-task-03`, exact reviewed Task 01 base `477c66303864d16b11c9ea99a7abd842d49d1d3c`. Operator explicitly authorized moving forward from review. Current focus: scoped paths, atomic writes, and durable append-only event storage with crash/corruption coverage.
- 2026-08-08T21:53:46Z — Implemented scoped filesystem and event-log primitives plus traversal, symlink, atomic crash-stage, bounds, modes, concurrency, corruption, torn-tail, and cancellation coverage. `cd collector && go test ./internal/plugins/canvas/runfs/...` passed. Next: required race/vet gates, API/diff audit, and proportionate collector regressions.
- 2026-08-08T21:55:33Z — Required `go test -race -count=3 ./internal/plugins/canvas/runfs/...` and targeted vet passed; full Canvas and collector test/vet regressions passed; coverage is 77.5%; owned-path and forbidden-shell/deletion scans are clean. Next: commit the review candidate and perform result-SHA verification.
- 2026-08-08T21:56:24Z — Moved to review at `cb8485eb33cdc96b3f3e9738b7bd34b4d55bcf79`. Post-commit race, targeted vet, full collector tests, clean-worktree, ancestry, diff-check, and owned-path verification pass. Next: master review, central report mirroring, Task 01-first merge ordering, then Task 03 merge/completion.
- 2026-08-08T22:01:21Z — Review returned Task 03 to implementation after finding two issues: concurrent first use of different logs under one parent can race on directory creation, and the process-lock registry retains each path indefinitely. Next: add regression tests, fix both within `runfs/`, and rerun required gates.
- 2026-08-08T22:04:13Z — Fixed both findings plus two adjacent edge cases: zero clocks can no longer serialize self-corrupting events, and chmod now occurs only after opened-file identity validation. Ten shuffled race runs, twenty focused concurrency/resource repetitions, and full collector test/vet gates pass. Next: commit fixes and verify the new result SHA.
- 2026-08-08T22:04:59Z — Moved back to review at `685540299b233290128115fde7e6e700f5c519eb`. Post-commit race x3, targeted/full vet, full collector tests, clean-worktree, ancestry, diff-check, and owned-path verification pass. Task 05 started from prior result `cb8485e` and must incorporate `6855402` before integration.

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

## Worker report — review — updated 2026-08-08T22:04:59Z

```yaml
task_id: "03"
task_title: "RunFS/event store"
final_state: review
agent: { id: "codex-root-task-03", runtime: "Codex coding agent" }
timing:
  claimed_at_utc: "2026-08-08T21:48:56Z"
  started_at_utc: "2026-08-08T21:48:56Z"
  finished_at_utc: "2026-08-08T22:04:59Z"
  reported_at_utc: "2026-08-08T22:04:59Z"
git:
  branch: "codex/canvas-task-03-runfs-eventstore"
  worktree: "/Users/helu/code/product/coslash-task-03"
  base_sha: "477c66303864d16b11c9ea99a7abd842d49d1d3c"
  result_sha: "685540299b233290128115fde7e6e700f5c519eb"
summary: "Delivered and reviewed the shared bounded filesystem and durable event-log foundation entirely within Task 03 ownership, including concurrency and resource-lifecycle fixes."
delivered:
  - "Canonical Scope rooted through os.Root with lexical traversal/glob/variable rejection and explicit lstat symlink refusal."
  - "Private-mode directory creation, bounded regular-file reads, and durable temp-write/chmod/fsync/rename/directory-fsync replacement."
  - "Workflow-neutral JSONL EventLog with intent-before-effect API, gapless sequence allocation, bounded records/logs, OS and process exclusion, and cancellation."
  - "Torn unterminated-tail detection/recovery that never hides malformed durable lines or sequence gaps."
  - "Race-safe idempotent shared-parent creation and reference-counted process locks that are reclaimed after use/cancellation."
  - "Zero-clock rejection and post-identity-validation chmod ordering to avoid self-corrupting records and premature target mutation."
changed_files:
  - "collector/internal/plugins/canvas/runfs/doc.go"
  - "collector/internal/plugins/canvas/runfs/errors.go"
  - "collector/internal/plugins/canvas/runfs/scope.go"
  - "collector/internal/plugins/canvas/runfs/eventlog.go"
  - "collector/internal/plugins/canvas/runfs/lock_unix.go"
  - "collector/internal/plugins/canvas/runfs/lock_other.go"
  - "collector/internal/plugins/canvas/runfs/scope_test.go"
  - "collector/internal/plugins/canvas/runfs/eventlog_test.go"
failure_and_crash_semantics:
  - "Before rename, atomic-write failure leaves the previous file visible and removes the temporary file."
  - "After rename, the complete replacement may be visible even if the following directory sync reports failure; partial content is never exposed."
  - "An event is durable only after its newline and file sync; an unterminated tail is reported and truncated before the next append."
  - "Malformed newline-terminated JSON, invalid envelopes, blank durable lines, and sequence gaps return CorruptionError at the first bad line."
acceptance_gates:
  - { gate: "Race tests pass", result: passed, evidence: "go test -race -count=3 ./internal/plugins/canvas/runfs/..." }
  - { gate: "APIs usable without DaGama/Atlas imports", result: passed, evidence: "generic runfs package and full Canvas subtree compile/test pass" }
  - { gate: "No shell filesystem operations", result: passed, evidence: "owned-path scan contains no exec.Command, shell invocation, or recursive deletion API" }
tests:
  - { command: "cd collector && go test ./internal/plugins/canvas/runfs/...", result: passed, evidence: "all scoped filesystem and event-log unit tests" }
  - { command: "cd collector && go test -race -count=3 ./internal/plugins/canvas/runfs/...", result: passed, evidence: "three repeated race runs including 64 concurrent writers" }
  - { command: "cd collector && go vet ./internal/plugins/canvas/runfs/...", result: passed, evidence: "no findings" }
  - { command: "cd collector && go test ./internal/plugins/canvas/...", result: passed, evidence: "all plugin packages pass/compile" }
  - { command: "cd collector && go test ./...", result: passed, evidence: "full collector regression suite" }
  - { command: "cd collector && go vet ./...", result: passed, evidence: "no findings" }
  - { command: "cd collector && go test -cover ./internal/plugins/canvas/runfs/...", result: passed, evidence: "77.5% statement coverage" }
  - { command: "go test -race -shuffle=on -count=10 ./internal/plugins/canvas/runfs/...", result: passed, evidence: "review-fix stress gate" }
  - { command: "focused concurrent mkdir/lock reclamation race tests x20", result: passed, evidence: "all repetitions pass" }
  - { command: "post-fix commit race x3, targeted/full vet, and full go test", result: passed, evidence: "result SHA 685540299b233290128115fde7e6e700f5c519eb" }
decisions:
  - "Use os.Root for containment plus explicit lstat/SameFile checks to refuse symlinks rather than merely containing their targets."
  - "Use a keyed process lock plus flock on supported Unix targets so separate EventLog instances and processes allocate sequences exclusively."
contract_deviations: []
issues:
  - { id: null, severity: P2, status: open, summary: "Task 05 base cb8485e predates Task 03 fixes", impact: "Task 05 integration would omit race/resource corrections", owner: "master/task-05", recommendation: "Rebase or merge 6855402 before integration" }
  - { id: null, severity: P3, status: known, summary: "Non-Unix fallback is process-local only", impact: "Cross-process writers on unsupported targets need a platform lock before release", owner: "master/task-18", recommendation: "Confirm release targets or add a platform-native lock" }
blockers: []
post_implementation:
  remaining_work:
    - "Master review and dependency-ordered merge after Task 01."
    - "Update Task 05 from cb8485e to include fix commit 6855402 before integration."
  improvements:
    - "Add hostile symlink-swap and actual process-crash stress during Task 18."
  known_issues:
    - "Task 01 is still review-only in shared records; this branch is based directly on its exact reviewed result."
    - "Non-Unix platforms use process-local locking."
  follow_up_tasks:
    - "Task 05: incorporate 6855402 and use AppendIntent before Git/publication effects."
    - "Task 08: use AtomicWrite/EventLog for revisioned persistence."
rollback_notes:
  - "The result is isolated to collector/internal/plugins/canvas/runfs/; omit the branch or revert cb8485e and 6855402 in reverse order."
next_task_recommendations: ["05", "08"]
central_updates_requested: { status: true, reports: true, issues: true, decisions: true }
```
