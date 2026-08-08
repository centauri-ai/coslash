# Task 11 — DaGama Model, Policy, and Store

## Objective

Create the durable DaGama domain model and reducer without coupling it to HTTP handlers, subprocesses, or UI code.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/11.js`](../task-status/11.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "11"
state: untouched # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: blocked # blocked | ready
status_reason: "Waiting for Tasks 00, 03, and 05."
pickup_condition: "Tasks 00, 03, and 05 are complete and merged into the assigned base SHA."
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
  required: ["00", "03", "05"]
  satisfied: []
blockers: ["00", "03", "05"]
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
- update_id: "11-YYYYMMDDTHHMMSSZ-NN"
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

- Tasks 00, 03, and 05 merged.

## Owned paths

- DaGama schema, policy, board store, run store, reducer, and fixture files assigned by the master under `collector/internal/plugins/canvas/dagama/`.
- Do not create controller, agent-execution, HTTP, or frontend code.

## Work

- Define a versioned board schema with stable IDs, project identity/path, stages, cards, gates, policy, revision, and timestamps.
- Preserve the proven DaGama lifecycle and exact statuses recorded by Task 00; reject undefined transitions.
- Use atomic, revision-checked persistence and the shared append-only event facilities.
- Build a deterministic reducer so stored snapshots can be reconstructed and audited from events.
- Normalize legacy inputs once at the boundary and keep the in-memory model strict.
- Validate project/run ownership, path containment, cross-canvas references, policy allowlists, and artifact references.
- Preserve unknown compatible fields when round-tripping newer documents.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/dagama/...
```

Add golden tests for schema serialization, normalization, status transitions, rejected cross-canvas references, optimistic revision conflicts, corrupt/truncated documents, symlink/path escapes, torn event tails, concurrent writers, reducer replay, and reducer invariants.

## Exit gate

- The model represents every approved DaGama state without an `any`-style escape hatch.
- Event replay deterministically recreates the same state and revision.
- Invalid transitions and stale writes fail safely without damaging the previous state.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "11"
task_title: "DaGama model/store"
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
  schema_version_and_lifecycle: []
  persistence_replay_guarantees: []
  golden_and_race_results: []
  legacy_normalization_decisions: []
  task_12_file_ownership_handoff: []
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
next_task_recommendations: ["12", "13", "17"]
central_updates_requested:
  { status: true, reports: true, issues: false, decisions: false }
```
