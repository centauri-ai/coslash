# Canvas Suite Acceptance Gates

## Existing coSlash regression gate

- List and Board filtering, sorting, search, totals, cards, inspector, synthesis, settings, diagnostics, onboarding, authentication, themes, startup, and packaging remain functional.
- Canvas detail parsing does not materially slow `/api/sessions`.
- Incomplete plugin destinations remain hidden.

## Session Canvas

- Exactly one `{agent,id}` session can be selected and restored.
- All nine nodes show real Claude and Codex data.
- Drag, resize, lock, collapse, focus, zoom, wires, auto-arrange, and keyboard controls match the visual reference.
- Context, diffs, turns, analysis, attention, pins, checkpoints, experiments, comparison, promotion, rename, export, terminal, and note delivery work.
- Workspace state survives browser and collector restart.

## DaGama

- Board save/reload and revision conflicts work.
- Intake → Plan → Build → Verify → Review → gate → Publish completes without manual context copying.
- The active user worktree, index, branch, and refs remain unchanged.
- Exact exit records and artifact validation gate success.
- Repair exhaustion, retry, takeover, handback, cancel snapshot, and restart reconciliation pass.
- Publication creates or updates at most one PR for the approved revision.

## Atlas

- Schema-v1 and schema-v2 boards round-trip without data loss.
- Graph editing and current run-policy blocking work.
- One-worker execution skips refine; multi-worker execution refines through the main worker.
- Successful sibling outputs survive worker retry.
- Manual/auto trigger and feedback behavior works.
- Headless attempts survive restart without duplication.
- Git-project and plain-folder modes retain their distinct behavior.

## Security and migration

- Token, host, origin, method, size, traversal, symlink, command injection, WebSocket, and file-render tests pass.
- Watcher, goroutine, PTY, timer, and subprocess counts return to baseline after repeated lifecycle tests.
- Legacy import is idempotent, reports conflicts, never overwrites originals, and never restarts old live agents.
- Browser export excludes secrets and imports only allowlisted schemas.

## Release

```sh
cd collector
go test -race ./...
make check
make test
make release
make smoke

cd ../frontend
npm test
npm run lint
npm run format:check
npm run build
```

Complete the live Claude/Codex matrix and a controlled idempotent publication test before final sign-off.
