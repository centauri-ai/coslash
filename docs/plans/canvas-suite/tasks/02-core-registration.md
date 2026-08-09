# Task 02 — Minimal coSlash Core Registration

## Owner

Master agent only.

## Objective

Connect the compile-time plugin to coSlash with the smallest reviewed set of existing-file changes.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted with follow-up `d7b1278` and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/02.js`](../task-status/02.js). The master agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

#### Live status record

```yaml
state: review
agent: claude-master-task-02
branch: claude/canvas-task-02-core-registration
worktree: /Users/helu/code/product/coslash-task-02
base_sha: 477c66303864d16b11c9ea99a7abd842d49d1d3c
result_sha: e5c7550ab62aa74a9447f950965f94f7e8d0d32d
claimed_at: 2026-08-08T22:07:30Z
started_at: 2026-08-08T22:09:00Z
updated_at: 2026-08-09T00:42:03Z
scope: items 1, 2, 3, 4, and 6 delivered; item 5 deferred with an exact reason
```

- 2026-08-08T22:07:30Z — **claimed**. The operator directed this agent to
  evaluate Task 02 and proceed if unblocked, waiving master-only
  self-assignment. Reconciled STATUS.md, all 20 sidecars, task briefs, and
  Git/worktree evidence: Task 02 was untouched with no branch or worktree. It
  occupies the master slot, so active workers 05/06/07 are unaffected, and none
  of their owned paths overlap. Base is the exact Task 01 result `477c663`, at
  review rather than merged, under the standing operator ruling that review
  unblocks dependents.
- 2026-08-09T00:20:00Z — **in_progress**. Backend lifecycle and WebSocket
  subprotocol guard implemented and tested.
- 2026-08-09T00:35:00Z — **in_progress**. `go test -race` caught a data race in
  the new test fake (not production code); counters converted to `sync/atomic`.
- 2026-08-09T00:42:03Z — **review** at
  `e5c7550ab62aa74a9447f950965f94f7e8d0d32d`. All required Go and frontend
  gates pass. Item 5 is deferred, not done.

## Dependencies

- Task 01 merged.
- Plugin lifecycle and frontend exports frozen.

## Owned paths

Only the existing-file allowlist in `FILE_OWNERSHIP.md`, plus integration tests. No product behavior belongs here.

## Work

1. Construct, register, start, and gracefully close the backend plugin.
2. Add guarded WebSocket token-subprotocol support without weakening existing HTTP checks.
3. Add frontend destination delegation and session-card action slots.
4. Forward card action support through Board without changing existing card content.
5. Add pinned Go/npm dependencies requested by task 04.
6. Keep incomplete destinations hidden through plugin readiness flags.

## Tests

- Existing `httpsec` host, origin, fetch-site, and token tests.
- New WebSocket subprotocol acceptance/rejection tests.
- Existing CoslashPage, SessionCard, and SessionBoard tests.
- Full Go/frontend baseline commands from `MASTER_AGENT.md`.

## Exit gate

- With the plugin disabled/unready, current coSlash renders and behaves exactly as before.
- Unauthorized plugin routes and sockets fail.
- Existing-file diff is limited to the allowlist and contains no product implementation.

## Report back

Append a master-authored Task 02 entry to `REPORTS.md` with exact existing files changed, line-count summary, tests, readiness behavior, and any exception to the edit budget. Update `STATUS.md`, `ISSUES.md`, and `DECISIONS.md` directly as coordinator.

## Worker report — 2026-08-09T00:42:03Z

```yaml
task: "02 Core registration"
status: review
agent: claude-master-task-02
branch: claude/canvas-task-02-core-registration
worktree: /Users/helu/code/product/coslash-task-02
base_sha: 477c66303864d16b11c9ea99a7abd842d49d1d3c
result_sha: e5c7550ab62aa74a9447f950965f94f7e8d0d32d
summary: >-
  Connected the compile-time Canvas plugin to coSlash core. The collector now
  constructs the plugin, registers its routes on the guarded /api mux after the
  core routes, starts it, and closes it after server shutdown. httpsec accepts
  the API token from a WebSocket subprotocol on genuine upgrades only and never
  echoes the token-carrying entry. The frontend delegates destination
  navigation and rendering and forwards a session-card action slot through
  SessionBoard and SessionCard. No product behavior was added.
changed_files:
  - collector/cmd/coslash/main.go
  - collector/cmd/coslash/main_test.go
  - collector/internal/httpsec/httpsec.go
  - collector/internal/httpsec/httpsec_test.go
  - frontend/src/pages/coslash/CoslashPage.tsx
  - frontend/src/pages/coslash/components/SessionBoard.tsx
  - frontend/src/pages/coslash/components/SessionCard.tsx
line_counts: "7 files, +504 / -38; product (non-test) files are +279 / -35"
allowlist_audit: >-
  All seven files are on the FILE_OWNERSHIP.md master-only allowlist. No
  go.mod, go.sum, package.json, package-lock.json, or plugin-owned file was
  changed. frontend/src/plugins/canvas/index.tsx belongs to the active Task 07
  and was deliberately not touched.
