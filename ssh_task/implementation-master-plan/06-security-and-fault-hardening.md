# T06 — Security and fault hardening

Status: not_started

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
