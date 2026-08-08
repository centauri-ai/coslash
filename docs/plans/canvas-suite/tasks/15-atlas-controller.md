# Task 15 — Atlas Controller and Committee Lifecycle

## Objective

Implement Atlas orchestration, including committee fan-out and main-agent refinement, using the shared controlled execution stack.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/15.js`](../task-status/15.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "15"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Tasks 04, 05, and 14."
pickup_condition: "Tasks 04, 05, and 14 are complete and merged into the assigned base SHA."
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
  required: ["04", "05", "14"]
  satisfied: []
blockers: ["04", "05", "14"]
current_focus: null
next_action: "Wait for the master to mark all dependencies satisfied."
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
- update_id: "15-YYYYMMDDTHHMMSSZ-NN"
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

- Tasks 04, 05, and 14 merged.

## Owned paths

- Atlas controller, runner, adapters, committee orchestration, prompt/intake, reconciliation, publication, and report files explicitly assigned by the master.
- Do not modify Task 14 model/store files or shared services.

## Work

- Preserve single-agent and multi-seat committee runs, sibling outputs, main-agent refinement, feedback triggers, and manual/automatic gates.
- Support headless execution with durable output capture in both plain-directory and Git-project modes.
- Enforce one live run per project unless an approved contract says otherwise.
- Implement retry, cancel, takeover, handback, restart reconciliation, session binding, reports, artifacts, verification, and gated publication.
- Use Task 04 execution and Task 05 artifact/publication services; do not add Canvas-specific shell or GitHub shortcuts.
- Keep committee fan-out bounded by policy and make partial seat failure explicit to the refining agent and operator.
- Clean up child processes, terminals, isolated checkouts, and temporary resources on every path.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/atlas/...
```

Use fake agents/adapters and temporary projects. Cover single and multi-seat runs, sibling output isolation, partial committee failure, main refinement, custom prompts, manual/automatic triggers, plain/Git modes, one-live-run enforcement, retry/cancel/takeover/handback, restart without duplicate work, report/publication gates, and leak checks.

## Exit gate

- Committee results are reproducible, attributable by seat, and visible to refinement.
- Restart never duplicates a live committee or publication.
- Plain and Git project modes both complete with correct artifact and cleanup behavior.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "15"
task_title: "Atlas controller"
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
  single_and_committee_lifecycle: []
  fake_adapter_and_project_mode_results: []
  restart_concurrency_leak_observations: []
  gate_and_publication_results: []
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
next_task_recommendations: ["16", "18"]
central_updates_requested:
  { status: true, reports: true, issues: false, decisions: false }
```
