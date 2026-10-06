# A session is source plus agent plus session ID, and its revision covers parsed content only

- Status: Accepted
- Date: 2026-08-17
- Source: 7135f055, cf23c4f6, 719e81e8, f6452654, bc0a981e

## Decision

- The identity of a session is the triple of source ID, agent, and the session ID of the vendor. coSlash keys, deduplicates, selects, caches, routes, and reconciles sessions by this triple. A display label, a basename, or a session ID alone is never an identity.
- The source ID is opaque and persisted. It never comes from an SSH alias, a host name, a user name, or a path. Renaming a configured remote does not change any session identity.
- The vendor session ID stays unchanged inside the triple. Resume needs the original value.
- The browser source summary never contains host names, user names, absolute paths, prompts, commands, or transcripts. SSH sources show a fixed, safe label.
- The revision of a session is a SHA-256 fingerprint of its parsed content. Live overlays never change it. Process state, repository and Git facts, filesystem probes, review state, synthesis, subagent status, and collection-time fallbacks are overlays.
- Exact detail and revision generation use the same parsed session family. An action that depends on a revision (detail, diff, preview, share, review) uses the exact revision that the user saw or approved.
- File-change bodies are reachable only through opaque change IDs that belong to the selected revision. A display path is never a file to open.

## Context

Two machines can hold the same agent session ID. Without the source in the key, a remote session overwrites a local one in lists, synthesis caches, and selections.

An SSH alias is configuration input and can contain a host name or a user name (comment in `collector/cmd/coslash`). An identity built from it leaks those values to the browser and changes when the user renames the remote.

A revision binds consent, previews, and cached detail to specific content. If a live fact such as "process running" or "branch dirty" changes the revision, every poll invalidates approvals and caches. A share then never matches the revision that the user reviewed.

If someone "simplifies" this design, these failures come back:

- Keying by session ID alone merges sessions from different machines.
- Hashing the whole `session.Session` value makes revisions change on every refresh.
- Resolving a diff by path lets a crafted transcript name a file outside the session.

## Alternatives rejected

- Replacing the vendor session ID with a composite ID. Resume needs the original ID.

## Consequences

- Every new cache, API route, and frontend store must carry all three parts of the identity.
- A new session field needs an explicit choice: portable content (in the revision) or local overlay (outside it). [`collector/AGENTS.md`](../../collector/AGENTS.md) describes the end-to-end check for a new field.
- A shared vendor session ID across sources is not proof of one session. One real duplicate is known: a Claude Desktop SSH session has a Mac-side mirror transcript. Any deduplication across sources must use vendor evidence of a mirror and must keep distinct sessions that share an ID.
- Contracts: [`collector/fullsession/v1/README.md`](../../collector/fullsession/v1/README.md) and [`collector/snapshot/v1/README.md`](../../collector/snapshot/v1/README.md).
- Enforcement: the source-aware API in `collector/cmd/coslash`, `collector/internal/session`, `collector/internal/fullsessionrecord`, `collector/internal/review`, and `sessionKey`, `sessionLogicalId`, and `sessionRevision` in `frontend/src/pages/coslash/lib/session.ts`.
