# Task 06 — On-Demand Canvas Session Detail Projection

## Objective

Produce the heavy Session Canvas detail for Claude and Codex without slowing or replacing coSlash's existing session-list parser.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/06.js`](../task-status/06.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

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