work_items:
  - id: 1
    title: Construct, register, start, and gracefully close the backend plugin
    status: done
    detail: >-
      serve() starts the plugin, serves until failure or SIGINT/SIGTERM, then
      shuts the server down before closing the plugin so no handler outlives
      what it uses, bounded by a 10s shutdownTimeout.
  - id: 2
    title: Guarded WebSocket token-subprotocol support
    status: done
    detail: >-
      allowedToken falls back to Sec-WebSocket-Protocol only when the request is
      a real WebSocket upgrade, so ordinary HTTP checks are unchanged. Exported
      IsWebSocketUpgrade and NegotiateSubprotocol for Task 04; the latter echoes
      only the static name.
  - id: 3
    title: Frontend destination delegation and session-card action slots
    status: done
  - id: 4
    title: Forward card action support through Board
    status: done
    detail: SessionBoard gained an optional renderSessionAction passed to compact cards.
  - id: 5
    title: Add pinned Go/npm dependencies requested by task 04
    status: deferred
    detail: >-
      Task 04 is untouched and has requested no versions, and collector/go.mod
      has zero requires. Pinning a PTY and WebSocket library here would invent
      Task 04's design and risk forcing rework. Escalated as issue below.
  - id: 6
    title: Keep incomplete destinations hidden through readiness flags
    status: done
    detail: >-
      A destination renders only when CANVAS_DESTINATION_READINESS reports it
      ready. All three are false, so Log always renders.
readiness_behavior: >-
  With every destination unready, CanvasDestinationNavigation,
  CanvasDestinationRenderer, and CanvasSessionCardAction all render null and the
  Log list/board path is taken unchanged. Existing frontend tests stay green.
tests:
  - command: "cd collector && go test ./..."
    result: passed
  - command: "cd collector && go test -race ./..."
    result: passed
    evidence: "No races, including the plugin start/close lifecycle test."
  - command: "cd collector && go vet ./..."
    result: passed
  - command: "cd frontend && npx tsc -b"
    result: passed
  - command: "cd frontend && npm test"
    result: passed
    evidence: "5 files, 13 tests."
  - command: "cd frontend && npm run lint"
    result: passed
    evidence: "Two pre-existing warnings in SessionSortDropdownMenu.tsx, unchanged by this task."
  - command: "cd frontend && npm run format:check"
    result: passed
  - command: "cd frontend && npm run build"
    result: passed
new_tests:
  - "httpsec: WebSocket subprotocol acceptance/rejection, wrong token, missing token, non-upgrade rejection, cross-origin rejection, and no-token-echo assertions."
  - "httpsec: NegotiateSubprotocol echoes only the static name."
  - "main: plugin registers behind the API guard; unauthenticated plugin routes are 401."
  - "main: a plugin cannot shadow a core route pattern."
  - "main: serve starts the plugin once and closes it once; a failed start does not close."
contract_deviations:
  - >-
    None to CONTRACTS.md behavior. The exact subprotocol string values
    ('coslash.terminal.v1' and the 'coslash.token.' prefix) were chosen here
    because CONTRACTS.md freezes behavior but not names, and Task 01 escalated
    them as an open master decision. They need a DECISIONS.md entry or a
    correction before Task 04 consumes them.
decisions_requested:
  - "Ratify or replace the two terminal WebSocket subprotocol names."
  - "Resolve the Task 02 item 5 / Task 04 circular dependency by naming the PTY and WebSocket libraries to pin."
issues:
  - severity: P1
    summary: >-
      Circular dependency: Task 04 waits on dependency versions supplied through
      Task 02, while Task 02 item 5 pins dependencies requested by Task 04.
      Task 04 stays blocked until this is broken centrally.
    owner: master/operator
  - severity: P2
    summary: "Terminal WebSocket subprotocol names were chosen by this task rather than by a prior central decision."
    owner: master
  - severity: P3
    summary: "Validation host runs Node 23.5.0 against a >=24 engine requirement; npm ci warned EBADENGINE and all checks still passed."
    owner: master/environment
remaining_work:
  - "Item 5 dependency pinning once the libraries are named."
  - "Master review, DECISIONS.md entry for the subprotocol names, and merge into hlu/canvas-migration."
improvements:
  - >-
    Core session selection is still keyed by id alone and can mis-target when
    Claude and Codex share an id. Task 02 added composite {agent,id} selection
    only on the plugin path; unifying it is a core behavior change and was left
    out on purpose.
known_issues:
  - "Task 04 remains blocked on dependency pinning."
  - "Node engine mismatch on the validation host."
follow_ups:
  - "Task 04 consumes httpsec.NegotiateSubprotocol and httpsec.IsWebSocketUpgrade."
  - "Task 07 fills the destination components behind the same frozen signatures; flipping a readiness flag is what reveals a destination."
rollback:
  - >-
    Do not merge, or revert e5c7550ab62aa74a9447f950965f94f7e8d0d32d. No
    persisted state, schema, or dependency manifest changed, so revert is clean.
central_updates_requested:
  status: true
  reports: true
  issues: true
  decisions: true
```
