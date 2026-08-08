# Task 19 — Final Integration and Release Handoff

## Objective

Have the master agent produce a current-main-based, auditable Canvas plugin branch that is ready for review without changing `main` directly.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/19.js`](../task-status/19.js). The master agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. Only the master may claim or update this task. Update it at every state change and before handoff; never start while Task 18 is incomplete or has a `no-go` recommendation.

```yaml
task_id: "19"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Task 18 and its go recommendation."
pickup_condition: "Task 18 is complete, recommends go, and no P0/P1 issue remains open."
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
  required: ["18"]
  satisfied: []
blockers: ["18"]
current_focus: null
next_action: "Wait for the master to accept Task 18's release evidence."
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

Append one entry below for each material checkpoint, blocker, resumed turn, test result, review response, and handoff. Do not rewrite earlier entries. Mirror each entry into the appropriate central monitoring files.

```yaml
- update_id: "19-YYYYMMDDTHHMMSSZ-NN"
  at_utc: "YYYY-MM-DDTHH:MM:SSZ"
  agent_id: "master-agent-id"
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

- Task 18 complete with a `go` recommendation.
- All required task reports recorded centrally.

## Owner

Master agent only. Do not delegate central reconciliation or the final go/no-go decision.

## Work

- Synchronize the integration branch with the latest approved coSlash `main` and resolve conflicts without weakening plugin boundaries.
- Confirm the final diff stays inside new plugin paths plus the approved existing-file allowlist in `FILE_OWNERSHIP.md`.
- Review every report, issue, decision, contract deviation, and acceptance item; close or explicitly carry forward each one.
- Re-run all acceptance commands and targeted browser/manual journeys on the exact candidate SHA.
- Verify normal coSlash log and session-card behavior with the plugin unopened, then verify all three Canvas entry points and cross-navigation.
- Exercise migration, restart/reconciliation, cancel/takeover/handback, verification gates, publication, and rollback on the candidate.
- Prepare the review summary, feature-parity table, known limitations, migration/operator instructions, evidence links, and rollback steps.
- Do not merge to `main`; hand off the integration branch/PR for human review.

## Tests

Run the complete `ACCEPTANCE.md` matrix on the exact final SHA. At minimum:

```sh
cd collector
go test -race ./...
go vet ./...

cd ../frontend
npm test -- --run
npm run build
```

Record command output locations and manual/browser evidence in the central report.

## Exit gate

- Candidate is based on current approved `main`, while `main` itself remains untouched.
- All required checks pass on the candidate SHA and no P0/P1 issue is open.
- The diff, migration, release, and rollback plans are understandable to a reviewer who did not participate in implementation.

## Report back

Update the live status and `post_implementation` fields, append the final progress entry, update `STATUS.md`, append the final summary to `REPORTS.md`, and reconcile `ISSUES.md` and `DECISIONS.md`. Then record this schema:

```yaml
task_id: "19"
task_title: "Final integration"
final_state: review # review | blocked | complete
agent: { id: null, runtime: null }
timing:
  {
    claimed_at_utc: null,
    started_at_utc: null,
    finished_at_utc: null,
    reported_at_utc: null,
  }
git:
  branch: null
  worktree: null
  coslash_main_base_sha: null
  candidate_sha: null
summary: null
delivered: []
changed_files: []
final_diff_allowlist_audit: []
acceptance_gates: [] # each: { gate, result, evidence }
tests: [] # each: { command, result, evidence }
task_evidence:
  automated_and_manual_acceptance: []
  feature_parity_by_canvas: []
  migration_and_rollback_drill: []
  pr_or_handoff_location: null
decisions: []
contract_deviations: []
issues: [] # each: { id, severity, status, summary, impact, owner, recommendation }
blockers: []
open_approved_limitations: []
post_implementation:
  remaining_work: []
  improvements: []
  known_issues: []
  follow_up_tasks: []
rollback_notes: []
final_recommendation: no-go # ready_for_human_review | no-go
central_updates_completed:
  { status: false, reports: false, issues: false, decisions: false }
```
