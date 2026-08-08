# Task 17 — Legacy Import and Browser-State Migration

## Objective

Provide a safe, resumable, one-way migration from the legacy Canvas data and browser state into the coSlash Canvas plugin.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/17.js`](../task-status/17.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "17"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Tasks 08, 11, and 14."
pickup_condition: "Tasks 08, 11, and 14 are complete and merged into the assigned base SHA."
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
  required: ["08", "11", "14"]
  satisfied: []
blockers: ["08", "11", "14"]
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
- update_id: "17-YYYYMMDDTHHMMSSZ-NN"
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

- Tasks 08, 11, and 14 merged.
- Use the inventory and fixtures from Task 00.

## Owned paths

- `collector/internal/plugins/canvas/migration/`
- `frontend/src/plugins/canvas/migration/`
- Migration-specific commands, fixtures, and tests within those directories.
- Do not alter legacy data or shared persistence implementations.

## Work

- Copy filesystem state without overwriting the source; use full Git clones where repository semantics matter.
- Maintain a versioned journal with source identity, destination identity, checksums, progress, warnings, conflicts, and completion state.
- Make reruns idempotent and partial failures resumable.
- Resolve identifier collisions through explicit deterministic remapping recorded in the journal.
- Import formerly active runs as `interrupted`; never resume them automatically.
- Provide a narrowly allowlisted browser-state exporter/importer that converts approved legacy keys into server-backed persistence.
- Exclude credentials, tokens, raw environment secrets, terminal buffers, and unapproved local-storage keys.
- Reject symlink escapes, unsafe ownership/permissions, corrupt records, and unexpected schema versions with actionable diagnostics.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/migration/...

cd ../frontend
npm test -- --run
npm run build
```

Test first import, repeat import, interruption/resume, source changes, destination conflict, deterministic ID remap, corrupt/truncated input, permission errors, symlink escapes, active-run conversion, browser allowlist, secret exclusion, and representative Session/DaGama/Atlas fixtures.

## Exit gate

- Running migration twice produces no duplicate boards, runs, events, or browser records.
- Every copied or skipped item is traceable in the journal.
- Automated secret scans find no imported credentials or unapproved browser data.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "17"
task_title: "Legacy import"
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
  sources_and_schema_versions: []
  journal_and_idempotency_behavior: []
  conflict_and_interrupted_run_handling: []
  security_and_secret_scan_results: []
  data_intentionally_not_migrated: []
  task_19_operator_steps: []
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
