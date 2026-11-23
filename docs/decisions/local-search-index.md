# Local search is derived private state outside session revisions

- Status: Accepted
- Date: 2026-11-19
- Sources: `collector/internal/search`, `collector/cmd/coslash/search_index.go`, `collector/internal/remote/search.go`, `TestSearchRefreshInterruptedPassRetainsRows`.

## Decision

Local search uses an FTS5 index under `COSLASH_HOME/search/`. It needs no model,
API key, or synthesis run. Identity is always source, agent, and session ID.
Parsed content and mutable overlays have independent update keys: newly available
synthesis and commit subjects update search without rewriting prompt units.
Location labels, roots, working directories, and repository names are metadata
overlays too, matching the browser's location search fields. The browser applies
`worktree:` filters to locations; the index strips that prefix before full-text
matching (`TestLocationMetadataUpdatesAsSearchOverlay`, `TestQuery`).
Only a completed inventory can establish deletion. Interrupted passes preserve
last-good rows, and a lower-coverage representation cannot replace richer content
at the same revision. A session that fails to index keeps its last-good rows
without failing the pass, and once a pass has completed, a failed pass leaves
search ready on those rows.

Full prompt and question text is private enrichment: at most 8 KiB per digest
entry and 128 KiB per session, preserving earliest entries first. It survives
local parse caching but never enters JSON APIs, portable records, synthesis
prompts, or revision hashes. Search responses expose only bounded matching
snippets. Adding this text bumped each vendor `ParserVersion`, so parse caches
written without it are reparsed. See
[session identity and revisions](session-identity-and-revisions.md).

SSH indexing reads only complete stored full records from a complete live view;
it never indexes the stripped startup snapshot or adds remote text that was not
already persisted. Remote sessions have no decision synthesis. A stale or
incomplete refresh retains previously indexed rows until a complete inventory
can replace them. Indexing uses the history window the session list already
fetched and never widens it, so it causes no extra remote fetches or stored
records. A view over a limited window proves deletion only for sessions active
within that window.

Search consumes versioned `SessionSynthesis.Decisions`, including uncited choices,
through `collector/internal/search/decisions.go`. It preserves decision IDs,
reasons, alternatives, and producer-owned `cited` flags. Cited evidence supplies
distinct turn labels; `ByYou` maps to `decidedBy: user` only when cited, and no
other attribution is inferred. Uncited choices remain searchable at lower weight.
See [cite-checked decisions](session-decisions-cite-checked-evidence.md).

A versioned empty list is authoritative. Only unversioned legacy summaries fall
back to `KeyDecisions` strings, with no citation or attribution. The producer has
no decision lifecycle, so search reports none. Search never extracts decisions,
validates evidence, changes synthesis prompts, or regenerates summaries. The
overlay key hashes the normalized decision content and metadata, so changes to
citations or rationale refresh decision units without rewriting prompts. These
boundaries are pinned by
`TestDecisionsAdapterUsesStructuredProducerMetadata` and
`TestStructuredDecisionOverlayUpdatesAndSearchesReasons`.

## Relevance and decision eligibility

Session retrieval keeps OR matching so a partial match can still help discovery.
Decision cards make a stronger claim: every content term must match within that
one decision's text, reason, or alternative, using the same FTS tokenizer and
stemming as retrieval. Terms spread across other parts of its session do not
qualify a card. A citation is evidence presence, not query relevance. See
`TestDecisionCardsRequireEveryTermInSameUnit` and
`TestDecisionCardTermsUseFTSPhrasesStemmingAndRationale`.

Adjacent query words in a title receive a 1.5× title-score boost. Without that
signal, two equally short titles containing the same words tie, and incidental
prompt or recap matches can put a scattered title above the direct phrase.
Single-word queries retain their previous weighting. See
`TestTitlePhraseOutranksScatteredTitleWithBodyBonus`. Ranking remains keyword
matching, not a semantic judgment; recency breaks score ties only.

## Consequences

Search covers all time while a content query is active. The browser applies
facets to the matching set before display limits; the CLI applies supported
filters before its limit. Readiness is explicit through `/api/search/status`.
The index can be deleted with Local stopped and rebuilt on next startup. A
schema mismatch rebuilds it after closing the old database. Search does not
upload this derived state or spend tokens to regenerate old synthesis results.

The initial ranking weights are provisional. Synthetic regression tests provide
deterministic coverage; private recall measurements remain required before
claiming the search design's launch gate. Decision extraction and quality
evaluation belong to the separate decision-quality implementation.
