# Task 07 — Frontend Plugin Shell and Shared Canvas UI

## Objective

Port the shared spatial UI and expose typed plugin components without modifying current coSlash pages directly.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/07.js`](../task-status/07.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

## Dependencies

- Tasks 00 and 01.

## Owned paths

- `frontend/src/plugins/canvas/index.tsx` after task 01 handoff.
- `frontend/src/plugins/canvas/shared/`.
- Plugin-local styles and tests.
- No existing coSlash page, card, board, theme, or dependency files.

## Work

- Implement destination/nav exports and readiness hiding.
- Port CanvasNode, zoom controls, wire geometry, drag/resize interaction, full-bleed shell, focus, lock, collapse, keyboard, and responsive behavior.
- Adapt old CSS to coSlash theme tokens and current styling conventions.
- Provide reusable dialog/panel shells needed by all three products.
- Keep product-specific state and content out of shared components.

## Tests

```sh
cd frontend
npm test -- src/plugins/canvas
npm run lint
npm run format:check
npm run build
```

Add interaction tests for drag, resize, lock, collapse, focus, zoom, keyboard escape/command behavior, wire layout, readiness hiding, accessibility, and light/dark snapshots.

## Exit gate

- Shared canvas primitives match task 00 visual baselines.
- Importing the plugin does not alter Log rendering until task 02 registers it.
- DaGama and Atlas can reuse geometry without importing Session Canvas.

## Report back

```markdown
Task: 07 Frontend plugin shell
Status: complete | partial | blocked
Branch/base/result SHA:
Components/styles delivered:
Visual comparisons:
Tests and results:
Existing dependencies requested:
Contract deviations:
New issues/risks:
Recommended frontend tasks now unblocked:
```
