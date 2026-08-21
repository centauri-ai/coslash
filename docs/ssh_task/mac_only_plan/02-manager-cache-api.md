# P2 — Remote collection manager, cache, and API

Status: done locally on 2026-08-21.

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

## Implemented locally

- The manager opens the read-only P1 SFTP transport and runs Claude/Codex
  discovery and parsing on the Mac; it invokes no Linux coSlash command.
- One-flight refresh, cancellation, bounded backoff, manual retry, stale
  last-good display, disable/remove, alias replacement, and shutdown are tested.
- The atomic cache stores a narrow session projection, opaque file
  fingerprints, coverage, and truncation. A regression test proves transcript
  text, commands, tool output, working directories, and edited-file paths do
  not reach disk.
- The UI opts into a source-aware sessions envelope with machine health and a
  separate remote history cutoff. The default endpoint retains its legacy local
  session array for existing callers. SFTP test/retry and remote-only action
  attempts return stable health and error contracts.
- Connection testing runs bounded discovery and parsing, distinguishing SSH
  authentication, host-key verification, SFTP availability, permissions, empty
  roots, partial agent data, and ready state.
- Diagnostics include bounded, redacted remote health without collector or
  Linux installation fields.

Verification completed locally: `gofmt`, `go vet ./...`, `go test ./...`, and
`go test -race ./internal/remote/...`.
