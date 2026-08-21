# P2 — Remote collection manager, cache, and API

## Purpose

Replace the Linux snapshot/probe runner with P1's Mac-side SFTP collector while
preserving source identity, background refresh, last-good behavior, and local API
compatibility.

## Required outcome

- Adapt one optional remote setting with stable source ID, label/alias, and enabled
  state; existing settings migration remains backward compatible.
- Replace framed-command probe/snapshot logic with SFTP connect, root discovery,
  capability inspection, inventory, and local parsing.
- Keep at most one refresh in flight with cancellation, bounded retry/backoff,
  manual retry, disable/remove semantics, and app shutdown.
- Persist only normalized session facts, fingerprints, coverage, truncation, and
  bounded health. Never persist raw transcript bytes or absolute remote paths.
- Preserve complete source-aware session keys and local/remote ID collision safety.
- Revise machine health to describe connection, permission, missing agent data,
  partial coverage, stale fallback, and unsupported SFTP—not collector versions.
- Keep remote diff, synthesis, preview, sharing, and launch routes unsupported with
  stable errors. Keep Copy handoff client-side.

## Scope

Expected ownership is settings/schema, remote manager/cache, collector integration,
HTTP API, diagnostics facts, and focused tests. Do not implement frontend changes
or delete old Linux surfaces until P4.

## Acceptance

- Existing local API behavior and omitted-source defaults remain unchanged.
- Connect/test distinguishes authentication/host-key failure, missing SFTP,
  permission denied, no supported data, partial agent availability, and ready.
- Failed, canceled, incomplete, or oversized refreshes never corrupt the last-good
  cache; incomplete successful data is visibly marked and excluded from aggregates
  according to the existing policy.
- Settings edit/remove cannot relabel old host data or delete outside the one source
  cache directory.
- Tests prove cache files contain no transcript messages, prompts, tool output,
  remote absolute paths, or SSH configuration.
- Race tests cover refresh, retry, settings replacement, removal, and shutdown.

## Verification

From `collector/`:

```text
gofmt -l ./cmd ./internal
go vet ./...
go test ./...
go test -race ./internal/remote/...
```

## Handoff to P3

Freeze the session envelope, machine health states/reasons, settings/test/retry
requests, unsupported action codes, and copy for partial/unknown facts.
