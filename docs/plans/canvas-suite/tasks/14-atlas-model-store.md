# Task 14 — Atlas Model, Graph, Policy, and Store

## Objective

Create Atlas Canvas's durable graph, committee, policy, run model, and deterministic storage layer.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/14.js`](../task-status/14.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "14"
state: in_progress # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: ready # blocked | ready
status_reason: "Master takeover at 2026-08-09T02:17:04Z by explicit operator direction. Preserving the prior owner's substantial uncommitted Atlas implementation and continuing review, repair, and verification in the same isolated worktree."
pickup_condition: "Tasks 00, 03, and 05 are complete and merged into the assigned base SHA."
agent:
  id: codex-root-master-task-14
  runtime: Codex coding agent acting as master
  claimed_at_utc: "2026-08-09T02:17:04Z"
  started_at_utc: "2026-08-09T02:17:04Z"
  completed_at_utc: null
branch: claude/canvas-task-14-atlas-model
worktree: /Users/helu/code/product/coslash-task-14
base_sha: 94fe07cad85773683898781ed62cd4f69ae27d75
result_sha: null
dependencies:
  required: ["00", "03", "05"]
  satisfied: ["00", "03", "05"] # satisfied at review; final integration still requires the master's merge
blockers: []
current_focus: "Audit and repair the inherited Atlas schema, graph, policy, reducer, and stores before verification."
next_action: "Compare inherited code to the Task 14 contract and legacy fixtures, run targeted tests, and fix all failures within Task 14 ownership."
last_updated_at_utc: "2026-08-09T02:17:04Z"
last_updated_by: codex-root-master-task-14
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

```yaml
- update_id: "14-20260809T021704Z-01"
  at_utc: "2026-08-09T02:17:04Z"
  agent_id: "codex-root-master-task-14"
  state_from: in_progress
  state_to: in_progress
  summary: "Operator directed a master takeover after the prior owner stopped reporting checkpoints. All inherited uncommitted Atlas work is preserved in the same isolated branch/worktree."
  work_completed:
    - "Reconciled the sidecar, task brief, central status, branch, worktree, and file timestamps."
    - "Transferred exclusive Task 14 ownership without switching branches or cleaning inherited files."
  files_changed: []
  tests: []
  decisions:
    - "Continue from the inherited worktree instead of discarding or duplicating the prior implementation."
  contract_deviations: []
  issues:
    - id: null
      severity: P2
      status: mitigated
      summary: "Prior Task 14 status reporting stopped while implementation continued."
      owner: codex-root-master-task-14
  blockers: []
  help_needed: []
  next_action: "Audit inherited implementation, repair gaps, and run the required race suite."
```

```yaml
- update_id: "14-20260809T022137Z-02"
  at_utc: "2026-08-09T02:21:37Z"
  agent_id: "codex-root-master-task-14"
  state_from: in_progress
  state_to: in_progress
  summary: "Completed the inherited-code audit, repaired correctness and concurrency gaps, and expanded the Task 14 verification matrix."
  work_completed:
    - "Preserved nested unknown fields and valid UTF-8 during normalization."
    - "Made event-log sequence authoritative over materialized run views."
    - "Serialized optimistic board writes and reclaimed board/run keyed locks."
    - "Rejected cross-project board snapshots and run-created events."
    - "Added v1/v2 migration, graph-policy, reducer, persistence, replay, corruption, symlink, and concurrent-write tests."
  files_changed:
    - "collector/internal/plugins/canvas/atlas/{graph,run,boardstore,runstore,keylock}.go"
    - "collector/internal/plugins/canvas/atlas/{migrate,reducer,store}_test.go"
  tests:
    - command: "cd collector && go test ./internal/plugins/canvas/atlas/..."
      result: passed
      evidence: "Expanded Atlas package suite passed."
    - command: "cd collector && go test -race -count=3 ./internal/plugins/canvas/atlas/..."
      result: passed
      evidence: "Three repeated race-enabled runs passed."
  decisions:
    - "Treat events.jsonl as authoritative on every read; run.json is accepted only at the same sequence."
    - "Use reference-counted keyed locks for compound read-check-write operations."
  contract_deviations: []
  issues: []
  blockers: []
  help_needed: []
  next_action: "Run full collector regression/vet and verify the final owned-path commit."
```

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
