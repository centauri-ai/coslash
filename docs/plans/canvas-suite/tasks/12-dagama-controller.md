# Task 12 — DaGama Controller and Run Lifecycle

## Objective

Implement DaGama's controlled agent pipeline on top of the approved model, execution, terminal, artifact, verification, and publication services.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/12.js`](../task-status/12.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "12"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Tasks 04, 05, and 11."
pickup_condition: "Tasks 04, 05, and 11 are complete and merged into the assigned base SHA."
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
  required: ["04", "05", "11"]
  satisfied: []
blockers: ["04", "05", "11"]
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
- update_id: "12-YYYYMMDDTHHMMSSZ-NN"
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

- Tasks 04, 05, and 11 merged.

## Owned paths

- DaGama controller, runner, adapter, prompt, intake, reconciliation, cancellation, takeover, handback, and report files explicitly assigned by the master.
- Do not modify Task 11 model/store files or shared services.

## Work

- Preserve the proven pipeline: intake, isolated checkout, agent run, exact exit capture, artifact collection, verification, bounded repair, gates, and publication.
- Use native PTY/tmux execution through Task 04; do not shell out through ttyd or create another process layer.
- Implement retry, cancel, takeover, handback, restart reconciliation, and explicit terminal states.
- Bind related coSlash sessions by `{agent, id}` and preserve navigable provenance from card to run to artifact.
- Isolate working copies and ensure cleanup of processes, sockets, temporary paths, and worktrees on every exit path.
- Keep repair attempts bounded and policy-driven. Never silently publish after a failed required check or approval gate.
- Produce a durable run report containing inputs, commands, exit results, verification, artifacts, decisions, and publication outcome.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/dagama/...
```

Use fake agent, tmux, and GitHub adapters plus temporary Git repositories. Cover all baseline failure cases F1–F8, successful and failed verification, bounded repair, retry, cancel, takeover/handback, restart without duplicate execution, publication gates, session binding, and resource-leak checks.

## Exit gate

- A run can be stopped, resumed/reconciled, audited, and safely retried.
- No failed required gate can reach publication.
- Repeated restart tests create neither duplicate work nor leaked processes/worktrees.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "12"
task_title: "DaGama controller"
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
  lifecycle_and_controls: []
  fake_adapter_and_git_results: []
  f1_f8_results: []
  restart_and_leak_observations: []
  publication_gate_results: []
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
next_task_recommendations: ["13", "18"]
central_updates_requested:
  { status: true, reports: true, issues: false, decisions: false }
```
