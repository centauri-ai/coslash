# UI enhancement 2 — calm the session inventory

**Branch:** `hlu/UI-enhancement2`  
**Goals:**
1. Make the main screen feel like an attention router (“what needs you next”), not a spend dashboard that shows everything it knows.
2. **Increase information density** so more session rows fit on one screen without feeling more chaotic.

Through-line: fewer equal-weight badges, clearer hierarchy, delete constant-valued chrome before redesigning — then spend the reclaimed vertical space on more rows, not more decoration.

---

## Tier 1 — delete / demote (highest calm per line)

Do these first. Contained mostly to `SessionCard.tsx`, `CoslashPage.tsx`, and `time-window.ts`.

| # | Change | Notes |
|---|--------|--------|
| 1 | **Shorten session ID** on the detailed list card | Pass `shortened` to `SessionId`. Full UUID stays on hover / copy (compact + inspector already shorten). |
| 2 | **Gate machine badge** on the *visible* set spanning more than one source | Today it shows whenever any remote is configured (`showMachineBadge={configuredRemote}`). Only show when the filtered list actually needs disambiguation. |
| 3 | **Show modality only when Autonomous** | Do not variance-gate on the visible set (flickers as filters change). Interactive is the default for nearly every row; keep modality in the inspector. |
| 4 | **Quiet “Liveness unknown”** | Prefer muted status treatment (dot + quiet text), not a saturated badge. Do **not** collapse it into row dimming alone — dimming is already used for `displayStale`, and remote liveness is a different failure mode. |
| 5 | **Cut time windows** to rolling `24h` / `7d` / `30d` / `All` | Drop calendar “This week” / “This month”. Rolling windows match how people scan recent agent work. |

**Expected title-line effect:** roughly 6 objects → ~3 (vendor · name · status), plus ID shortened out of the way.

**Status (Tier 1):** implemented on `hlu/UI-enhancement2`. Screenshots: `docs/media/ui-enhancement2/`.

---

## Tier 2 — hierarchy + density (selective)

Calm and density go together: remove vertical waste so more sessions are visible at once, while hierarchy keeps the denser page scannable.

| # | Change | Notes |
|---|--------|--------|
| 6 | **Demote cost** on the row | From `text-base font-bold` to secondary / muted (`text-sm`). Cost stays useful; it should not be the scan anchor. |
| 7 | **Promote waiting / actionable status** | “Waiting on you” (and other action-driving states) should be the loudest status signal on the row. Vendor may keep a small quiet tint or mono mark — do not force everything grayscale except waiting. |
| 8 | **Lighten row chrome + tighten vertical rhythm** | Strip heavy card ring / shadow / excess padding; reduce list `gap` and per-row padding so more rows fit per viewport. Full “drop `Card` for a table” can wait until hierarchy lands (click target + subagent nesting still matter). |
| 9 | **Align a three-zone row** | Identity \| summary \| metrics so columns line up down the page and the eye scans vertically instead of re-parsing each card. Prefer a **single compact row** (or title + one muted meta line) over today’s multi-block card. |
| 10 | **Collapse subagent chrome by default** | Prefer a compact “N subagents” affordance on the row; expand only on demand. Nested subagent cards are a major density killer when several sessions have them open. |
| 11 | **Slim the top chrome where cheap** | After Tier 1’s fewer time chips, keep toolbar / stats from eating the first viewport. Every 32–48px saved above the list is roughly one extra row. |

**Density success check:** on a typical laptop viewport, the list should show meaningfully more sessions than today (target: roughly **~1.5–2×** visible rows when subagents are collapsed), without increasing badge count or competing CTAs.

**Status (Tier 2):** implemented on `hlu/UI-enhancement2` — hairline rows, quieter cost, loud waiting/active only, collapsed subagent chips, slimmer header/toolbar. Screenshots: `docs/media/ui-enhancement2/tier2-list.png`.

---

## Follow-ups (valid, not blocking Tier 1–2)

| # | Change | Why later |
|---|--------|-----------|
| 12 | **Inspector empty scaffolding** | Render ARTIFACTS / COMMITS / TODOS / FILES only when non-empty; fold all-zero artifact stats into one muted line. Fix the blank filler cell in the 3-column artifacts grid. |
| 13 | **Board column sizing** | Equal `1fr` columns waste space when Active is thin and Inactive is full; size by content or narrower empty tracks. Separate surface from the list calm pass — but board density should improve when columns aren’t equal-width empty bands. |
| 14 | **“N waiting on you” as a filter** | Strong product idea; ship only once waiting is trustworthy enough to be a primary control. A clickable count that applies a filter is enough at first. |
| 15 | **System cleanup** | Consolidate native `title=` tooltips onto the shared Tooltip; remove the duplicate `--color-opencode` block in `index.css`. Opportunistic when touching those files. |
| 16 | **Palette restraint** | Prefer brand + a few status tones + one subagent accent; distinguish timeline categories more by label/weight than by many hues. Easy to bikeshed — do after calm hierarchy. |

---

## Explicit non-goals for this pass

- New features or new data from the collector.
- Variance-gated modality (“show only when the visible set differs”).
- Erasing liveness into pure opacity/dimming.
- Big aesthetic retheme (purple gradients, new font system, etc.).
- Expanding toolbar chrome to “organize” clutter.
- Density via tiny unreadably small type — reclaim padding, badges, and nested cards first.

---

## Suggested implementation order

1. Tier 1 items 1–5 (less chrome → more room for rows)  
2. Tier 2 items 6–11 (hierarchy + vertical density)  
3. Re-screenshot list before/after; count visible rows on the same viewport  
4. Follow-ups 12–13 if time remains on this branch  

## Primary files

- `frontend/src/pages/coslash/components/SessionCard.tsx`
- `frontend/src/pages/coslash/CoslashPage.tsx`
- `frontend/src/pages/coslash/lib/time-window.ts`
- `frontend/src/pages/coslash/CoslashTabMenus.tsx` (time window labels)
- `frontend/src/pages/coslash/components/SessionInspector.tsx` (follow-up empty states)
- `frontend/src/pages/coslash/components/SessionBoard.tsx` (follow-up column sizing)
