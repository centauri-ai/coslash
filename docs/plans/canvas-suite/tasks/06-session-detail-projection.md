# Task 06 — On-Demand Canvas Session Detail Projection

## Objective

Produce the heavy Session Canvas detail for Claude and Codex without slowing or replacing coSlash's existing session-list parser.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`; Task 09 retains integrated latency measurement.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/06.js`](../task-status/06.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

- 2026-08-08T21:50:14Z — `codex-root-task-06` claimed and started Task 06 on branch `codex/canvas-task-06-session-detail`, worktree `/Users/helu/code/product/coslash-task-06`, exact reviewed Task 01 base `477c66303864d16b11c9ea99a7abd842d49d1d3c`. Operator authorized moving forward from review; reviewed Task 00 session fixtures are read-only golden evidence. Current focus: bounded on-demand Claude/Codex projection without core parser changes.
- 2026-08-08T21:58:09Z — Implemented the plugin-local bounded projector over existing coSlash session facts. Claude/Codex Task 00-derived golden tests plus composite identity, missing/malformed transcript, size/row/projection bounds, and cancellation cases pass with `cd collector && go test ./internal/plugins/canvas/sessiondetail/...`. Next: grouped-read assertions, race/vet, collector regression, and owned-path audit.
- 2026-08-08T22:01:03Z — Moved to review at result `c67bd1db61810168741683cf895ac107b6a42c45`. Targeted race/vet, full collector test/vet, dependency/list-path audit, ancestry, ownership, and diff checks pass. Task 09 should integrate `ProjectKnown` and measure end-to-end handler latency.

## Dependencies

- Tasks 00 and 01.

## Owned paths

- `collector/internal/plugins/canvas/sessiondetail/`.
- Plugin-local legacy projection fixtures/tests.
- No edits to existing collector vendor parsers unless the master approves a separately reviewed exception.

## Required data

- Composite `{agent,id}` lookup.
- Full prompt and plan text per user turn.
- Per-turn todo snapshot, decisions, tool/error counts, and edited files.
- Context files, partial segments, grouped command reads, deferred context, and triggered MCP/skill/tool use.
- File edit statistics and diff hunks.
- Existing coSlash session/synthesis facts preserved rather than redefined.

## Tests

```sh
cd collector
go test ./internal/plugins/canvas/sessiondetail/...
```

Compare output to task 00 Claude/Codex golden fixtures. Add malformed/truncated transcript, missing file, unknown vendor, duplicate ID across vendors, and large transcript bounds.

Measure `/api/sessions` before and after integration; this package must not be invoked by the list endpoint.

## Exit gate

- Both vendors populate the Canvas contract.
- Heavy derivation is one-session/on-demand.
- No core session JSON contract changed.

## Report back

```markdown
Task: 06 Session detail projection
Status: complete | partial | blocked
Branch/base/result SHA:
Vendor coverage:
Golden fixtures passed:
Performance measurements:
Tests and results:
Missing legacy behavior:
Contract deviations:
New issues/risks:
```

## Worker report — review — updated 2026-08-08T22:01:03Z

