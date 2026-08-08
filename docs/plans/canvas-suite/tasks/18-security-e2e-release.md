# Task 18 — Security, End-to-End, and Release Validation

## Objective

Validate the assembled plugin against security boundaries, legacy parity, end-to-end workflows, visual expectations, restart behavior, and release requirements.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/18.js`](../task-status/18.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "18"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Tasks 09 through 17."
pickup_condition: "Tasks 09 through 17 are merged and their reports, issues, and deviations have been reconciled."
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
  required: ["09", "10", "11", "12", "13", "14", "15", "16", "17"]
  satisfied: []
blockers: ["09", "10", "11", "12", "13", "14", "15", "16", "17"]
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
- update_id: "18-YYYYMMDDTHHMMSSZ-NN"
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

- Tasks 09 through 17 merged into the integration branch.
- Read `ACCEPTANCE.md`, `CONTRACTS.md`, and all completed worker reports first.

## Owned paths

- New security, integration, end-to-end, fixture, and release-check files assigned by the master.
- Documentation for test execution within the task's assigned path.
- Do not hide product fixes in this task: route failures to the owning worker or master and retest after merge.

## Work

- Build the threat-model matrix for HTTP authentication, WebSocket origin/authentication, path traversal/symlinks, request limits, command/prompt injection, terminal isolation, and secret leakage.
- Run Session, DaGama, and Atlas journeys using fixture agents and temporary repositories/directories.
- Test process restart during active work and confirm reconciliation without duplicate execution or publication.
- Compare approved Task 00 reference screenshots and behavior in light/dark and supported viewport sizes.
- Execute migration on representative copies and validate journals, imported state, and interrupted-run behavior.
- Run the real-agent/tmux/GitHub matrix only in the final isolated environment and only with credentials explicitly provisioned for that validation.
- Record every P0/P1 failure immediately in `ISSUES.md` through the master; release cannot proceed with either severity open.

## Tests

Run every command and manual journey in `ACCEPTANCE.md`, including:

```sh
cd collector
go test -race ./...
go vet ./...

cd ../frontend
npm test -- --run
npm run build
```

Also run browser E2E/visual, malicious-input, restart/reconciliation, migration, resource-leak, and release/rollback drills defined by the assembled test harness.

## Exit gate

- Every acceptance row has evidence, an owner, and a pass result or explicitly approved non-release-blocking deviation.
- No P0/P1 issue remains open.
- Resource, security, restart, migration, visual, and parity matrices are complete.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A `go` recommendation requires every exit gate to pass and no open P0/P1 issue.

```yaml
task_id: "18"
task_title: "Security/E2E/release validation"
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
  automated_command_results: []
  browser_and_visual_matrix: []
  threat_model_and_malicious_input_results: []
  restart_migration_and_leak_results: []
  real_integration_matrix_and_environment: []
  evidence_locations: []
decisions: []
contract_deviations: []
issues: [] # each: { id, severity, status, summary, impact, owner, recommendation }
open_p0_p1_issues: []
approved_lower_severity_deviations: []
blockers: []
post_implementation:
  remaining_work: []
  improvements: []
  known_issues: []
  follow_up_tasks: []
rollback_notes: []
release_recommendation: no-go # go | no-go
next_task_recommendations: []
central_updates_requested:
  { status: true, reports: true, issues: true, decisions: true }
```
