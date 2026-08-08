# Task 10 — Session Canvas Frontend

## Objective

Rebuild the existing Session Canvas experience inside the coSlash Canvas plugin while preserving its working design and behavior.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/10.js`](../task-status/10.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "10"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Task 07; final integration also requires Task 09."
pickup_condition: "Task 07 is complete; Task 09 may still be active only if its frozen API fixtures are available."
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
  required: ["07", "09"]
  satisfied: []
blockers: ["07", "09"]
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
- update_id: "10-YYYYMMDDTHHMMSSZ-NN"
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

- Task 07 must be merged.
- Development may use frozen API fixtures while Task 09 is in progress.
- Final integration requires Task 09.

## Owned paths

- `frontend/src/plugins/canvas/session/`
- Session Canvas fixtures and tests within that directory.
- Do not edit coSlash pages, cards, shared plugin code, or central planning files.

## Work

- Preserve the nine useful node types: session, goal, plan, timeline, context, changes, terminal, note, and turn.
- Preserve attention states, pinning, checkpoints, experiments, comparison, promotion, export, rename, and AI-assisted actions.
- Use the shared Canvas geometry, theme, persistence, and API contracts instead of copying parallel infrastructure.
- Address sessions by `{agent, id}` everywhere; never assume a globally unique bare ID.
- Route HTTP through the guarded API client and terminals through the approved WebSocket helper.
- Represent loading, empty, disabled, unsupported, partial-data, and failed states explicitly.
- Restore saved layouts after reload without losing unknown forward-compatible fields.
- Keep the plugin lazy-loadable so the normal coSlash log/session-card path has no Canvas runtime cost until opened.

## Tests

```sh
cd frontend
npm test -- --run
npm run build
```

Add component and browser tests for every node type, duplicate IDs from different agents, reload/layout restoration, pin/checkpoint/experiment flows, compare/promote/export, rename, terminal reconnect, API failures, and disabled AI actions. Exercise light and dark themes and narrow/large viewports.

## Exit gate

- The legacy Session Canvas feature matrix is either preserved or has a documented, approved deviation.
- No direct `fetch`, unguarded WebSocket construction, or local-only source of truth exists in the feature.
- Refreshing the page restores the same server-backed canvas.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "10"
task_title: "Session Canvas frontend"
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
  screens_and_interactions: []
  legacy_parity_gaps: []
  browser_viewport_matrix: []
  accessibility_theme_observations: []
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
