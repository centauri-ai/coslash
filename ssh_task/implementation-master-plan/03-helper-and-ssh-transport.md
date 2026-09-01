# T03 — Linux helper and SSH transport

Status: not_started

Depends on: T01

## Objective

Build the stateless Linux collector and bounded Mac-side SSH exec transport as
one end-to-end implementation of the T01 protocol.

## Context

The helper runs as the SSH user, owns a fixed allowlist, persists no transcripts,
has no daemon/network listener, and emits protocol only on stdout. The Mac uses
the system SSH client and existing ControlMaster behavior, treats all output as
untrusted, and commits only the protocol accumulator's proposed generation.

## Scope

### Linux helper

- Add `version/capabilities` and `collect` commands around existing Claude/Codex
  parsers; do not fork parser logic.
- Decode one bounded request and stream normalized family records.
- Discover only fixed roots beneath the SSH user's home plus narrow liveness
  probes; never open a request key as a path.
- Use race-resistant no-follow traversal for regular files.
- Isolate vendor/family failures and enforce request/output/file/entry/depth/
  duration limits.
- Detect files changing during parse and emit unstable state after bounded retry.
- Emit vendor completion only after authoritative enumeration.
- Provide reproducible Linux `amd64`/`arm64` builds, preferring
  `CGO_ENABLED=0` when verified.

### Mac SSH exec client

- Build fixed capability/collect argv separately from SFTP argv; interpolate no
  request data into a shell command.
- Reuse validated aliases and ControlMaster behavior.
- Bound serialized stdin and drain capped stdout/stderr concurrently.
- Parse records incrementally into the T01 accumulator.
- Terminate SSH pipes/process group on timeout, cancellation, or output flood
  without goroutine leaks.
- Return distinct SSH/helper/protocol/resource reasons plus safe timing/byte
  diagnostics.
- Accept overlapping compatible ranges; helper/Mac build strings need not match.

## Acceptance criteria

- Helper facts match local normalized fixtures, including large-file shape.
- No transcript body enters stdout, stderr, cache input, or diagnostics.
- Fake-process tests cover success, incompatibility, malformed/truncated output,
  stdout/stderr floods, nonzero exit, hung child, and cancellation.
- No partial record mutates cache state; cleanup is deterministic.
- Alias/path/request inputs cannot become option, shell, or path injection.

## Focused tests

Run helper/protocol adapter and exec transport package tests. Build the native
Linux helper while iterating and cross-build the second architecture once before
handoff. Use fake processes, not a real SSH host.

## Out of scope

No managed artifact transfer, lifecycle UI, manager preference policy, or full
host benchmark.
