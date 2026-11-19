# Session decisions carry cite-checked evidence

- Status: Accepted
- Date: 2026-11-18
- Source: `collector/internal/synthesis`, `TestStructuredDecisionsRemainLocalEnrichment`, `TestSynthesisIsRevisionBoundWithoutChangingPortableRecord`

## Decision

A decision is a lasting choice, not a task step, finding, chore, or release sequence. New local summaries carry a versioned structured list with reasons, named alternatives, and short timeline quotes. The model may propose a claim; code alone establishes whether its quote appears in the selected, bounded timeline input. A citation verifies presence, not semantic support. The **You** mark also requires a quote from a user description or question answer.

Evidence refers to a full digest entry index, with Pi source-entry identity when available. Merge calls refer to partial decisions by ID rather than generating new quotes. Matching uses the same selected text and limits as source prompts, so a compaction seed, hidden branch, or unseen recap tail cannot become evidence.

Uncited decisions remain visible, dimmed and without a link. Only cited decisions populate `keyDecisions` and handoffs. A validated edited Markdown path can identify a design document; such summaries retain three choices rather than five. Legacy caches keep their original behavior until a new summary is produced. Prompt changes do not trigger backfill.

The new fields are local enrichment. `internal/fullsessionrecord/adapter.go` maps only the existing four synthesis fields, and `internal/sessionbackupproducer` retains that same spool artifact shape. Structured decisions never change portable revisions or upload content. See [session identity and revisions](session-identity-and-revisions.md) and [bounded, opt-in synthesis](synthesis-opt-in-bounded-facts.md).

## Why

Unstructured extraction mixed lasting choices with transient work and gave readers no way to check its claims. A small structured list supports reading, handoffs, and future search without claiming a decision lifecycle or semantic verification that code cannot establish.

## Alternatives rejected

- Treating every model quote or attribution as verified. A model can cite the wrong turn, hidden context, or agent text.
- Asking merges to copy evidence anew. Reference-based merges preserve the original evidence through repeated rounds.
- Uploading the local overlay or rewriting old caches. Both would expand the consent and compatibility scope beyond this change.

## Consequences

- The prompt asks for 4–20-word quotes to keep output concise. The presence check requires at least four words and enforces the stored 160-rune limit; a longer literal match within that limit remains cited. See `TestEvidenceQuoteWordCountBounds`.
- Evidence uses the positive turn labels actually rendered in the selected digest. The aggregate session turn counter can be lower than those labels, so it cannot bound citations; absent or unselected turns still fail the check. See `TestEvidenceUsesRenderedTurnLabelsWhenAggregateCountDiffers`.
- Quote matching can miss valid paraphrases; those claims remain visible but are excluded from downstream decision lists.
- A real quote can still be attached to an unsupported claim. Citation checks establish text presence, not semantic support for the decision or its reason.
- Local and Hub can produce different summaries for the same session because each produces its own synthesis.
