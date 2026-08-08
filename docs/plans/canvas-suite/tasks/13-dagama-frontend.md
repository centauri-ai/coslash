# Task 13 — DaGama Canvas Frontend

## Objective

Port the working DaGama Canvas design and operator controls into the shared coSlash Canvas plugin shell.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/13.js`](../task-status/13.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "13"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Tasks 07 and 11; final integration also requires Task 12."
pickup_condition: "Tasks 07 and 11 are complete; Task 12 may still be active only if frozen controller fixtures are available."
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
  required: ["07", "11", "12"]
  satisfied: []
blockers: ["07", "11", "12"]
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
- update_id: "13-YYYYMMDDTHHMMSSZ-NN"
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

- Tasks 07 and 11 merged.
- May proceed alongside Task 12 with frozen controller fixtures.
- Final integration requires Task 12.

## Owned paths

- `frontend/src/plugins/canvas/dagama/`
- DaGama UI fixtures and tests within that directory.
- Do not edit the shared plugin shell or backend files.

## Work

- Preserve project and board navigation, autosave, run creation, stage/card components, gates, status display, reports, and live terminal behavior.
- Reuse shared geometry, API, persistence, theme, dialog, and xterm facilities.
- Show the exact backend lifecycle and make retry, cancel, takeover, handback, approve, and publish controls available only in valid states.
- Make revision conflicts visible and recoverable; never overwrite newer server state silently.
- Link sessions with `{agent, id}` and show unsupported or missing session data safely.
- Maintain the legacy visual hierarchy while conforming to coSlash navigation and accessibility behavior.

## Tests

```sh
cd frontend
npm test -- --run
npm run build
```

Add unit and browser tests for project/board creation, autosave, revision conflict, run dialog validation, stage transitions, gates, live terminal reconnect, retry/cancel/takeover/handback, report/artifact navigation, polling/reload, backend failures, and light/dark snapshots.

## Exit gate

- The Task 00 DaGama UI parity checklist passes against fake and integrated APIs.
- UI controls never claim a transition the backend rejects.
- Reload and conflict recovery preserve server state and operator intent.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "13"
task_title: "DaGama frontend"
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
  screens_and_controls: []
  legacy_visual_behavior_gaps: []
  browser_viewport_matrix: []
  revision_conflict_observations: []
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
