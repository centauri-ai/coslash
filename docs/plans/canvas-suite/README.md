# coSlash Canvas Suite Migration Package

This directory is the coordination package for migrating Session Canvas, DaGama Canvas, and Atlas Canvas from Fleetlog commit `c13a3ef01438193dcdcd2e387300e69ae3c27437` into a branch based on the latest `centauri-ai/coslash/main`.

This directory is the canonical live coordination package on the coSlash integration branch `hlu/canvas-migration`. Agents use this copy for task claims, status feeds, progress reports, and dashboard monitoring.

## Read first

1. [MASTER_PLAN.md](MASTER_PLAN.md)
2. [AUTOMATION.md](AUTOMATION.md)
3. [CONTRACTS.md](CONTRACTS.md)
4. [FILE_OWNERSHIP.md](FILE_OWNERSHIP.md)
5. [MASTER_AGENT.md](MASTER_AGENT.md)
6. The assigned file under [tasks/](tasks/) and its matching file under `task-status/`.

## Central monitoring files

- [STATUS.md](STATUS.md): current task, branch, agent, dependency, and verification state.
- [REPORTS.md](REPORTS.md): append-only summaries of completed or paused agent turns.
- [ISSUES.md](ISSUES.md): newly discovered bugs, risks, blockers, and follow-up work.
- [DECISIONS.md](DECISIONS.md): decisions that change contracts, scope, ordering, or behavior.
- [ACCEPTANCE.md](ACCEPTANCE.md): product and release gates.

Only the master agent edits these central monitoring files. Workers report using the template embedded in every task file; the master records that report centrally. This avoids merge conflicts between parallel worktrees.

Live task state is automatic: each assigned agent exclusively updates `task-status/NN.js` plus its own task brief according to [AUTOMATION.md](AUTOMATION.md). `migration-control.html` loads those files directly and refreshes every 15 seconds, so the human operator does not re-enter worker status.

## Execution waves

```text
Wave 0:  00 reference baseline  ||  01 plugin contracts
Wave 1:  02 core registration   ||  03 runfs  ||  04 terminal  ||  06 detail  ||  07 UI shell
Wave 2:  05 git/artifacts       ||  08 persistence
Wave 3:  09/10 Session Canvas   ||  11 DaGama model  ||  14 Atlas model
Wave 4:  12/13 DaGama           ||  15/16 Atlas      ||  17 legacy import
Wave 5:  18 security, E2E, release hardening
Wave 6:  19 final integration
```

With four available agent slots, the master remains active and runs at most three worker tasks concurrently.

## Safety rules

- Do not merge the unrelated Fleetlog and coSlash histories.
- Do not implement on `main`.
- Treat Fleetlog as a read-only behavioral reference after its source commit is archived.
- Do not expose incomplete Canvas destinations.
- Do not perform real pushes, PR creation, or destructive cleanup during normal tests.
- Do not let workers edit shared integration files or central monitoring documents.
