# Frontend conventions

## Preserve identity and derived behavior

- A session is the composite of source, agent, and session ID. Key, deduplicate, select, cache, and reconcile sessions with `sessionKey` and `sameSession` from `lib/session.ts`, never by a display label, a basename, or a session ID alone. Keep revision-bound actions tied to the exact revision they displayed or approved, resolved through `sessionRevision`.
- Derive displayed titles, status, grouping, sorting, searching, totals, and facet counts from the shared helpers: `displayStatusLabel` and `boardStatusKey` in `lib/session.ts`, `groupSessions` and `boardGroupKey` in `lib/session-grouping.ts`, `sessionMatchesSearchTerm` in `lib/search.ts`. A control must sort and search the value users actually see. Do not add a local re-implementation of product logic a helper already expresses.
- Preserve API-owned labels and typed status/reason fields. Do not reconstruct sanitized labels from configuration or collapse distinct setup, credential, offline, limited-coverage, disabled, and request-failure states into one action.

## Make asynchronous UI state attempt-scoped

- Model loading, empty, success, error, offline, disabled, and retrying states explicitly. Retry the operation that failed and await any prerequisite refresh; do not route unrelated failures through a convenient global reload or setup flow.
- Give each dialog, request, and mutation attempt an identity or cancellation path. Ignore completions from a closed dialog, a superseded selection, or an older request so stale work cannot overwrite current state.
- Do not enable controls backed by settings or remote state until the authoritative load has succeeded. Saving after a load failure must not overwrite existing settings with defaults.
- When changing effects or memoized values, audit dependencies for both freshness and stability. Avoid tying exact-detail reads to unrelated global polling, and do not create fresh objects or timestamps that defeat memoization on every render.
- Work per render, per poll, or per group row must not scale with the session count. Index once outside the loop and reuse it; do not rescan every session for each sidebar facet or board cell.

## Preserve recovery and accessibility

- Keep the primary action and the recovery action reachable at narrow widths and when the inspector is docked. Verify overflow, sticky headers, nested sheets or dialogs, and focus behavior in the actual containing layout.
- Interactive recovery must remain a focusable control while work is pending unless duplicate activation is unsafe. Use `role="alert"` for actionable failures and appropriate labels and state for toggles, tabs, selections, sorting, and status.
- When changing a user-visible state machine, add tests for the success path, the relevant error or retry path, and stale or superseded completion. Add narrow-layout and accessibility assertions when controls or overlays change.

## Styling

- Prefer Tailwind spacing/size classnames over pixel values (e.g. `size-6`, not `w-[24px]`).
- Prefer padding over margins for spacing.
- Prefer whole-number scale steps over fractional ones (e.g. `p-4`, not `p-3.5`).
- Prefer `justify-between` over `ml-auto` for pushing flex children apart.
- Prefer plain `div`s over semantic HTML elements (`main`, `header`, `footer`).
- No slash opacity syntax on colors (e.g. `border-border/40`) — use a solid scale step like `border-neutral-100` instead.
- No unicode glyphs as UI icons (☑, ☐, ▸, ●, ⧉) — use icon components (lucide-react) or styled elements instead. Typographic characters in text (·, —, /) are fine.
- If a component becomes too big, consider breaking it down into child or sub components.
- Name things semantically to make reading code easier.
- Prefer padding over hardcoding heights. (h-8 vs p-2)
- For conditional classes with `cn()`, prefer object syntax (`"class": condition`) over `condition && "class"`.
- Prefer fail-fast code over fail-safe behavior, unless the fallback case is fully expected.

## Stylesheets and tokens

- Use the existing design tokens before introducing new ones. Keep one-off typography, spacing, color, and responsive rules with the component.
- Add or expand a stylesheet only when shared selectors, cross-element state, pseudo-elements, or behavior that is materially clearer in CSS requires it. Do not add CSS variables or named selectors merely to restate one component's utility classes.

## Working from designs

- Treat HTML mockups as visual references, not implementation templates. Match the requested visual contract without copying prototype chrome, sample data, or interactions that are outside the task.
- For pixel-matching work, render the reference and implementation at the same viewport. Verify computed dimensions and the relevant responsive breakpoints in addition to inspecting screenshots.
- After the first correct render, perform a simplification pass before running checks. Pay particular attention to net-new CSS and one-use abstractions.

## Verification

- Use the repository's installed Prettier rather than manually tuning wrapping or indentation. Run `npm run format`, inspect the resulting diff, then run `npm run format:check`.
- Verify frontend changes with `npm run lint`, `npm test`, `npm run build`, and `npm run format:check` from `frontend/`. If dependencies are unavailable and the exact formatter cannot run, leave formatting unchanged instead of guessing and report the missing verification.
