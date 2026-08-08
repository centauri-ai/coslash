# Task Status Automation Contract

## Purpose

The dashboard at `migration-control.html` loads one machine-readable status file per task from `task-status/NN.js`. Coding agents update their selected task's status file and task brief during normal work. The human operator does not re-enter agent progress in the dashboard.

Browser `localStorage` is only an optional human override. Shared coordination truth is the newest task status file, its matching task brief record, and Git/worktree evidence.

## Exclusive ownership

- A worker may edit only `task-status/NN.js` and `tasks/NN-*.md` for the task it has atomically claimed.
- These records live in the absolute shared plan root `/Users/helu/code/product/coslash/docs/plans/canvas-suite`, even when implementation runs in a separate coSlash worktree. This keeps concurrent agent status visible to the one dashboard immediately.
- The master agent owns `STATUS.md`, `REPORTS.md`, `ISSUES.md`, and `DECISIONS.md` and mirrors accepted worker reports there.
- No agent may edit another task's status or report file.
- Re-read the sidecar immediately before each update. If its agent or state changed, stop and report a claim conflict.

## Required state transitions

```text
untouched -> claimed -> in_progress -> review -> complete
                         |      ^          |
                         v      |          v
                       blocked  +-- changes_requested

untouched | claimed | in_progress -> deferred
```

- `untouched`: never claimed. Dashboard derives `ready` or `waiting` from dependencies.
- `claimed`: agent and isolated branch/worktree reserved; implementation has not begun.
- `in_progress`: implementation or verification is actively running.
- `blocked`: work stopped and an exact external unlock condition is recorded.
- `review`: implementation stopped; result SHA and test evidence are ready for review.
- `changes_requested`: reviewer returned actionable work to the same owner.
- `complete`: reviewed, accepted, and merged into the dependency base.
- `deferred`: deliberately removed from the active schedule with a reason.

Only `complete` unlocks dependent tasks.

## Sidecar schema

Every `task-status/NN.js` assigns one record to `window.COSLASH_CANVAS_TASK_STATUS["NN"]`. Keep all fields present even when empty.

```javascript
{
  schemaVersion: 1,
  taskId: "NN",
  state: "untouched",
  agent: "",
  branch: "",
  worktree: "",
  baseSha: "",
  sha: "",
  reviewer: "",
  review: "pending",
  reason: "",
  notes: "",
  claimedAt: "",
  startedAt: "",
  completedAt: "",
  updatedAt: "YYYY-MM-DDTHH:MM:SSZ",
  progress: [],
  tests: [],
  issues: [],
  postImplementation: {
    remainingWork: [],
    improvements: [],
    knownIssues: [],
    followUps: [],
  },
}
```

Use UTC ISO-8601 timestamps. Never claim success without exact evidence. Never put secrets, credentials, private prompts, or raw terminal buffers in status files.

## Mandatory automatic updates

The assigned coding agent performs these updates without waiting for the human:

1. **Claim:** set `claimed`, identity, branch/worktree, base SHA, reason, `claimedAt`, and `updatedAt`; mirror the live record in the task brief.
2. **Start:** set `in_progress`, `startedAt`, current focus, next action, and `updatedAt`.
3. **Checkpoint:** after each material implementation unit or test run, append a progress entry with UTC time, changed files, exact test command/result/evidence, issues, decisions requested, and next action.
4. **Block:** set `blocked`, preserve completed work, and record blocker, owner, evidence, and exact unlock condition. Stop; do not take another task.
5. **Review handoff:** set `review`, result SHA, final tests, report-back data, remaining work, improvements, known issues, follow-ups, and `updatedAt`.
6. **Review outcome:** the reviewing/master agent sets `changes_requested` with actions or `complete` with reviewer, review time, merge/result SHA, and evidence.

Update the sidecar and task brief in the same patch whenever possible. The sidecar is the dashboard input; the task brief is the durable human report. The master mirrors accepted reports into central monitoring files.

## Dashboard behavior

- The HTML loads all 20 task sidecars directly under `file://` and checks them every 15 seconds by reloading when no form or prompt is active.
- A newer browser-local human override may temporarily win; a later agent sidecar timestamp automatically takes precedence.
- The dependency graph and recommendations recalculate from the newest loaded states.
- Exported dashboard JSON is a convenience snapshot, not the authoritative claim mechanism.
