# Task 14 — Atlas Model, Graph, Policy, and Store

## Objective

Create Atlas Canvas's durable graph, committee, policy, run model, and deterministic storage layer.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/14.js`](../task-status/14.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "14"
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
- update_id: "14-YYYYMMDDTHHMMSSZ-NN"
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

- Atlas schema, graph, policy, board store, run store, reducer, committee-state, and fixture files assigned by the master under `collector/internal/plugins/canvas/atlas/`.
- Do not create controller, subprocess, HTTP, or frontend code.

## Work

- Define the current versioned graph schema for seats, typed edges, shared context, prompts, committee settings, run policy, revisions, and project identity.
- Implement the approved v1-to-v2 migration as an idempotent boundary operation.
- Preserve the fixed execution chain and committee/run states captured by Task 00.
- Normalize and validate graph IDs, dangling edges, duplicate seats, cycles where prohibited, policy allowlists, path containment, and project ownership.
- Use atomic revision-checked writes and shared events; provide deterministic reducer replay for board and committee state.
- Preserve unknown compatible fields on round-trip and reject incompatible future major versions clearly.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/atlas/...
```

Add golden tests for v1/v2 documents, migration idempotence, graph normalization, dangling/duplicate/cyclic inputs, legacy policy conversion, ID collisions, revision conflicts, corrupt/truncated storage, event replay, committee state sequences, and race/concurrent-write behavior.

## Exit gate

- Valid v1 data migrates once to the approved v2 representation without semantic loss.
- Invalid graphs and state transitions fail before storage or execution.
- Snapshot and event replay produce equivalent Atlas state.

## Report back

Before ending the assignment, update the live status and `post_implementation` fields, append a final progress entry, and send this exact schema to the master. A task is not `complete` merely because coding stopped; every exit gate must have passing evidence.

```yaml
task_id: "14"
task_title: "Atlas model/store"
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
  schema_and_graph_versions: []
  migration_and_replay_guarantees: []
  golden_and_race_results: []
  legacy_policy_decisions: []
  task_15_file_ownership_handoff: []
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
next_task_recommendations: ["15", "16", "17"]
central_updates_requested:
  { status: true, reports: true, issues: false, decisions: false }
```
