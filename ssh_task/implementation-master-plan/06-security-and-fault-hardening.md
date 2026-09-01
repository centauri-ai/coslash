# T06 — Security and fault hardening

Status: done

Depends on: T02–T05

## Objective

Test the completed boundaries against malicious output, filesystem races,
interrupted lifecycle operations, changing source files, and resource exhaustion;
close findings before the full validation gate.

## Threats in scope

- Compromised remote account producing malicious protocol/stderr data.
- Replaced, tampered, revoked, or unexpected helper executable.
- Symlink/path replacement during reads and lifecycle operations.
- Request keys used as paths; shell/argv/path injection; unsafe ownership/modes.
- Infinite, oversized, or deeply nested NDJSON; replay, duplicate, conflict.
- Process/goroutine leaks, pipe deadlock, cancellation, and timeout.
- Transcript append/truncate/replace/disappear during scan/parse.
- Partial enumeration causing false deletion.
- Cache corruption or interrupted atomic commit.

## Scope

- Write/update a concise threat model with trust boundaries and residual risks.
- Add targeted negative tests and fault injection for protocol, exec, helper
  filesystem, lifecycle, cache, and manager boundaries.
- Add bounded fuzz targets for untrusted decoders and short race tests for
  concurrency-sensitive code.
- Verify logs, metrics, telemetry, diagnostics, protocol, and cache contain no
  disallowed session content or path data.
- Verify every resource limit fails closed while retaining last good cache state.
- Record accepted residual risks with an owner and rationale.

## Acceptance criteria

- Every listed threat has a tested mitigation or approved residual-risk entry.
- Hostile/incomplete input commits no unauthorized tombstone or partial record.
- Tested cancellation/flood paths leak no child process or goroutine.
- Installer/uninstaller touches only validated exact targets.
- Privacy inspection finds no transcript rows, prompts, session names, paths,
  credentials, raw stderr, or raw protocol content in observability output.

## Focused tests

Run only new negative/fault cases, short bounded fuzz runs, and race tests for the
specific concurrency-sensitive packages. Sustained fuzzing and full suites stay
in T07.

## Out of scope

No unrelated application security review or SSH server hardening.

## Handoff

T06 handoff — 2026-09-01

Changed: `internal/remotefacts`, `internal/remoteprotocol`, `internal/remote`,
`internal/remotehelper`, and [t06-threat-model.md](t06-threat-model.md).

Decisions: complete NDJSON requires `request_complete`; cacheable stale state is
a fixed content-free code rather than remote prose; control-master output has
the same bounded/cancelled behavior as collection; malformed SFTP directory
entry names fail closed before path construction. The SFTP final-path TOCTOU
gap is recorded as an accepted, owner-assigned residual risk.

Focused tests (all passed):

```sh
GOCACHE=/tmp/coslash-t06-go-cache go test -count=1 \
  ./internal/remotefacts ./internal/remoteprotocol ./internal/remote \
  ./internal/remotehelper ./internal/diagnostics
GOCACHE=/tmp/coslash-t06-go-cache go test -run '^$' \
  -fuzz '^FuzzDecode$' -fuzztime=2s ./internal/remoteprotocol
GOCACHE=/tmp/coslash-t06-go-cache go test -run '^$' \
  -fuzz '^FuzzDecodeCapabilities$' -fuzztime=2s ./internal/remoteprotocol
GOCACHE=/tmp/coslash-t06-go-cache go test -race \
  -run 'TestRunSSHCommand(KillsProcessGroupOnOutputFlood|BoundsControlOutput)$|TestHelperCollect(CleansUpChildThatHangsAfterCompletion|HonorsCancellation)$|TestReadDirCacheAvoidsRedundantValidation' \
  ./internal/remote
CGO_ENABLED=0 GOCACHE=/tmp/coslash-t06-go-cache \
  go build -o /tmp/coslash-helper-t06 ./cmd/coslash-helper
git diff --check
```

Remaining blocker/risk: production signing material and artifact publication
remain T07 rollout inputs; accepted SFTP protocol race limitation is in the
threat model.
