# coSlash UI — design concepts

**Author:** Claude (Opus 5), acting as senior UX designer
**Date:** 2026-09-10
**Method:** Ran the real app (Go collector on `:8787` + Vite on `:5173`) against the live local dataset —
52 sessions, 7 Claude Code / 45 Codex, $296.20 this week — and read the components behind it.
**Scope:** Analysis and mockups only. No application code was changed.

Mockups (open in a browser):

**Prototypes (current)** — standalone, self-contained, clickable. Open any file directly in a browser;
no server, no build, no network. Each links to the other two in its top bar.

| | Concept | Risk | File |
|---|---|---|---|
| 1 | **Calm List** — hierarchy repair, attention groups, density modes | Low | [`ui-mockups/01-calm-list.html`](ui-mockups/01-calm-list.html) |
| 2 | **Board & Table** — attention lanes + sortable table | Medium | [`ui-mockups/02-navigation.html`](ui-mockups/02-navigation.html) |
| 3 | **Mission Control** — light glass, triage-shaped | High | [`ui-mockups/03-mission-control.html`](ui-mockups/03-mission-control.html) |

**What actually works in them** — these are prototypes, not screenshots:

- **Search** across title, goal, outcome, repo, branch and filenames, with a match-evidence chip
  (`OUTCOME`, `GOAL`, `REPO`) and the matching text highlighted. Try `frosty` — three results, none of
  which the real app can find today. Try `laticrete` — four results, including the false friend that
  matched a generated folder name, labelled as such.
- **⌘K palette** (or `/` to jump to the search box). Empty query shows live and recent sessions; once you
  type, ranking is by match evidence with recency breaking ties. Arrow keys and ↵ work.
- **Session inspector** — click any session. Goal, outcome, key decisions, files touched, the blocking
  question where there is one, and a Resume button that names its destination agent and machine and
  disables itself where resume isn't eligible.
- **Concept 1**: agent filter, and Comfortable / Compact / **Spend** density modes.
- **Concept 2**: Board ↔ Table, `Lanes by` Attention / Agent / Repo, sortable table columns, per-lane scroll.
- **Concept 3**: rail filters by attention bucket and agent; the hero switches between its waiting and
  calm variants depending on what's in scope.

Resume and Copy handoff are simulated and say so. Sample data is 22 sessions modelled on the live dataset;
all counts and totals derive from it, so every surface agrees.

*Sources for the prototypes live in [`ui-mockups/proto/`](ui-mockups/proto/); the earlier annotated design
boards are in [`ui-mockups/superseded/`](ui-mockups/superseded/) — same thinking, presented as commentary
rather than as a working prototype.*

---|---|---|---|
| 1 | **Calm List** — hierarchy repair of the session card | Low | [`ui-mockups/01-calm-list-v2.html`](ui-mockups/01-calm-list-v2.html) |
| 2 | **Finding the Session** — board rethink, table view, ⌘K palette | Medium | [`ui-mockups/02-navigation-v2.html`](ui-mockups/02-navigation-v2.html) |
| 3 | **Mission Control** — glassmorphic **light**, triage-shaped | High | [`ui-mockups/03-mission-control-v2.html`](ui-mockups/03-mission-control-v2.html) |

<details><summary>v1 mockups (superseded, kept for comparison)</summary>

[`01-calm-list.html`](ui-mockups/01-calm-list.html) ·
[`02-navigation.html`](ui-mockups/02-navigation.html) ·
[`03-mission-control.html`](ui-mockups/03-mission-control.html) (dark)

</details>

---

## 1. What I found

The three complaints — busy cards, hard-to-navigate board, slow to find a session — are not three problems.
They are three symptoms of one thing: **nothing on this page is allowed to be more important than anything
else.** Every session renders at identical visual weight whether it is blocked on a question, running right
now, or finished four days ago. All ranking work is therefore pushed onto the user's eyes.

What's notable is that the underlying product is *good*. The session inspector is genuinely well designed —
Goal / Outcome / Key decisions / Timeline is a strong information architecture, and the synthesis behind it is
the real value of coSlash. The problem is almost entirely that the list and board don't inherit that quality.
The inspector knows what a session was *for*; the card shows a truncated first sentence or "No summary
available".

