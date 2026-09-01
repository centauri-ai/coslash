# T03 — Linux helper and SSH transport

Status: done

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

## Handoff

T03 handoff — 2026-09-01

Changed: `cmd/coslash-helper`, `internal/remotehelper` (+README),
`internal/remote/helperexec*.go`, `internal/remote/helperhealth.go`,
`internal/remoteprotocol/capabilities.go`, `internal/vendors/version.go`,
`internal/vendors/claude/family.go`, `internal/vendors/codex/family.go`, and the
`helper`/`helper-dist` Makefile targets.

Decisions:

- One `os.Root` handle on the SSH user's home is the only read primitive;
  symlinked directory entries are dropped and files open `O_NOFOLLOW` after an
  `lstat` that already rejected links. The allowlist matches the SFTP one.
- The remote command line is a validated, shell-quoted helper path plus one word
  from a closed set (`version`, `collect`); a `~/`-prefixed install renders as
  `"$HOME"/'<quoted>'`. The request only ever travels on stdin.
- Family grouping runs before any body opens: Claude groups by path, Codex by
  header parent chains. A family fingerprint digests its files' opaque keys,
  sizes, and mtimes plus the approved metadata facts for its sessions, so a
  liveness or name change still recollects.
- Changed families parse as one batch to preserve cross-file parser behaviour
  (Claude's background re-home collapse), then fall back to per-family parsing so
  one bad transcript is isolated. Family identity always comes from the grouping
  pass, so a cached family ID is always one the inventory can prove exists.
- Instability is a bounded retry then a `skipped_family` reason; the Mac keeps the
  last good facts.
- `vendor_complete` is emitted only after a scan that skipped nothing and hit no
  limit. A missing vendor root is complete coverage of zero families.
- Helper exit codes: 0 complete, 3 partial, 4 request rejected, 5 internal; the
  shell's 126/127 mean blocked or missing. Each maps to a distinct reason
  (`helper_missing`, `helper_not_executable`, `helper_incompatible`,
  `helper_failed`, `output_limit`).
- The transport applies records incrementally, continues a bounded drain through
  EOF after `request_complete` so trailing output is rejected, reaps before
  collecting the stdin write result, and terminates the SSH process group on
  abort, flood, timeout, or a child that stops writing without exiting.
- Codex changed-family facts carry bounded opaque file-header mappings. A warm
  request supplies them with the known baseline, allowing unchanged headers to
  be reused without reopening transcripts; overflow drops the whole baseline.
- Directory enumeration reads fixed-size batches and enforces the aggregate
  entry ceiling while reading, rather than allocating an unbounded directory.
- Codex prompt-derived fallback names are cleared at the helper-only adapter;
  approved metadata names remain available without sending prompt text.
- Helper digests cover the binaries, not archives, since installation verifies the
  exact executable it places and runs.

Focused tests:

- `go test ./internal/remotehelper ./internal/remote ./internal/remoteprotocol
  ./internal/remotefacts ./internal/vendors/codex ./cmd/coslash-helper` — passed;
  committed regression tests cover success, incompatibility, malformed/truncated
  and trailing output, stdout/stderr floods, resource/exit classification, hung
  child, cancellation, injection, bounded directory reads, warm Codex header
  reuse, exact one-line requests, and prompt-name exclusion.
- `go test ./internal/collector` — passed (one adjacent package; `vendors` gained
  exported symbols).
- `go test -race ./internal/remote ./internal/remotehelper` — passed. Run early
  rather than in T06 because the transport is the only new concurrency: one
  short run confirmed no data race or goroutine deadlock in the stdin/stdout/
  stderr drain.
- `gofmt -l .` — clean; `go vet` — clean.
- `make helper-dist` — reproducible `linux/amd64` and `linux/arm64` binaries,
  `CGO_ENABLED=0`, statically linked; a rebuild produced identical digests.
- End-to-end smoke with the real binary over a fixture home: cold collect
  published both families in 3.3 KiB. The committed warm-header regression uses
  a deliberately invalid on-disk header and proves the matching cached mapping
  is reused without opening it.
- Regression tests are committed with the implementation so the transport and
  helper safety boundaries remain independently reproducible.

Remaining blocker/risk: none for T04. Discovered scope recorded in the master
plan change log for T02/T05/T06.