```yaml
task_id: "06"
task_title: "Session detail projection"
final_state: review
agent: { id: "codex-root-task-06", runtime: "Codex coding agent" }
timing:
  claimed_at_utc: "2026-08-08T21:50:14Z"
  started_at_utc: "2026-08-08T21:50:14Z"
  finished_at_utc: "2026-08-08T22:01:03Z"
  reported_at_utc: "2026-08-08T22:01:03Z"
git:
  branch: "codex/canvas-task-06-session-detail"
  worktree: "/Users/helu/code/product/coslash-task-06"
  base_sha: "477c66303864d16b11c9ea99a7abd842d49d1d3c"
  result_sha: "c67bd1db61810168741683cf895ac107b6a42c45"
summary: "Added a bounded on-demand Claude/Codex projector that preserves collector-resolved session facts and derives Canvas turn, decision, context, tool-use, and diff detail from exactly one composite-identified transcript."
delivered:
  - "Composite {agent,id} resolver with unknown, missing, and identity-mismatch failures."
  - "Claude and Codex turn prompts, plan text, todo snapshots, decisions, tool/error counts, and edited-file attribution."
  - "File edit statistics and diff hunks; context segments, partial/grouped reads, deferred context, and MCP/skill/tool counts."
  - "Transcript, row, projection, malformed-input, and cancellation bounds."
  - "ProjectKnown integration entry point preserving collector names, status, subagents, synthesis, and probes."
changed_files:
  - "collector/internal/plugins/canvas/sessiondetail/claude.go"
  - "collector/internal/plugins/canvas/sessiondetail/codex.go"
  - "collector/internal/plugins/canvas/sessiondetail/doc.go"
  - "collector/internal/plugins/canvas/sessiondetail/helpers.go"
  - "collector/internal/plugins/canvas/sessiondetail/projector.go"
  - "collector/internal/plugins/canvas/sessiondetail/projector_test.go"
  - "collector/internal/plugins/canvas/sessiondetail/reads.go"
  - "collector/internal/plugins/canvas/sessiondetail/testdata/claude-golden.json"
  - "collector/internal/plugins/canvas/sessiondetail/testdata/codex-golden.json"
  - "collector/internal/plugins/canvas/sessiondetail/types.go"
acceptance_gates:
  - { gate: "Both vendors populate the Canvas detail contract", result: passed, evidence: "Task 00-derived Claude and Codex golden projection tests" }
  - { gate: "Heavy derivation is one-session/on-demand", result: passed, evidence: "Composite resolver selects one transcript; collector command dependency audit has no sessiondetail import" }
  - { gate: "Existing coSlash facts are preserved", result: passed, evidence: "ProjectKnown preservation test covers resolved name, status, and synthesis" }
  - { gate: "No core session JSON contract changed", result: passed, evidence: "All committed paths are under collector/internal/plugins/canvas/sessiondetail/" }
tests:
  - { command: "cd collector && go test ./internal/plugins/canvas/sessiondetail/...", result: passed, evidence: "Golden and all error/boundary tests pass" }
  - { command: "cd collector && go test -race ./internal/plugins/canvas/sessiondetail/...", result: passed, evidence: "Race detector pass" }
  - { command: "cd collector && go vet ./internal/plugins/canvas/sessiondetail/...", result: passed, evidence: "No findings" }
  - { command: "cd collector && go test ./...", result: passed, evidence: "Full collector suite passes" }
  - { command: "cd collector && go vet ./...", result: passed, evidence: "Full collector vet passes" }
  - { command: "cd collector && go list -deps ./cmd/coslash | rg sessiondetail", result: passed, evidence: "No match; current list path has zero package import cost" }
  - { command: "git diff --check 477c66303864d16b11c9ea99a7abd842d49d1d3c..c67bd1db61810168741683cf895ac107b6a42c45", result: passed, evidence: "No whitespace errors" }
performance_measurements:
  - "Current /api/sessions before/after effect is structurally zero: cmd/coslash does not import sessiondetail."
  - "End-to-end detail-handler latency remains a Task 09 integration measurement because no Task 06 route was authorized."
missing_legacy_behavior: []
decisions:
  - "Reuse existing vendor parsers for transcript identity/base facts and keep heavy derivation plugin-local."
  - "Expose ProjectKnown so integrated handlers preserve fully resolved collector session facts instead of recomputing them."
contract_deviations: []
issues: []
blockers: []
post_implementation:
  remaining_work: ["Master review/merge and Task 09 guarded-handler integration."]
  improvements: ["Add fuzz cases for future vendor JSONL variants and additional safe shell-read shapes."]
  known_issues: ["HTTP latency awaits Task 09 route integration; current dependency audit proves no list-path import."]
  follow_up_tasks: ["Task 09: call ProjectKnown, add guarded route tests, and record before/after endpoint latency."]
rollback_notes: ["Do not merge, or revert result c67bd1db61810168741683cf895ac107b6a42c45; no persisted state or existing core file changed."]
next_task_recommendations: ["09"]
central_updates_requested: { status: true, reports: true, issues: false, decisions: false }
```