### 1.1 The list card

Measured on the live app, the default card renders up to **eleven** elements at near-identical weight: vendor
pill, title, full UUID, status pill, machine pill, modality pill, summary line, five-fact metadata run, cost,
token count — plus a subagent rail.

Concrete findings, in rough order of impact:

- **The session ID outranks the title.** `SessionName` is capped at `max-w-80` and truncates;
  `SessionId` renders all 36 characters with no cap. At a 900px window the title collapses to
  "Design Coslash …" while the UUID next to it is fully legible. This is exactly backwards — nobody
  identifies a session by its UUID, and this is the clearest single defect on the page.
  ([`SessionCard.tsx:75`](../frontend/src/pages/coslash/components/SessionCard.tsx#L75))
- **Cost out-shouts the title.** Cost renders at `text-base font-bold`; the session name at
  `text-sm font-semibold`. The largest, heaviest text on every card is a number you are rarely scanning for.
  ([`SessionCard.tsx:131`](../frontend/src/pages/coslash/components/SessionCard.tsx#L131))
- **41 of 52 cards carry an "Inactive" badge.** A badge worn by 79% of the population conveys almost nothing
  while spending colour and space on all of them.
- **"This Mac" labels every local card.** The machine badge is gated on *whether a remote is configured*, not
  on whether the session is remote — so configuring one remote host stamps a redundant badge onto every local
  session.
- **"No summary available" repeats down the whole list.** Teaching the eye to skip that row is expensive,
  because that row is where the real summaries live.
- **Modality ("Codex Work Desktop", "Claude Desktop") is near-constant per user** and so carries close to zero
  information per card, while costing a full pill.
- **Location is buried.** Repo and branch — the thing people actually recognise work by — sit inside an 11px
  grey monospace run of five `·`-separated facts, below the summary.
- **Waiting looks like finished.** A session blocked on a question is the highest-value event the product can
  show you, and it is styled as one more amber pill in a row of pills.

### 1.2 The board

Today's board is a `repo × status` matrix, not a board.

- **Columns are `1fr` regardless of population.** With 2 Active / 41 Inactive / 9 Unknown, roughly two thirds
  of the width is permanently blank while 41 cards queue single-file down one narrow column. I confirmed this
  by scrolling: mid-board, two of three columns are pure whitespace.
  ([`SessionBoard.tsx:144`](../frontend/src/pages/coslash/components/SessionBoard.tsx#L144))
- **Repo context scrolls away.** Only the status header is sticky. Several hundred pixels down, the repo and
  branch labels are gone and there is no way to tell what you are looking at.
- **Double grouping mostly produces singletons.** Repo header + branch row = two rows of chrome for a repo
  that holds one session, which is the common case.
- **One shared scroll surface.** Reading down the long Inactive column drags the two live sessions off screen.
- **Subagent rails inflate cells,** so a single parent with seven subagents can dominate a column.

### 1.3 Finding a session

This one has a specific, fixable cause:

```ts
// frontend/src/pages/coslash/lib/search.ts
return [session.name, session.repo, session.branch].some(...)
```

**Search tests three fields.** It never touches the synthesised goal or outcome — the richest description of
what a session actually did, already computed, already rendered in the inspector. So searching for what you
remember ("grout", "the schema thing") silently under-returns, and you fall back to scrolling 52 cards.

There is also **no keyboard route to the search field** — no `⌘K`, no `/`. And sort offers Recency / Status /
Est. cost / Tokens / Duration, none of which is "the one I was just in".

---

## 2. Concepts

Three tiers, deliberately independent — you can ship 1 without 2, and 2 without 3.

### Concept 1 — Calm List (low risk, same design system)

📄 [`ui-mockups/01-calm-list.html`](ui-mockups/01-calm-list.html) — includes a faithful before/after.

A pure hierarchy repair. No new colours, no new framework, no density change (still three rows). Every move is
a deletion or a demotion; the card goes from ~11 elements to 5.

| Move | Change |
|---|---|
| **Delete** | UUID leaves the card face → 8-char chip on hover; full value stays in the inspector |
| **Delete** | Status badge renders only for Active / Waiting — Inactive becomes the unmarked default |
| **Delete** | "No summary available" renders nothing at all |
| **Delete** | "This Mac" shows only for genuinely remote sessions |
| **Delete** | Modality pill moves to the inspector (or becomes a filter) |
| **Demote** | Vendor pill → 3px coloured spine on the card edge |
| **Demote** | Cost → 12px tabular in the fact row, still right-aligned |
| **Promote** | Title → 14.5px, uncapped, truncates last instead of first |
| **Promote** | Repo + branch → readable breadcrumb with branch in a monospace chip |
| **Promote** | Waiting → warm card wash, amber spine, pinned to its own group at the top |
| **Add** | Subagents roll up to one "7 subagents" chip |
| **Add** | Time-bucket rules: Needs you / Running / Earlier today / Yesterday |

The highest-value line in this concept costs about two lines of code: **extend
`sessionMatchesSearchTerm` to the synthesised goal and outcome.**

### Concept 2 — Finding the Session (medium risk, same design system)

📄 [`ui-mockups/02-navigation.html`](ui-mockups/02-navigation.html)

Three navigation answers.

**A · Attention lanes.** Replace `repo × status` with lanes ordered by demand on your attention —
*Needs you / Running / Last active today / Earlier*. Lanes are populated by construction, so no column is ever
mostly blank. Repo becomes a **sticky subhead inside each lane**, so context travels with the cards. Each lane
scrolls independently, so live sessions never leave the viewport. A `Lanes by [Attention | Status | Repo |
Agent]` control turns one hard-coded board into four.

**B · Or keep the matrix, repaired.** If the repo × status model should stay: size columns to content instead
of `1fr`, make each repo one collapsible lane with branches summarised in the label rather than exploded into
rows, hatch empty cells so they read as "nothing here", and make the repo label sticky.

**C · Table view.** Cards are right for ten sessions and wrong for fifty. A third density — sortable columns,
one row per session — makes "the expensive one from yesterday" two clicks instead of a scroll hunt.

**D · ⌘K palette.** Fuzzy match across title, **goal, outcome**, repo, branch and ID; `repo:` `branch:`
`agent:` `is:waiting` prefixes; live sessions pinned to the top regardless of rank; `⌘↵` resumes directly.

### Concept 3 — Mission Control (high risk, new visual system)

📄 [`ui-mockups/03-mission-control.html`](ui-mockups/03-mission-control.html)

Dark-first glassmorphism — frosted panels, a slow ambient gradient field, vendor-coloured glow, motion on live
cards. The modern-AI register you asked about.

The glass is the surface; the argument underneath is **three visual weights instead of one**:

- **Hero** — the blocked session, with its actual question quoted, answerable without opening anything.
- **Card** — live sessions, with a running shimmer and a context-usage gauge that predicts compaction.
- **Row** — the 41 finished ones, quiet.

Filters move from five stacked horizontal tab menus into a persistent left rail with live counts, so the
counts themselves become navigation.

**Honest costs**, documented in the mockup itself:

- Contrast. Frosted panels over a moving gradient make WCAG AA genuinely hard. Needs a fixed dark substrate
  under the blur and a real audit — not just `backdrop-filter`.
- Performance. `backdrop-filter` on 50+ visible cards costs real frames on Intel Macs. Blur the rail, hero and
  live cards only; flat translucent fill for stream rows.
- It commits to dark. The language does not survive a light theme intact, so adopting it means dark-only or
  maintaining two genuinely different systems.
- It contradicts the house style. `no unicode glyphs`, `no slash-opacity colours`, solid scale steps — the
  frontend conventions in `frontend/CLAUDE.md` are written for a flat, opaque, token-driven system. This
  concept needs an explicit exception, not a quiet drift.

---

## 3. Recommended sequence

Ordered by value per unit of risk, not by ambition.

1. **Search the goal and outcome.** Two lines. Fixes the stated top complaint more than any visual change will.
2. **Card hierarchy repair** (Concept 1). Self-contained in `SessionCard.tsx`; no data or state changes.
3. **⌘K palette.** Additive — nothing existing has to move.
4. **Attention lanes board** (Concept 2A), keeping status lanes available behind the `Lanes by` control.
5. **Table view.** Third density mode; mostly new code, low blast radius.
6. **Mission Control** — treat as a directional exploration, and harvest the hero / three-weight hierarchy /
   counted rail / context gauge into the current system first. Those work in light shadcn today and carry most
   of the benefit without the contrast and performance bill.

The thing I'd protect throughout: the inspector is the best-designed surface in the product. The goal of all
of the above is to make the list and board feel like they were designed by whoever designed the inspector.

---

## 4. Review of other proposals

**[`ui-ideas-codex.md`](ui-ideas-codex.md)** — reviewed 2026-09-10. My full review is appended to that
document under *"Review by Claude (Opus 5)"*, leaving its author's text unmodified.

Summary of that review:

- **Independently verified its three empirical claims.** Searching `Frosty` returns
  *"Nothing matches 'Frosty' in this window"* while the card reading "Selected Laticrete **Frosty** as the
  recommended grout color…" is visible in that same window — the sharpest possible demonstration of the
  search-coverage defect. `Laticrete` does return three results, the third matching on a prompt-derived
  folder name. The Markdown-debris observation is real too (`| Custom color | Closest Laticrete match |`
  rendering raw on a card).
- **We converged independently** on the three biggest findings: search coverage is the top defect, title-first
  hierarchy with one quiet outcome line, and the board optimizes for storage topology over recognition.
- **One correction to its mechanics:** search indexes `repo`, not `cwd` — so its proposal to add path and
  artifact matching would *increase* unexplained results unless match-evidence display ships simultaneously.
- **Two findings I added:** the zero-result empty state misdirects users toward widening the time window,
  which cannot help when the field isn't indexed at all; and the `max-w-80` / uncapped-UUID space contest.
- **Where I pushed back:** removing cost from the row entirely (I demote rather than delete), and
  user-curated workspaces — I'd derive grouping automatically and let curation be a correction, since a
  hand-maintained mapping rots against a zero-config app that ingests sessions continuously.
- **Where it corrected me:** its principle *"do not confuse inactivity with completion"* catches a real flaw
  in my Concept 2 — I had labelled a lane "Finished today", which asserts completion the data doesn't
  support. Renamed to **"Last active today"** in this document and in both mockups. Its points about not
  live-resorting under the pointer, and about refusing drag-to-change-status (these are observations, not
  workflow state), are also correct and absent from my proposal.

**The two structural proposals compose rather than compete:** its workspace rail answers *where does this
work live* (scope); my attention lanes answer *what needs me next* (ordering within scope). A merged design
is a rail with derived grouping, scoping a board whose lanes run Waiting / Active / Inactive.

---

## Codex review — September 10, 2026

Review scope: this proposal, read-only examination of its linked HTML source, my independent live-app inspection, and the current session/search types. I did not perform a browser interaction review of these three linked mockups. These comments are appended without changing the author's proposal or review.

**Recommendation: adopt the hierarchy repair and outcome search first; refine the attention model before implementing the larger concepts.** UUID/title competition, repeated local-machine labels, sparse matrix cells, and inaccessible outcome text agree with my own inspection. The subagent roll-up is a useful addition to the smallest visual pass.

### 1. Activity labels: the correction is right, but apply it consistently

The updated proposal acknowledges that “Finished today” should be “Last active today.” That resolves the main Concept 2 labeling concern. Concept 3 still describes the **“41 finished ones”**, although the observed count is **41 Inactive**. An inactive session may be interrupted, abandoned, or unsuccessful.

`STATUSES` in `frontend/src/pages/coslash/lib/session.ts` distinguishes Active, Idle, Waiting, Inactive, and Unknown; it does not supply a completed-work state. Preserve Idle when present, and give the nine Unknown sessions an explicit destination in each grouping mode. A future Completed category needs separate evidence rather than an alias for inactivity.

### 2. Search relevance should outrank live activity during retrieval

Concept 2D pins live sessions above results “regardless of rank.” That works with an empty query, but can bury the historical session the user is looking for. An unrelated running session should not precede an exact title or outcome match.

Use recent/pinned/attention shortcuts before typing. Once there is a query, rank by match evidence, with recency as a tie-breaker. Label matched fields and show time scope. Direct resume should target the visibly selected result, respect launch eligibility, and clearly identify the destination agent/machine.

### 3. Attention lanes do not guarantee balanced populations

“Lanes are populated by construction, so no column is ever mostly blank” is stronger than the model supports. This dataset had zero Waiting sessions and a large inactive majority during my inspection. Needs you can be empty while Earlier remains long. Independent scrolling also creates several scroll targets and a keyboard-navigation tradeoff.

Test zero-waiting, one-active, many-inactive distributions. Scope the board to a workspace, keep fixed ordering/counts, define what empty lanes do, and avoid layout shifts on refresh. A list/search fallback remains useful for historical retrieval. Hatching large empty cells in the repaired matrix risks adding visual noise without adding useful information.

### 4. Broader search is a small change with a larger acceptance criterion

The current model contains nullable synthesis, an array of `goals`, an `outcome`, declared goals, and fallback summaries/first prompts. Searching loaded goal/outcome text is appropriately scoped. “Two lines” should not be treated as the acceptance criterion: pending/missing synthesis, normalized input, filtered result counts, all-time versus current-window scope, and visible match evidence still matter.

I agree with the reciprocal review that the Laticrete result matches the **repo field populated from a directory name**, not a direct `cwd` search. I have clarified that in my document. Broader indexing and match explanations should ship together. Artifact filenames are a useful later surface for finding work by its output, without implying transcript-wide or semantic coverage.

### 5. Glass can stay restrained; capability claims need validation

The fixed dark substrate and limited-blur recommendations are sound. Add reduced-motion and reduced-transparency fallbacks. The linked Mission Control source includes ambient drift, sweep, and pulse animations; I did not find a reduced-motion media query. A static background can provide the same visual character with less competition near text.

A context gauge reports measured usage; it cannot by itself predict the next compaction reliably. “Answer & resume” also assumes an input-delivery path to the original agent. The existing resume/handoff action does not establish that capability. Label both as future behavior requiring feasibility checks, and retain an explicit open-in-agent route.

Dark-only is a product choice, not an inherent requirement of glass styling. A light variant can preserve opaque reading surfaces and translucent chrome, although it needs its own visual checks.

### 6. How I would combine the two proposals

Take this proposal's **metadata demotion, subagent roll-up, outcome search, and Waiting shortcut**, plus the **workspace scope and adjacent preview** in [ui-ideas-codex.md](ui-ideas-codex.md).

I accept the argument for **automatic grouping by canonical repo identity first, with user aliases/collections as optional corrections**. For non-repository sessions, avoid pretending a generated parent directory is a meaningful project; retain an honest ungrouped/recent fallback and never merge by basename alone. Identity should stay `sourceId + agent + id`.

Cost visibility is a genuine product choice. A small, optional cost column can preserve spend comparison while keeping titles dominant. Test retrieval and spend-review tasks separately rather than resolving this solely from the existing header's prominence.

The adjacent panel in my mockups is a **short retrieval preview**, not the full inspector with diffs and token breakdowns. Its width floor can therefore differ from the full inspector's. Still, test it at 1280/1440/1600 with realistic long titles and use a drawer whenever the result list or preview falls below readable width. I would not choose a universal breakpoint before those tests.

Validate the combined direction using exact-title, remembered-outcome, duplicate-title, old-session, and remote-unknown retrieval tasks. Agreement between reviews increases confidence in prioritization; it is not a substitute for measured user performance.

---

## 5. v2 revisions — 2026-09-11

Two drivers: a direct request to move Concept 3 to the light theme so the three concepts can be compared on
equal ground, and the Codex review in §4 above.

### Concept 3 is now light

Dark was doing unearned work. A dark, glowing, animated page reads as "more designed" next to two light ones
regardless of whether its *structure* is better — which made the comparison useless for deciding anything.
Codex made the same point from the other direction: dark-only is a product choice, not a property of glass.
The light version keeps translucency in chrome, hero and live cards, and puts every text surface on a ground
of at least 72% opacity, so the three-weight hierarchy can be judged on its merits.

### Accepted from the review, and applied

| Review point | What changed |
|---|---|
| "Finished" asserts completion the data doesn't support | Concept 3's "the 41 finished ones" corrected to **41 Inactive**. `STATUSES` has no completed state; inactive may mean interrupted, abandoned or failed. |
| Idle and Unknown need explicit destinations | Both now exist in all three concepts — Idle as a real card state and lane, Unknown with its own group, band copy and row treatment stating that liveness is unreadable and these sessions are **not finished**. |
| Live sessions shouldn't outrank search relevance | ⌘K now pins live/recent shortcuts **only on an empty query**. Once there is a query, ranking is by match evidence with recency as tie-breaker. |
| Results should show why they matched | Every result carries a field label (`outcome`, `goal`, `repo`) and highlighted text. The Laticrete false-friend now explains itself as a generated folder-name match. |
| "Two lines of code" is not an acceptance criterion | The palette now discloses coverage gaps — sessions with pending or missing synthesis are listed as explicitly *not searched* — and states scope with one-click widening. |
| Lanes are not populated by construction | Correct, and my v1 claim was wrong: the observed dataset had **zero Waiting**, and v1 mocked a Waiting session as if it were typical. v2 defaults to the observed distribution, gives empty lanes defined behaviour and fixed position, and labels every illustrative element as illustrative. |
| Hatching empty cells adds noise, not information | Removed. |
| No `prefers-reduced-motion` | Added to all three. Concept 3's ambient field is now static by default; the sweep degrades to a solid rule and the pulse to a static ring. |
| A context gauge measures, it cannot predict compaction | Relabelled "26% ctx" with a measurement-only tooltip. The v1 claim that it "predicts a compaction before it happens" was wrong. |
| "Answer & resume" assumes an unproven input path | Now visibly marked as a future capability pending feasibility, with **Open in Claude Code** beside it as the route that works today. |
| Don't dress generated directories up as projects | Concept 3's rail derives repositories from canonical repo identity and puts the 26 sessions in folders like `ge/` and `wha/` under an honest **Ungrouped**. |
| Resume should name its destination | ⌘K's `⌘↵` now shows the target agent and machine and respects `resumeDisabled` rather than failing after the keystroke. |

### One consequence worth flagging

Giving Idle and Unknown their own lanes pushes Concept 2's board to **six lanes, which does not fit at
1280px**. Rather than clip, populated lanes take a 208px floor and share the remaining width while zero-count
lanes collapse to a 150px strip that keeps its position; beyond that the board scrolls horizontally. This is a
real cost of representing every status honestly, and it strengthens the case for Codex's workspace scoping —
six lanes across *one* workspace is far more comfortable than six across everything.

### Not accepted

**Removing cost from the row.** Still demoted rather than deleted, for the reason in §4: the $64.45 outlier
among $2 sessions is only findable if the number is on the row. v2 adds a **Spend** density mode so the
retrieval and spend-review jobs can be tested separately, which is what Codex asked for.

### Still unvalidated

Ranking weights between title, goal and outcome matches; whether artifact-filename indexing earns its cost;
keyboard navigation across independently scrolling lanes; and contrast on the rendered glass composite rather
than on token values. These need the task-based testing both reviews call for, not more design iteration.

---

## 6. Prototype rebuild — 2026-09-11

Milan's feedback: the concepts were design boards with commentary interleaved into the artefact, not
prototypes you could click through. Correct, and it mattered more than presentation — you can't evaluate a
retrieval design by reading about it. All three are now **standalone, self-contained, interactive** HTML
files with the rationale removed from the artefact and kept here instead.

**One finding came out of the feedback itself.** The 3px coloured spine on each card — my vendor
indicator — was circled with "what do these represent?". That is the answer: if the reader has to ask, the
encoding failed. Colour alone was carrying agent identity with no legend anywhere on the page. Agent is now
a readable tag (`Claude Code`, `Codex`) with the colour as a small swatch inside it, so the colour
reinforces a label rather than replacing one. This is a genuine accessibility improvement too — the spine
was the only agent signal, and it was pure hue.

Other changes in the rebuild:

- Commentary, before/after panels and annotation cards removed from all three artefacts.
- Totals derive from the dataset rather than being hardcoded, so the rail, spend card, scope line and group
  counts can never disagree.
- Empty states are reachable by interaction rather than described — filter Concept 3's rail to
  *Liveness unknown* and the hero degrades to its calm variant, because nothing in scope is waiting.
- `prefers-reduced-motion` is honoured throughout.
