# Local search is derived private state outside session revisions

- Status: Accepted
- Date: 2026-11-19
- Sources: `collector/internal/search`, `collector/cmd/coslash/search_index.go`, `collector/internal/remote/search.go`, `TestSearchRefreshInterruptedPassRetainsRows`.

## Decision

Local search uses an FTS5 index under `COSLASH_HOME/search/`. It needs no model,
API key, or synthesis run. Identity is always source, agent, and session ID.
Parsed content and mutable overlays have independent update keys: newly available
synthesis and commit subjects update search without rewriting prompt units.
Only a completed inventory can establish deletion. Interrupted passes preserve
last-good rows, and a lower-coverage representation cannot replace richer content
at the same revision.

Full prompt and question text is private enrichment: at most 8 KiB per digest
entry and 128 KiB per session, preserving earliest entries first. It survives
local parse caching but never enters JSON APIs, portable records, synthesis
prompts, or revision hashes. Search responses expose only bounded matching
snippets. See [session identity and revisions](session-identity-and-revisions.md).

SSH indexing reads only complete stored full records from a complete live view;
it never indexes the stripped startup snapshot or adds remote text that was not
already persisted. Remote sessions have no decision synthesis. A stale or
incomplete refresh retains previously indexed rows until a complete inventory
can replace them.

Decision records are Local enrichment. Their `cited` flag means term overlap
with the rendered synthesis input, not semantic verification. Proposed and
superseded records receive lower search weights and are excluded from settled
decision summaries. Legacy string decisions remain searchable without citations.

## Consequences

Search covers all time while a content query is active. The browser applies
facets to the matching set before display limits; the CLI applies supported
filters before its limit. Readiness is explicit through `/api/search/status`.
The index can be deleted with Local stopped and rebuilt on next startup. A
schema mismatch rebuilds it after closing the old database. Search does not
upload this derived state or spend tokens to regenerate old synthesis results.

The initial ranking weights and decision budget are provisional. Synthetic
regression tests provide deterministic coverage; private recall and decision
quality measurements remain required before claiming the design's launch gates.
