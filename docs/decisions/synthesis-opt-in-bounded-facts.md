# Synthesis is opt-in and reads only bounded derived facts

- Status: Accepted
- Date: 2026-09-04
- Source: ef151e9b, 98e164e0

## Decision

- AI synthesis is off until the user turns it on and saves Settings. A profile without `settings.json` never starts a synthesis run, and the unsaved Settings view shows synthesis as off.
- Synthesis input is a bounded set of normalized facts that coSlash derives from the parsed session. It is never the raw transcript.
- Every synthesis prompt has a fixed byte budget. When the facts exceed the budget, coSlash summarizes chronological, turn-aligned chunks and then merges the partial results. It does not drop older history only because it is old.
- A user request, its answer, and the resulting recap stay in one selection unit. A chunk boundary never splits them.
- Session facts and partial syntheses enter the prompt inside explicit `BEGIN UNTRUSTED ...` and `END UNTRUSTED ...` blocks with an instruction never to follow text inside them.
- When no synthesis runner is configured, coSlash does no synthesis collection work at startup.
- Synthesis results and synthesis cost accounting are local derived state. They never change portable session records, revisions, snapshots, or backups.

## Context

Synthesis runs through the CLI account of the user, so each run spends the money and quota of the user. In `v0.0.3-rc.1`, a pristine profile showed synthesis as enabled with a default backend and model. A user who opened Settings and saved for another reason then turned on paid runs without an explicit choice.

The first prompt builder kept only the newest 40 digest events and then cut the prompt at 12,000 bytes. In a 24-hour session, this removed a user message but kept its recap. Dense digest text also pushed out todos, files, commits, and stats. The synthesis then described only the end of the session.

Startup enumerated the sessions of the day before the manager discovered that no runner existed. This work had no purpose for users who never enabled synthesis.

If someone "simplifies" this design, these failures come back:

- A default of "on" spends user quota without consent.
- A single truncated prompt loses early goals, corrections, and blockers in long sessions.
- Raw transcript text in the prompt removes the size bound and lets planted instructions reach the model as instructions.

## Alternatives rejected

- Recency-only selection (the newest N events, then a byte cut). It silently drops older history and splits turns.
- Cumulative token usage as the measure of session size. It counts repeated input and cache traffic, not unique information.
- Incremental rolling synthesis as a required design. It is one candidate implementation. The requirement is chronological coverage, not a specific mechanism.

## Consequences

- New users see only deterministic debriefs until they choose a backend and model and save.
- Long sessions cost more than one synthesis call per revision. Work stays bounded per revision (98e164e0).
- The synthesis can miss detail that exists only in raw transcript rows. This is accepted. Parsed session events stay the source of truth.
- Enforcement: the default lives in `collector/internal/settings` and `frontend/src/pages/coslash/lib/settings.ts`. Prompt budgets, chunking, and untrusted blocks live in `collector/internal/synthesis`. The launch flags for synthesis runs follow [hardened-agent-launches](hardened-agent-launches.md).
- The privacy statement for synthesis is in [`docs/data-and-privacy.md`](../data-and-privacy.md). Keep it in step with this decision.
