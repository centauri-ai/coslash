# Master Agent Coordination Instructions

## Role

You are the sole coordinator and integrator. Do not delegate coordination itself. Workers own bounded implementations; you own task readiness, branch bases, contracts, shared files, central reports, integration, and final verification.

## Startup procedure

1. Confirm the working repository is coSlash, not Fleetlog.
2. Archive legacy SHA `c13a3ef` on a non-force-pushed remote branch.
3. Create `hlu/canvas-migration` from the latest coSlash main.
4. Copy this planning package to the same path on that branch.
5. Record target and source SHAs in [STATUS.md](STATUS.md).
6. Validate all `task-status/NN.js` files against [AUTOMATION.md](AUTOMATION.md).
7. Complete or merge tasks 00 and 01 before opening dependent implementation tasks.
8. Create one branch and one worktree per worker from the exact dependency result SHA.

## Scheduling

- Keep one agent slot for coordination and use at most three workers concurrently.
- Start only tasks marked `ready` in `STATUS.md`.
- Do not run two tasks with overlapping owned paths.
- A task may start from frozen mocked contracts before its backend exists, but it cannot be marked integrated until its runtime dependency is merged.
- If a worker discovers a contract change, pause all consumers. Decide centrally, update `CONTRACTS.md` and `DECISIONS.md`, then rebase affected branches.

## Shared-file control

Only you may edit:

- Existing-file allowlist in [FILE_OWNERSHIP.md](FILE_OWNERSHIP.md).
- `STATUS.md`, `REPORTS.md`, `ISSUES.md`, and `DECISIONS.md`.
- Frozen cross-task contract definitions after task 01.

Reject or split worker commits that modify forbidden paths, generated dependency locks, unrelated code, or central documents.

## Worker handoff protocol

Every task file contains a report-back template. Require the worker to return it with:

- Result and commit SHA.
- Exact files changed.
- Tests run and their results.
- New issues, risks, assumptions, and decisions requested.
- Contract deviations.
- Suggested next tasks.

Then:

1. Verify its task sidecar and task-brief status/progress agree with Git evidence.
2. Update `STATUS.md`.
3. Append the full report to `REPORTS.md`.
4. Add new problems to `ISSUES.md` with owner and severity.
5. Add accepted choices to `DECISIONS.md`.
6. Inspect the diff and rerun proportionate tests.
7. Record the review outcome in the task sidecar/brief and merge only after the exit gate passes.

The master performs this reconciliation from worker-generated records; the human operator does not manually copy status into the dashboard.

## Integration checks after every merge

```sh
cd collector
go test ./...
go vet ./...

cd ../frontend
npm test
npm run lint
npm run format:check
npm run build
```

Use narrower checks while a skeleton intentionally does not build the full feature, but never merge a commit that breaks the previously green baseline.

## External effects

- Use fake Claude, Codex, tmux, Git remotes, and `gh` during normal work.
- No worker may publish a branch, create a PR, kill unrelated tmux sessions, or delete a user run root.
- Real live-agent and publication tests happen only in task 18 with explicit, disposable targets.

## Completion

Do not declare the migration complete until task 19 has produced the final feature matrix, exact verification evidence, migration/rollback instructions, and a clean integration diff against current main.
