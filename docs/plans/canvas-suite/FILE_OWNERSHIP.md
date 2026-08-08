# File Ownership and Conflict Rules

## Master-only existing files

Only the master agent may edit:

```text
collector/cmd/coslash/main.go
collector/cmd/coslash/main_test.go
collector/internal/httpsec/httpsec.go
collector/internal/httpsec/httpsec_test.go
collector/go.mod
collector/go.sum
frontend/package.json
frontend/package-lock.json
frontend/src/pages/coslash/CoslashPage.tsx
frontend/src/pages/coslash/components/SessionCard.tsx
frontend/src/pages/coslash/components/SessionBoard.tsx
frontend/src/pages/coslash/lib/api.ts
frontend/src/pages/coslash/lib/api.test.ts
docs/plans/canvas-suite/STATUS.md
docs/plans/canvas-suite/REPORTS.md
docs/plans/canvas-suite/ISSUES.md
docs/plans/canvas-suite/DECISIONS.md
```

## Worker ownership

Every assigned worker exclusively owns `task-status/NN.js` and `tasks/NN-*.md` for its task while that task is claimed. Those two monitoring files must be updated according to `AUTOMATION.md`; no worker edits another task's records.

| Task | Exclusive implementation paths                                      |
| ---- | ------------------------------------------------------------------- |
| 01   | Plugin root skeleton and contract packages only                     |
| 03   | `collector/internal/plugins/canvas/runfs/`                          |
| 04   | `agentexec/`, `terminal/`                                           |
| 05   | `revision/`, `artifacts/`, `verification/`, `publication/`          |
| 06   | `sessiondetail/`                                                    |
| 07   | `frontend/src/plugins/canvas/shared/`, plugin root shell            |
| 08   | `persistence/` and frontend persistence client                      |
| 09   | Backend Session Canvas handlers/services outside shared owned paths |
| 10   | `frontend/src/plugins/canvas/session/`                              |
| 11   | Backend `dagama/` model, policy, board/run store files              |
| 12   | Backend `dagama/` controller/runner files assigned by master        |
| 13   | `frontend/src/plugins/canvas/dagama/`                               |
| 14   | Backend `atlas/` model, policy, board/run store files               |
| 15   | Backend `atlas/` controller/committee files assigned by master      |
| 16   | `frontend/src/plugins/canvas/atlas/`                                |
| 17   | Backend/frontend `migration/`                                       |
| 18   | New integration/security/E2E test paths; no product rewrites        |

Tasks 11/12 and 14/15 share product directories sequentially. The master must give them non-overlapping file-level ownership when creating their worktrees.

## Conflict rule

If a worker needs a forbidden or currently owned file, stop and report the need. Do not make a speculative shared edit.
