# Task 08 — Server-Backed Workspace Persistence

## Objective

Replace new browser-origin-bound Canvas state with safe, revisioned state under `~/.coslash`.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`; documented pruning and non-session-route follow-ups remain open.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/08.js`](../task-status/08.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

- 2026-08-08T22:15:10Z — `claude-worker-task-08` claimed Task 08 on branch `claude/canvas-task-08-persistence`, worktree `/Users/helu/code/product/coslash-task-08`, base `685540299b233290128115fde7e6e700f5c519eb` (Task 03 post-review-fix result, a descendant of Task 01 result `477c663`, so both dependencies are contained in the base). Dependencies 01 and 03 are at review; the standing operator ruling treats review as unblocking. Current focus: revisioned atomic workspace persistence over `runfs` plus the debounced generation-counter frontend client.
- 2026-08-08T22:19:40Z — Characterized the legacy state being replaced: `frontend/src/pages/fleetlog/lib/canvas-workspace.ts` keyed `CanvasWorkspaceState` by a bare session ID under `fleetlog.canvasWorkspace.v1:${sessionId}`, which violates D-003. Confirmed Task 01 Go and TS workspace contracts and Task 03 `runfs` primitives are present in the base.
- 2026-08-08T22:33:00Z — Implemented the store, the frozen workspace handler, and the frontend client. Targeted package tests passed after fixing a self-found defect where the size bound applied to raw bytes before compaction, making acceptance depend on whitespace.
- 2026-08-08T22:41:05Z — Moved to review at `8e6158ecc064ecfb5dba13d5963f6a0d31c3fb4d`. Race `-count=3`, full collector test/vet, 32 frontend tests, lint, format, and build all pass. Ancestry and owned-path audits are clean. Next: master review, central report mirroring, and merge; Tasks 09 and 17 unblock on completion.

## Dependencies

- Tasks 01 and 03.

## Owned paths

- `collector/internal/plugins/canvas/persistence/`.
- `frontend/src/plugins/canvas/api/persistence.ts` and related new tests.

## State covered

- Selected `{agent,id}` session.
- Canvas layout, locks, collapse state, zoom if persisted, pins, checkpoints, and experiment metadata.
- Atlas/DaGama unsaved drafts and recent project/board/run selections.
- Turn-analysis cache metadata/results.

## Required behavior

- Atomic revisioned GET/PUT contracts.
- Schema/version normalization and explicit corruption reporting.
- Debounced clients with generation counters so stale responses cannot overwrite newer edits.
- Size/count/age bounds for caches and checkpoints.
- No credentials or raw terminal streams.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/persistence/...

cd ../frontend
npm test -- src/plugins/canvas/api
```

Cover first write, conflicts, concurrent clients, stale completion, corrupt files, permission failures, quotas, pruning, and restart.

## Exit gate

- No new functional state depends exclusively on localStorage.
- APIs are ready for task 17 import.
- Failed persistence leaves the active UI usable and visibly unsaved.

## Report back

```markdown
Task: 08 Persistence foundation
Status: complete (implementation), awaiting review
Branch/base/result SHA:
  branch claude/canvas-task-08-persistence
  base   685540299b233290128115fde7e6e700f5c519eb (Task 03 post-fix; contains Task 01 477c663)
  result 8e6158ecc064ecfb5dba13d5963f6a0d31c3fb4d

Schemas/APIs delivered:
  Go — collector/internal/plugins/canvas/persistence/
    persistence.Open(ctx, root, Options{MaxStateBytes, MaxDocuments, Now}) (*Store, error)
    (*Store).Load(ctx, contracts.SessionIdentity) (contracts.WorkspaceDocument, error)
    (*Store).Save(ctx, contracts.SessionIdentity, contracts.WorkspaceWrite) (contracts.WorkspaceDocument, error)
    (*Store).List(ctx) ([]Entry, error)          // enumeration entry point for Task 17
    (*Store).Handler() http.Handler, (*Store).Register(*http.ServeMux), (*Store).Root(), (*Store).Close()
    persistence.ValidateSession, persistence.Code(err), persistence.Message(err)
    Stable codes: INVALID_SESSION, INVALID_STATE, SCHEMA_UNSUPPORTED, REVISION_CONFLICT,
      STATE_TOO_LARGE, QUOTA_EXCEEDED, STATE_CORRUPT, PERSISTENCE_FAILED,
      MALFORMED_REQUEST, METHOD_NOT_ALLOWED, REQUEST_TOO_LARGE, UNSUPPORTED_CONTENT_TYPE
  Routes — exactly the frozen group, nothing else:
    GET  /api/canvas/workspaces/{agent}/{id}
    PUT  /api/canvas/workspaces/{agent}/{id}
  Frontend — frontend/src/plugins/canvas/api/persistence.ts
    workspacePath, loadWorkspace, saveWorkspace, CanvasPersistenceError,
    createWorkspaceClient -> { snapshot, subscribe, load, update, flush,
      resolveWithLocal, reloadFromServer, dispose }
  Storage: <coSlash home>/canvas/workspaces/<sha256(agent \0 id)>.json plus a derived
    workspaces/index.json catalog. Files 0600, directories 0700.

Conflict and pruning policy:
  Conflict — every write is an optimistic CAS on expectedRevision. A mismatch returns
    409 REVISION_CONFLICT carrying actualRevision, and the rejected write leaves the
    stored revision untouched. The client keeps local state, reports status 'conflict',
    stays dirty, and offers explicit resolveWithLocal (rebase onto the server revision)
    or reloadFromServer (discard local). No silent last-write-wins.
  Corruption — an undecodable document is reported as STATE_CORRUPT rather than reset.
    A write with expectedRevision 0 is the single explicit recovery path.
  Bounds — per-document size bound on canonical (compacted) bytes, and a total document
    count bound enforced when creating a new workspace; existing workspaces stay writable
    at the bound. Bounds inside the state (individual checkpoints, cache entries) belong
    to the consuming package because the envelope stores state as opaque JSON.
  Pruning — NOT delivered; see contract deviations.

Tests and results: all pass.
  cd collector && go test -race -count=3 ./internal/plugins/canvas/persistence/...   pass
  cd collector && go test ./... && go vet ./...                                       pass
  cd collector && go test -cover ./internal/plugins/canvas/persistence/...            81.9%
  cd frontend  && npm test -- src/plugins/canvas/api                                  19 pass
  cd frontend  && npm test                                                            32 pass / 6 files
  cd frontend  && npm run lint && npm run format:check && npm run build               pass
  Coverage of the required matrix: first write, conflict, concurrent clients (24-goroutine
  CAS loop asserting no lost update), stale completion (frontend generation counter),
  corrupt document and corrupt index, permission failure (read-only store), quota,
  restart, symlink refusal, traversal identity, case-only identity collision, same ID
  across agents, private modes, cancellation, and key-lock retention returning to zero.

Contract deviations:
  1. No pruning API. runfs exports no scoped removal or directory listing, so this package
     cannot reclaim a stored workspace. Envelope size and count bounds are enforced and
     List() enumerates the catalog, but age-based reclamation needs a scoped runfs.Remove
     that only Task 03's owner may add. No speculative edit was made to runfs.
  2. No route for non-session state. The brief lists recent project/board/run selections
     and DaGama/Atlas drafts as covered, but CONTRACTS.md freezes only the per-session
     workspace route. The store is key-agnostic and can hold them; no endpoint was invented.
  3. Documents are named by a digest of {agent,id} rather than by the identity itself.
     This is an internal storage decision, not an API change: the exact identity is
     preserved in the document and the catalog, and the API shape is unchanged.

New issues/risks:
  P1 Task 04 is blocked by the Task 02 dependency-pin cycle (go.mod has zero requires);
     it gates Wave 1 and therefore Tasks 09, 12, and 15. Operator decision required.
  P2 runfs lacks a scoped removal primitive (deviation 1).
  P2 No frozen route for non-session Canvas state (deviation 2).
  P3 CAS is serialized by an in-process keyed lock. Correct for the single-collector
     deployment; two collectors sharing one home could lose an update.

Recommended tasks now unblocked:
  09 Session backend — still needs 02, 04, and 06; this removes 08 from its blockers.
  17 Legacy import — still needs 11 and 14; Store.List and the revision-0 write path
     are the import entry points it was waiting on.
  Route registration for the workspace group must be done by Task 02 or Task 09, since
  plugin.go is not an owned path here.
```
