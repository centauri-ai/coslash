# Task 02 — Minimal coSlash Core Registration

## Owner

Master agent only.

## Objective

Connect the compile-time plugin to coSlash with the smallest reviewed set of existing-file changes.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/02.js`](../task-status/02.js). The master agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

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
5. Select, document, and pin the approved Go/npm terminal dependencies before Task 04 starts; Task 04 consumes these versions and never edits manifests.
6. Keep incomplete destinations hidden through plugin readiness flags.

## Tests

- Existing `httpsec` host, origin, fetch-site, and token tests.
- New WebSocket subprotocol acceptance/rejection tests.
- Existing CoslashPage, SessionCard, and SessionBoard tests.
- Full Go/frontend baseline commands from `MASTER_AGENT.md`.

## Exit gate

- With the plugin disabled/unready, current coSlash renders and behaves exactly as before.
- Unauthorized plugin routes and sockets fail.
- Task 04's approved dependency versions are present in the manifests and recorded in the Task 02 report.
- Existing-file diff is limited to the allowlist and contains no product implementation.

## Report back

Append a master-authored Task 02 entry to `REPORTS.md` with exact existing files changed, line-count summary, tests, readiness behavior, and any exception to the edit budget. Update `STATUS.md`, `ISSUES.md`, and `DECISIONS.md` directly as coordinator.
