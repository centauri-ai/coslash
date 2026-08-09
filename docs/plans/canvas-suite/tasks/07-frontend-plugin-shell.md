# Task 07 — Frontend Plugin Shell and Shared Canvas UI

## Objective

Port the shared spatial UI and expose typed plugin components without modifying current coSlash pages directly.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`; DOM and visual follow-ups remain assigned to hardening.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/07.js`](../task-status/07.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

#### Live status record

```yaml
state: review
agent: claude-worker-task-07
branch: codex/canvas-task-07-frontend-shell
worktree: /Users/helu/code/product/coslash-task-07
base_sha: 477c66303864d16b11c9ea99a7abd842d49d1d3c
result_sha: 5d2e6af2b541b351341a7558a0b6232447e1ba95
claimed_at: 2026-08-08T21:50:08Z
started_at: 2026-08-08T21:53:10Z
updated_at: 2026-08-08T22:01:23Z
reason: >-
  Implementation and verification are ready for master review. Two exit-gate
  items cannot be closed inside this task's ownership boundary: DOM interaction
  tests need master-only dependency files, and Task 00's visual reference matrix
  was never captured.
```

- 2026-08-08T21:50:08Z — **claimed**. Reconciled STATUS.md, all 20 sidecars, task
  briefs, task branches, and worktree evidence. Task 03 was claimed by another
  agent during this scan (`codex/canvas-task-03-runfs-eventstore`); that
  selection was discarded and the plan rescanned once. Task 04 remains
  ineligible because it also depends on master-only Task 02. Created isolated
  branch/worktree from Task 01 result SHA `477c663`. No path overlap with the
  active Task 03, which owns backend `runfs/` only.
  Next action: inspect Task 01 frontend entry points, coSlash theme tokens, and
  legacy shared canvas primitives as read-only evidence.
- 2026-08-08T21:53:10Z — **in_progress**. Surveyed the Task 01 frontend skeleton,
  coSlash theme tokens in `src/index.css`, the available shadcn UI primitives,
  and the legacy shared canvas sources (`types.ts`, `wire.ts`,
  `use-canvas-node-interaction.ts`, `CanvasNode.tsx`, `ZoomControls.tsx`,
  `SessionCanvas.css`).
  Next action: implement pure geometry/wire/zoom/readiness/keyboard modules with
  node-environment tests, then the React components and themed stylesheet.
- 2026-08-08T22:01:23Z — **review** at `5d2e6af`. 17 new files, 1728 insertions,
  all under `frontend/src/plugins/canvas/shared/`. All five brief commands pass.
  Two exit-gate items are blocked outside this task's boundary and are recorded
  as issues rather than silently claimed: the DOM interaction/snapshot suite
  needs master-only dependency files, and Task 00's visual matrix does not exist.
  Next action: master review, DOM test dependencies, and merge after Task 01.

## Report back — Task 07

```markdown
Task: 07 Frontend plugin shell
Status: partial — implementation complete; two exit-gate items blocked outside this task's ownership

Branch/base/result SHA:
  branch codex/canvas-task-07-frontend-shell
  base   477c66303864d16b11c9ea99a7abd842d49d1d3c (Task 01 result; not yet merged to hlu/canvas-migration)
  result 5d2e6af2b541b351341a7558a0b6232447e1ba95

Components/styles delivered:
  Pure modules (no DOM, fully covered):
    geometry.ts   CanvasNodeBox, CANVAS_COLLAPSED_HEIGHT, visibleHeight, clampPosition,
                  clampSize, applyDrag, applyResize, exceedsDragThreshold
    wire.ts       nodeCenter, edgeAnchor, wirePath, triggerWirePath, feedbackWirePath,
                  feedbackBoxWirePath
    zoom.ts       clampZoom, zoomIn/zoomOut, canZoomIn/canZoomOut, zoomPercent, fitZoom
    readiness.ts  isDestinationReady, visibleDestinations, anyDestinationReady,
                  resolveDestination
    keyboard.ts   boardCommandFor, nodeCommandFor, isTextEntryTarget
  React components:
    CanvasNode.tsx      draggable/resizable/collapsible/lockable chrome, inline rename,
                        focus/lock/collapse actions, keyboard activation
    ZoomControls.tsx    zoom cluster driven by zoom.ts
    CanvasStage.tsx     CanvasStage (full-bleed scrolling stage + sticky toolbar),
                        CanvasWorldLayer (scaled world), CanvasWires (SVG layer)
    CanvasPanels.tsx    CanvasInspector, CanvasSidePanel, CanvasCommandOverlay
    use-canvas-node-interaction.ts  pointer drag/resize with companion dragging
  Styles:
    canvas.css    ported board chrome, every legacy hex mapped to a coSlash theme
                  token, plus a new prefers-reduced-motion block

Visual comparisons:
  NOT PERFORMED. Task 00 recorded its sanitized light/dark screenshot matrix as
  still-pending (no browser was available), so no baseline images exist to diff
  against. Parity is asserted from source-level porting of the legacy components
  and stylesheet, which were read as read-only evidence from
  /Users/helu/code/product/fleetlog-canvas-task-00.

Tests and results:
  cd frontend && npm test -- src/plugins/canvas   passed  5 files, 62 tests
  cd frontend && npm test                         passed 10 files, 75 tests
  cd frontend && npm run lint                     passed  0 findings in plugins/canvas
  cd frontend && npm run format:check             passed  whole repo clean
  cd frontend && npm run build                    passed  tsc -b + vite build, 1916 modules
  git status --porcelain                          only frontend/src/plugins/canvas/shared/ added

Existing dependencies requested:
  jsdom (or happy-dom), @testing-library/react, @testing-library/user-event in
  frontend/package.json, plus a vitest `test: { environment: 'jsdom' }` block in
  frontend/vite.config.ts. The repository currently has no DOM test environment —
  vitest runs in the node environment and every existing test is a pure lib test.
  Both files are master-only and this task is explicitly forbidden from editing
  dependency files, so the required component interaction and light/dark snapshot
  tests could not be written.

Contract deviations:
  1. Legacy hardcoded light-only hex replaced with coSlash theme tokens so dark
     mode renders. Deliberate; needs a DECISIONS.md entry if accepted.
  2. ZoomControls takes a CanvasZoomBounds object instead of separate min/max/step
     props, so a board and its keyboard shortcuts share one bounds value.
  3. Product-specific legacy classes (canvas-segment, canvas-turns,
     canvas-node-terminal, canvas-node-note, comparison drawer) were deliberately
     left out of the shared layer; they belong to Tasks 10/13/16.

New issues/risks:
  P1 No DOM test environment — component wiring is unverified by automated tests.
  P1 Exit gate "match task 00 visual baselines" cannot be evaluated; baselines do not exist.
  P2 Theme-token color mapping is a visual deviation from the legacy hex values.
  P3 npm ci reports one pre-existing high-severity advisory; host runs Node 23.5.0 vs required >=24.

Recommended frontend tasks now unblocked:
  Task 10 (Session frontend) — can consume @/plugins/canvas/shared directly.
  Tasks 13 and 16 — can reuse geometry/wires/chrome without importing Session
  Canvas; verified by an import audit showing the shared layer's only external
  imports are @/components/ui/button, @/lib/utils, react, and lucide-react.
```

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
