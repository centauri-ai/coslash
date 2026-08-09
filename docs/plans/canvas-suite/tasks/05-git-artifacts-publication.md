# Task 05 — Git, Revision, Artifact, Verification, and Publication Primitives

## Objective

Implement shared workflow primitives without encoding DaGama- or Atlas-specific scheduling policy.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/05.js`](../task-status/05.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

| UTC | State | Summary |
| --- | ----- | ------- |
| 2026-08-08T21:58:35Z | claimed | Claimed by `claude-worker-task-05` on `claude/canvas-task-05-git-artifacts` at base `cb8485e` after Task 03 reached review and freed a worker slot. |
| 2026-08-08T21:59:40Z | in_progress | Worktree `/Users/helu/code/product/coslash-task-05` created; Task 01 contracts and Task 03 `runfs` confirmed present in the base. |
| 2026-08-09T00:20:00Z | in_progress | All four packages implemented; build and `go vet` clean. |
| 2026-08-09T00:32:00Z | in_progress | Concurrent-promotion race traced to `runfs.ensureDirectory` (Task 03's known defect); worked around by creating the blob directory once at store construction. |
| 2026-08-09T00:45:59Z | review | Result `c23b5d6`. All suites pass under `-race`; full collector regression, `go vet ./...`, and `gofmt` clean. |
| 2026-08-09T00:52:30Z | review | Rebased onto Task 03's review fix `6855402` with zero conflicts; re-verified on head `94fe07c`. Task 03 fixed the `ensureDirectory` race at its source. |

## Review handoff report

```markdown
Task: 05 Git/artifacts/publication
Status: complete
Branch/base/result SHA: claude/canvas-task-05-git-artifacts / 685540299b233290128115fde7e6e700f5c519eb / 94fe07cad85773683898781ed62cd4f69ae27d75
(originally built on cb8485e, Task 03's first result; rebased onto Task 03's fix 6855402 with zero conflicts and re-verified)

Packages/APIs delivered:
- revision: Runner/ExecRunner and Git (argv-only, no shell, hardened `-c` flags and
  an environment allowlist on every invocation); RunPreflight; CreateRunRoot;
  AttachInPlaceRunRoot; WriteExchangeDirectory; RemoveRunRoot; CaptureTreeOID;
  CaptureRevision; StatusPorcelain; ValidBranchName; ValidObjectID;
  ValidateRemoteURL; ExcludeDSStore; ExcludeExchange.
- artifacts: Store over a runfs scope; ReadCandidate; Promote; PromoteCandidate;
  List; Latest; ReadPromoted; Validator hook and JSONObjectValidator.
- verification: Check/Result/Document/Verdict; Policy; ValidateChecks; Run;
  Runner/ExecRunner with bounded argv, duration, output, and environment.
- publication: Publisher; Preflight (eight gates); Execute; IdempotencyKey;
  ResolveRemoteBaseSha; ParseGitHubOwnerRepo; GitHubRunner/ExecGitHubRunner.

Isolation evidence:
- `TestCreateRunRootIsolatesTheUserRepository` snapshots the user repository's
  `status --porcelain`, `show-ref`, `HEAD`, and the raw `.git/index` bytes before
  the run and asserts all four are unchanged after; it also asserts the clone's
  loose objects are not the same inode as the source's, proving `--no-hardlinks`.
- `TestCapturePreservesAConcurrentAgentIndex` stages one file, leaves another
  unstaged, and asserts `.git/index` is byte-identical across a capture.
- `TestExchangeDirectoryIsIgnored` asserts `status --porcelain` is empty after the
  control plane is written, so the exchange tree cannot leak into a user PR.
- `TestRemoveRunRootBounds` proves both guards: a repository outside a `roots`
  parent and a non-repository under one are each refused and left on disk.

Idempotency evidence:
- `TestExecuteRetryUpdatesInsteadOfOpeningASecondPullRequest` runs Execute twice
  for one `{run, revision}` key and asserts exactly one `pr create`, exactly one
  `pr edit`, exactly one `commit`, and a stable PR number across both runs.
- `TestExecuteUpdatesAPullRequestOpenedEarlier` asserts zero `pr create` when a PR
  already exists for the head branch.
- `TestExecuteStopsAtAFailedPreflight` asserts zero pushes and zero PR creations
  when any gate refuses.

Tests and results:
- `cd collector && go test -race ./internal/plugins/canvas/revision/... ./internal/plugins/canvas/artifacts/... ./internal/plugins/canvas/verification/... ./internal/plugins/canvas/publication/...` — pass on the rebased head (artifacts 1.925s, verification 2.493s, publication 1.912s, revision cached). This is the exact command in the brief.
- `cd collector && go test -race ./...` — pass. Full regression including httpsec, web, contracts, canvas root, runfs.
- `cd collector && go vet ./...` — pass, no findings.
- `cd collector && gofmt -l internal/plugins/canvas/` — no output.
- Real temporary Git repositories are used throughout the revision suite; publication uses a fake git runner and a fake `gh`. No test contacts GitHub or any network remote.

Files changed: 21 files, +5691/-6. New: revision/{doc,errors,exec,preflight,validate,runroot,capture,revision_test}.go, artifacts/{errors,artifacts,artifacts_test}.go, verification/{errors,verification,verification_test}.go, publication/{errors,publication,github,publication_test}.go. Removed: the four Task 01 `doc.go` placeholders, whose package comments were folded into the implementations. No file outside the four owned paths was touched.

Contract deviations:
1. Verification and publication return their documents rather than promoting
   `verification.json` and `publication.json` themselves. The legacy modules both
   promoted and appended run-store events; the exit gate requires these shared APIs
   to contain no workflow stage transitions, so the controller now owns promotion.
2. Publication takes a `ReviewFact{Approved, ChangeRevision}` instead of parsing
   `review.json`. That schema is product-owned by DaGama and Atlas.
3. `artifacts.NewStore` takes a `context.Context` so the blob directory is created
   once at construction rather than racing on each promotion.
4. Client-facing error messages never contain command output; `Detail()` carries the
   withheld diagnostic for server-side logging. Legacy interpolated stderr directly.
5. Two preflight gates added beyond the legacy checklist: `non_empty_change` and
   `remote_safe`.

New issues/risks:
- Confirmed a concurrent-parent-creation race in `runfs.ensureDirectory`: `Lstat`
  reported `ErrNotExist` to two goroutines and the loser of the `Mkdir` received
  `EEXIST` as a hard error. Reproduced by eight concurrent promotions. Not patched
  here, because `runfs/` is Task 03's owned path. RESOLVED UPSTREAM: Task 03's
  result `6855402` re-inspects the path when `Mkdir` returns `ErrExist`. The
  construction-time blob-directory creation added here is retained as defense in
  depth and to avoid repeating directory work on every promotion.
- Merge order matters: 01, then 03, then 05. The rebase onto 03 is already done and
  verified, so only a rebase onto the merged Task 01 remains and no source change is
  expected.

Recommended tasks now unblocked: 11 and 14 (DaGama and Atlas model/store) once
Task 00 and Task 03 are merged; 12 and 15 gain their git, artifact, verification,
and publication primitives.
```

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
