# Master Plan: coSlash Canvas Suite

## Objective

Add Session Canvas, DaGama Canvas, and Atlas Canvas to coSlash as a compile-time plugin while preserving the current coSlash Log experience and changing existing coSlash files as little as possible.

The delivered application remains one React frontend embedded in one guarded Go binary. Privileged filesystem, process, terminal, Git, artifact, verification, and publication behavior moves from legacy Vite middleware into Go.

## Baselines

- Target: latest `https://github.com/centauri-ai/coslash` main at implementation kickoff.
- Inspected target: `1bfe2e257aa6db3953b4f6448b9725c01388f46a`.
- Legacy source: `hlu/canvas-testing@c13a3ef01438193dcdcd2e387300e69ae3c27437`.
- Legacy status: 822 of 823 tests passed in the evaluation environment; the remaining failure was home-directory sandboxing. The build has 12 TypeScript errors, and controller tests emitted `EMFILE` watcher errors.

## Scope

### Included

- Per-session nine-node Canvas workbench and all spatial interactions.
- Context, diff, turn, analysis, terminal, checkpoint, experiment, pin, export, and rename behavior.
- DaGama fixed pipeline with visible terminals and isolated run roots.
- Atlas graph editor, committees, headless execution, monitoring, repair, and publication.
- Native PTY/WebSocket terminal attached to tmux.
- Non-destructive legacy filesystem and browser-state import.
- Server-backed Canvas workspace state.

### Excluded

- Columbus Canvas productization.
- Daily Digest and legacy Azure Foundry configuration.
- Runtime loading of third-party plugins.
- A separate Canvas service.
- Arbitrary-graph Atlas execution.
- New vendors or ticket/publish providers.
- Renaming the embedded `.fleetlog/run/**` agent/controller protocol during this migration.
- Automatic deletion of old data or run roots.

## Plugin boundary

New backend code lives under `collector/internal/plugins/canvas/`. New frontend code lives under `frontend/src/plugins/canvas/`. The plugin owns its settings, diagnostics, persistence, routes, background reconciliation, and UI.

Existing coSlash code only:

1. Constructs and registers the backend plugin.
2. Starts and closes its lifecycle.
3. Supports authenticated terminal WebSockets.
4. Exposes a frontend destination and session-card action slot.
5. Adds pinned terminal dependencies.

## Storage

- New project boards: `<project>/.coslash/{dagama,atlas}/boards`.
- New private workflow state: `~/.coslash/{dagama,atlas}/projects`.
- Canvas workspace state: `~/.coslash/canvas`.
- New run roots stay beneath coSlash-owned private roots but retain `.fleetlog/run/**` internally for parity.
- Legacy data remains untouched and is copied through an idempotent migration journal.
- Nonterminal legacy runs import as historical `interrupted_migration` runs and never restart automatically.

## Delivery order

1. Freeze and characterize the source.
2. Freeze plugin/API/storage contracts.
3. Land minimal core hooks and shared runtime primitives.
4. Deliver Session Canvas first.
5. Deliver DaGama state, controller, and UI.
6. Deliver Atlas state, controller, and UI.
7. Complete import, security, leak, packaging, and live-agent verification.
8. Synchronize the integration branch with current main and open the final PR.

## Integration strategy

- Integration branch: `hlu/canvas-migration` from current coSlash main.
- Each task runs in a dedicated branch and worktree based on its dependencies.
- Milestone PRs target the integration branch, never `main`.
- Only the final reviewed PR targets `main`.
- Existing Log behavior must stay green after every merge.

## Estimate

Expected effort is approximately 58–101 engineering days, or 12–20 weeks for one engineer. Session Canvas should become usable well before the complete suite.
