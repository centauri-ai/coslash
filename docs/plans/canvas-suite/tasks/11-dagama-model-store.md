# Task 11 — DaGama Model, Policy, and Store

## Objective

Create the durable DaGama domain model and reducer without coupling it to HTTP handlers, subprocesses, or UI code.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`; the Task 12 file-level handoff is approved.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/11.js`](../task-status/11.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

## Status schema and current record

This record is the task's pickup lock and current truth. The master changes `readiness` to `ready`; the claiming agent immediately records its identity, branch, and UTC timestamps. Update it at every state change and before every handoff. Never claim a task already in `claimed`, `in_progress`, or `review`.

```yaml
task_id: "11"
state: complete # untouched | claimed | in_progress | blocked | review | changes_requested | complete | deferred
readiness: ready # blocked | ready
status_reason: "Reviewed result accepted by the operator and locally merged into hlu/canvas-migration at 01aa158ecc322b3dcf4b71e46d278944147ca7b6."
pickup_condition: "Tasks 00, 03, and 05 are complete and merged into the assigned base SHA."
agent:
  id: null
  runtime: null
  claimed_at_utc: null
  started_at_utc: null
  completed_at_utc: "2026-08-09T02:19:04Z"
branch: claude/canvas-task-11-dagama-model
worktree: /Users/helu/code/product/coslash-task-11
base_sha: 94fe07cad85773683898781ed62cd4f69ae27d75
result_sha: a6c1bb80674e08ad8e01f41ec286a1d06ceac0f6
dependencies:
  required: ["00", "03", "05"]
  satisfied: ["00", "03", "05"]
blockers: []
current_focus: "Complete and locally integrated."
next_action: "Task 12 may claim the accepted file-level handoff from integration base 01aa158."
last_updated_at_utc: "2026-08-09T02:19:04Z"
last_updated_by: codex-local-integrator
verification:
  state: passed # not_run | running | passed | failed | partial
  commands:
    - "cd collector && go test -race ./internal/plugins/canvas/dagama/..."
    - "cd collector && go test -race ./..."
    - "cd collector && go vet ./..."
review:
  reviewer: codex-local-integrator
  reviewed_at_utc: "2026-08-09T02:19:04Z"
  outcome: approved # approved | changes_requested | rejected
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

| UTC | State | Summary |
| --- | ----- | ------- |
| 2026-08-09T00:51:41Z | claimed | Claimed by `claude-worker-task-11` on `claude/canvas-task-11-dagama-model` at base `94fe07c`. Only Task 08 was actively in progress, so a worker slot was free. |
| 2026-08-09T00:52:30Z | in_progress | Characterized the legacy model from Task 00 evidence: `run-store.ts` (899 lines), `board-store.ts`, `board-policy.ts`, `dagama-vocabulary.ts`. |
| 2026-08-09T01:02:00Z | in_progress | Eight source files implemented and building clean; ownership, path-containment, and cross-canvas reference validation added. |
| 2026-08-09T01:06:00Z | in_progress | Closed an in-process check-and-append race in `Append` with a per-run writer mutex; added a test asserting exactly one of six concurrent writers wins. |
| 2026-08-09T01:09:46Z | review | Result `a6c1bb8`. 51 tests (115 with subtests) pass under `-race`; full collector regression, `go vet ./...`, gofmt clean. |

## Review handoff report

```yaml
task_id: "11"
task_title: "DaGama model/store"
final_state: review
agent: { id: "claude-worker-task-11", runtime: "claude-code" }
timing:
  claimed_at_utc: "2026-08-09T00:51:41Z"
  started_at_utc: "2026-08-09T00:52:30Z"
  finished_at_utc: "2026-08-09T01:09:46Z"
  reported_at_utc: "2026-08-09T01:09:46Z"
git:
  branch: "claude/canvas-task-11-dagama-model"
  worktree: "/Users/helu/code/product/coslash-task-11"
  base_sha: "94fe07cad85773683898781ed62cd4f69ae27d75"
  result_sha: "a6c1bb80674e08ad8e01f41ec286a1d06ceac0f6"
summary: >
  The durable DaGama domain model, policy, reducer, and stores. No HTTP handlers,
  no subprocesses, no UI. events.jsonl is the run; run.json is a rebuildable view.
delivered:
  - "vocabulary.go: the verified launch allowlists (vendors, models, efforts, permissions, check commands) and their validators."
  - "board.go: versioned board schema whose round trip preserves unknown fields at board, components, seat, check, and publish level."
  - "policy.go: Normalize repairs at the boundary; AssertPolicy refuses seat escalation, shell/npx check commands, control characters, duplicate check names, traversal base branches, and non-canonical project paths; AssertArtifactReference refuses cross-canvas references."
  - "run.go: run state, twenty typed event payloads, and a decoder that errors rather than skipping an unknown type."
  - "reducer.go: Reduce, a pure total function from events to state that reads no clock."
  - "boardstore.go: atomic, optimistically revision-checked board persistence with conflict reporting."
  - "runstore.go: append-only log plus materialized view, ValidateTransition, per-run writer lock, and listing."
changed_files:
  - "collector/internal/plugins/canvas/dagama/{vocabulary,board,policy,run,reducer,boardstore,runstore,errors,doc}.go"
  - "collector/internal/plugins/canvas/dagama/{board_test,reducer_test,store_test}.go"
  - "12 files, +3875/-1; every file inside the owned path."
acceptance_gates:
  - gate: "The model represents every approved DaGama state without an any-style escape hatch."
    result: passed
    evidence: >
      Every status, ownership, attempt status, gate decision, and component id is a
      named Go type with a closed constant set. No interface{} or json.RawMessage
      appears in RunState. The one raw-JSON use is the deliberate unknown-field
      preservation map on Board, which is never read as model data.
  - gate: "Event replay deterministically recreates the same state and revision."
    result: passed
    evidence: >
      TestReplayIsDeterministic marshals three reductions of one log (including one
      after a delay) and asserts byte equality; TestRunAppendMaterializesAndReplayAgrees
      asserts append, read, and replay produce identical JSON;
      TestReduceIncrementallyMatchesFullReplay checks every prefix.
  - gate: "Invalid transitions and stale writes fail safely without damaging the previous state."
    result: passed
    evidence: >
      TestRunAppendRejectsUndefinedTransitions covers twelve rejection cases and, for
      each, re-reads the run and asserts the state JSON is byte-identical to before the
      refused append. TestBoardSaveRejectsAStaleRevisionWithoutDamagingTheStoredBoard
      asserts the stored board still holds the winning writer's content and revision.
tests:
  - command: "cd collector && go test -race ./internal/plugins/canvas/dagama/..."
    result: passed
    evidence: "ok 2.611s; 51 top-level tests, 115 including subtests, 0 failures."
  - command: "cd collector && go test -race ./..."
    result: passed
    evidence: "Full collector regression green, including httpsec, web, runfs, and the Task 05 packages."
  - command: "cd collector && go vet ./..."
    result: passed
    evidence: "No findings."
  - command: "cd collector && gofmt -l internal/plugins/canvas/"
    result: passed
    evidence: "No files listed."
task_evidence:
  schema_version_and_lifecycle:
    - "BoardSchemaVersion and RunSchemaVersion are both 1; a board declaring a higher version is refused with UNSUPPORTED_SCHEMA_VERSION."
    - "The six-stage pipeline order is stated once in ComponentIDs and read from there; the set is closed."
    - "Run statuses (preparing, running, awaiting_approval, succeeded, failed, canceled) and component statuses (blocked, ready, running, validating, awaiting_approval, succeeded, failed) match Task 00's recorded lifecycle exactly."
    - "A publish gate pauses the run; a repair-exhaustion gate leaves it running so live seats stay inspectable. Both behaviours are pinned by tests."
  persistence_replay_guarantees:
    - "Reduce reads no clock; every timestamp comes from the event that carried it."
    - "The view is written after the log, so a crash between them leaves a stale view that Read repairs by replaying — TestRunReadRepairsAStaleView."
    - "A torn tail is not a durable event and is not replayed; the next append repairs it rather than concatenating — TestRunReadRecoversFromATornTail."
    - "Concurrent appends stay gapless and lose nothing — TestConcurrentAppendsAreSerializedAndGapless."
  golden_and_race_results:
    - "TestBoardSerializationGolden pins the exact board encoding byte for byte."
    - "TestBoardRoundTripPreservesUnknownFields asserts six unknown fields at five nesting levels survive, and that encoding is deterministic across marshals."
    - "The whole suite runs under -race with no findings."
  legacy_normalization_decisions:
    - "A drifted effort repairs to the middle of the range, not the first value: `low` on a repair round is the difference between a fix and another failed instance."
    - "A drifted permission repairs to the tightest legal value, never the loosest, so an unreadable value cannot silently widen what an unattended agent may do."
    - "PublishConfig.Draft defaults to true when omitted, so a board that never mentions draft cannot silently open a ready-for-review pull request."
    - "Normalize never invents identity: a board with no id stays without one and fails validation, because guessing would let two boards collide in the store."
    - "Normalize drops unusable and duplicate checks but never repairs an argv, because a partially repaired command is a different command."
  task_12_file_ownership_handoff:
    - "Task 11 owns and has created, under collector/internal/plugins/canvas/dagama/: vocabulary.go, board.go, policy.go, run.go, reducer.go, boardstore.go, runstore.go, errors.go, doc.go, board_test.go, reducer_test.go, store_test.go."
    - "Task 12 owns, and Task 11 has deliberately not created: controller.go, runner.go, pipeline.go, intake.go, prompt.go, repair.go, reconcile.go, takeover.go, cancel.go, review_outcome.go, and their tests."
    - "Task 12 must consume ValidateTransition rather than re-deriving legality, so there is exactly one definition of a legal move. Adding an event type means extending run.go's payload set and decodePayload, which is a Task 11 file — coordinate through the master."
decisions:
  - "Events are one Go struct per type rather than a tagged union, so the reducer decodes exactly the fields its case reads and a malformed payload is a decode failure rather than a silently empty struct."
  - "Reduce stays total over ordering and never fails on an out-of-order event, so replay always agrees with history. Ordering is enforced in ValidateTransition at append time, the only place it can be enforced without making replay disagree with the log."
  - "An unknown event type is a hard error, not a skip: silently ignoring an event a newer coSlash wrote would materialize a state that never existed."
contract_deviations:
  - "The brief's embedded YAML still reads `readiness: blocked` with status_reason 'Waiting for Tasks 00, 03, and 05' — the exact condition the operator waived when authorizing work from `review`. It was treated as stale rather than as an independent lock. The master should confirm this reading."
  - "Owned paths were to be 'assigned by the master'. No assignment existed and Task 12 was untouched, so this task defined the split and recorded it above, which is what the brief's task_12_file_ownership_handoff field implies."
  - "AssertArtifactReference is a concrete reading of the brief's 'cross-canvas references': a DaGama artifact record must name this run's own promoted blob, which rejects an Atlas blob reached through a relative path."
issues:
  - id: "11-1"
    severity: P3
    status: mitigated
    summary: "The per-run writer lock is in-process only."
    impact: "A second collector on the same runs root could interleave a check-and-append."
    owner: master
    recommendation: >
      One collector owns a runs root in the shipped topology, so this is documented
      rather than fixed. A real fix needs an append-if-predicate hook on
      runfs.EventLog, which is Task 03 or Task 18 territory.
blockers: []
post_implementation:
  remaining_work:
    - "Rebase onto the merged 01, 03, and 05 results; only public runfs APIs are consumed, so no source change is expected."
  improvements:
    - "Unknown-field preservation at every board nesting level, so an older build cannot delete a newer build's configuration."
    - "Refused appends provably leave state untouched, asserted for all twelve rejection cases."
    - "The reducer defensively copies caller slices, so a caller mutating its input cannot reach into materialized state."
  known_issues:
    - "Reduce is deliberately permissive about a component id outside the pipeline: it creates a visible entry rather than panicking, so a corrupt log is diagnosable instead of crashing the collector."
    - "Listing resolves through the scope and then uses os.ReadDir because runfs.Scope exposes no ReadDir. Resolve refuses traversal and symlinks and every entry is loaded back through the scope, so the listing cannot escape."
  follow_up_tasks:
    - "Task 17 needs the schema-v1 board and run fixtures this package's golden tests pin."
rollback_notes:
  - "The change is additive: twelve new files in one previously placeholder-only package. Reverting a6c1bb8 restores the Task 01 doc.go placeholder and removes nothing else."
next_task_recommendations: ["12", "13", "17"]
central_updates_requested: { status: true, reports: true, issues: true, decisions: true }
```

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
