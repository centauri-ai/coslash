# Task 16 — Atlas Canvas Frontend

## Objective

Port Atlas's graph editor, committee controls, and run observation UI into the shared coSlash Canvas plugin shell.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/16.js`](../task-status/16.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "16"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Tasks 07 and 14; final integration also requires Task 15."
pickup_condition: "Tasks 07 and 14 are complete; Task 15 may still be active only if frozen controller fixtures are available."
agent:
  id: null
  runtime: null
  claimed_at_utc: null
  started_at_utc: null
  completed_at_utc: null
branch: null
worktree: null
base_sha: null
result_sha: null
dependencies:
  required: ["07", "14", "15"]
  satisfied: []
blockers: ["07", "14", "15"]
current_focus: null
next_action: "Wait for the master to mark the pickup condition satisfied."
last_updated_at_utc: "2026-08-08T18:44:11Z"
last_updated_by: planning-agent
verification:
  state: not_run # not_run | running | passed | failed | partial
  commands: []
review:
  reviewer: null
  reviewed_at_utc: null
  outcome: null # approved | changes_requested | rejected
post_implementation:
  remaining_work: []
  improvements: []
  known_issues: []
  follow_up_tasks: []
```

## Progress-reporting schema

Append one entry below for each material checkpoint, blocker, resumed turn, test result, review response, and handoff. Do not rewrite earlier entries. Send the same entry to the master so `STATUS.md`, `REPORTS.md`, `ISSUES.md`, and `DECISIONS.md` stay synchronized.

```yaml
- update_id: "16-YYYYMMDDTHHMMSSZ-NN"
  at_utc: "YYYY-MM-DDTHH:MM:SSZ"
  agent_id: "coding-agent-id"
  state_from: untouched
  state_to: in_progress
  summary: "What changed and why"
  work_completed: []
  files_changed: []
  tests:
    - command: "exact command"
      result: passed # passed | failed | partial | not_run
      evidence: "output, log, or artifact location"
  decisions: []
  contract_deviations: []
  issues:
    - id: null
      severity: null # P0 | P1 | P2 | P3
      status: null # open | mitigated | resolved
      summary: null
      owner: null
  blockers: []
  help_needed: []
  next_action: "Concrete next step"
```

### Progress reports

No progress reports yet.

## Dependencies

- Tasks 07 and 14 merged.
- May proceed alongside Task 15 with frozen controller fixtures.
- Final integration requires Task 15.

## Owned paths

- `frontend/src/plugins/canvas/atlas/`
- Atlas UI fixtures and tests within that directory.
- Do not edit the shared plugin shell or backend files.

## Work

- Preserve graph layout, seats, typed connections, seat editing, shared context, prompt editing, committee setup, attempt dialogs, gates, reports, and live run views.
- Reuse shared Canvas geometry, persistence, API, theme, dialogs, and terminal components.
- Support approved v1 display/migration messaging and the v2 editing contract.
- Display single-seat, multi-seat, partial failure, refinement, retry, cancel, takeover, handback, and publication states accurately.
- Support plain-directory and Git-project differences without hiding unavailable actions.
- Open related coSlash sessions by `{agent, id}` and make missing or ambiguous references explicit.

## Tests

```sh
cd frontend
npm test -- --run
npm run build
```

Add unit and browser tests for v1/v2 fixtures, add/edit/delete seat, connect/disconnect edges, invalid graph feedback, shared context and prompt editing, custom/no-run cases, single/multi-seat attempts, partial failure, gates, live terminal reconnect, reload/revision conflict, session links, and light/dark snapshots.

## Exit gate

- The Task 00 Atlas design and behavior checklist passes with no unexplained regression.
- Graph edits cannot create or silently persist a server-invalid structure.
- Committee progress remains understandable through partial failure and restart.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "16"
task_title: "Atlas frontend"
final_state: review # review | blocked | complete
agent: { id: null, runtime: null }
timing:
  {
    claimed_at_utc: null,
    started_at_utc: null,
    finished_at_utc: null,
    reported_at_utc: null,
  }
git: { branch: null, worktree: null, base_sha: null, result_sha: null }
summary: null
delivered: []
changed_files: []
acceptance_gates: [] # each: { gate, result, evidence }
tests: [] # each: { command, result, evidence }
task_evidence:
  graph_and_run_experiences: []
  legacy_visual_behavior_gaps: []
  browser_viewport_matrix: []
  migration_conflict_observations: []
decisions: []
contract_deviations: []
issues: [] # each: { id, severity, status, summary, impact, owner, recommendation }
blockers: []
post_implementation:
  remaining_work: []
  improvements: []
  known_issues: []
  follow_up_tasks: []
rollback_notes: []
next_task_recommendations: ["18", "19"]
central_updates_requested:
  { status: true, reports: true, issues: false, decisions: false }
```
