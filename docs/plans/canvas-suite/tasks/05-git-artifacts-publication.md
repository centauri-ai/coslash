# Task 05 — Git, Revision, Artifact, Verification, and Publication Primitives

## Objective

Implement shared workflow primitives without encoding DaGama- or Atlas-specific scheduling policy.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/05.js`](../task-status/05.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

## Dependencies

- Tasks 01 and 03.

## Owned paths

- `collector/internal/plugins/canvas/revision/`.
- `collector/internal/plugins/canvas/artifacts/`.
- `collector/internal/plugins/canvas/verification/`.
- `collector/internal/plugins/canvas/publication/`.

## Required behavior

- Temporary isolated repositories and Atlas in-place work-branch preflight support.
- Base SHA, tree OID, patch hash, changed-file, insertion, and deletion capture.
- Artifact basename/producer/size/schema validation and immutable promotion.
- Bounded verification argv, duration, output, and environment.
- Review mutation detection and revision invalidation.
- Publish preflight, commit, push, and GitHub PR idempotency.
- Reject control-plane paths, workflow files, unsafe remotes, stale bases, and empty changes.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/revision/... \
  ./internal/plugins/canvas/artifacts/... \
  ./internal/plugins/canvas/verification/... \
  ./internal/plugins/canvas/publication/...
```

Use temporary real Git repositories plus fake remotes and fake `gh`; never contact GitHub. Snapshot the user repo status/index/refs before and after isolation tests.

## Exit gate

- Shared APIs contain no workflow stage transitions.
- Publication retry cannot create a second PR effect for the same key.
- Failure messages are safe for API clients.

## Report back

```markdown
Task: 05 Git/artifacts/publication
Status: complete | partial | blocked
Branch/base/result SHA:
Packages/APIs delivered:
Isolation evidence:
Idempotency evidence:
Tests and results:
Contract deviations:
New issues/risks:
Recommended tasks now unblocked:
```
