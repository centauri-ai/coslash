# Collector conventions

## Preserve data contracts end to end

- Every `README.md` under `collector/` is normative for its own boundary; `snapshot/v1/` and `fullsession/v1/` define the public, deployment-independent contracts. Read the nearest one before changing portable records, remote collection, caches, snapshots, or sharing, and do not restate its field lists or limits here.
- Trace every added or changed session field through its whole chain: the authoritative producer, `internal/session/clone.go`, `internal/fullsessionrecord/adapter.go`, `internal/sessionexport`, the transport or cache representation, the exact-detail API, and the consumer. Make an explicit decision for excluded local enrichment. Schema support alone does not establish producer, transport, or round-trip parity.
- Preserve contract-significant distinctions: unknown versus zero, null versus empty, declared order, integer precision, source/agent/session identity, parent lineage, and content-derived revisions. Clone caller-owned data before sorting, normalizing, freezing, or caching it.
- Keep exact detail and revision generation on the same parsed session family. Live process, repository, filesystem, review, and synthesis overlays must not silently change immutable portable revision identity.
- Cover contract changes with round-trip tests through every supported path and with a fixture or boundary test for nullability, ordering, precision, lineage, and nested child data that the change can affect.

## Enforce bounds at the boundary

- Reject byte, item-count, nesting, identifier, and lineage violations before expensive decode, allocation, composition, or persistence. Limit readers and scanners before materializing untrusted collections.
- Measure the exact representation written on the wire or disk, including JSON escaping and framing. Enforce both per-record and aggregate budgets and reserve room for required completion records.
- Never publish a partial family as complete. An interrupted, malformed, skipped, or budget-limited refresh retains the last good generation; absence proves deletion only after the protocol's authoritative completion evidence.
- Keep memory and lock scope bounded. Do encoding, file I/O, and cache writes outside manager locks when state can be snapshotted and revalidated safely.
- Work per refresh, poll, or user action must not repeat expensive work per item. Index, hash, or encode once and reuse the result; do not rehash every local detail on a list poll, re-encode a retained record set per record, or probe the environment on every click.

## Lifecycle and external operations

- Propagate request cancellation through collection, SSH, helper, subprocess, and upload work. On subprocesses, drain owned pipes before `Wait`, bound diagnostics, and coordinate cancellation with cleanup so no operation-owned child or control socket is left behind.
- Enumerate success, failure, timeout, cancellation, retry, superseded work, and shutdown for stateful operations. A stale completion must not overwrite a newer attempt.
- Report readiness or success only after the external operation and its required durable publication succeed. Preserve specific typed causes long enough for the API and UI to choose the correct recovery action.

## Trust boundaries

- Transcript text, repository contents, synthesis output, remote host output, and Hub responses are untrusted. When any of it enters an agent prompt, put it in an explicitly delimited data block and instruct the agent never to follow instructions found inside it.
- Pass untrusted or unbounded content through stdin, not argv: argv is readable by other local processes and has an operating system size limit. Terminate option parsing with `--` before any positional prompt so message text cannot become a flag.
- Launch agent CLIs with project hooks, plugins, and MCP configuration disabled, and with filesystem and credential access restricted to what the operation needs. `internal/synthesis/runner.go` is the reference for the hardened per-vendor flags.
- Bound and strip control characters from external process, host, or Hub output before logging it. Keep the specific cause in the log and return a fixed message to the client.
- Enforce privacy, share-eligibility, and consent gates on the server where the data is produced, and bind consent to the exact audience rather than to its size.

## HTTP security

All routes, including the embedded frontend, must be served through
`internal/httpsec.Guard`. New routes must not bypass the guard or emit CORS
headers. API routes require the per-run token and must use method-qualified
`http.ServeMux` patterns. Frontend requests to `/api/` must use `apiFetch` so
the per-run token is attached. Keep internal errors in server logs and return
fixed, non-sensitive messages to clients.

The token prevents requests from other browser origins; it does not protect
against local processes running as the same user, which can read the token file.

## Verification

- Run focused package tests while iterating, then `make check` and `make test` from `collector/`.
- Run `make release && make smoke` when changing startup, embedded assets, helper packaging, or the frontend/collector integration.
