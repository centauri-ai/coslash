# T01 — Contracts, metrics, and fixtures

Status: done

## Objective

Establish the transport-independent family contract, bounded NDJSON protocol,
safe merge state machine, deterministic fixtures, and baseline measurements that
all later implementation uses.

## Context

The benchmark workload contains 131 Claude files/92.57 MiB and 296 Codex
files/1,127.3 MiB, including a 135.73 MiB transcript. Current SFTP repeatedly
reads Codex headers and transfers selected bodies. `vendors.ParsedSession`
contains composition inputs, while the current remote cache persists narrow,
already-composed roots.

Read:

- `../mix-plan/ssh-helper-design-plan.md`
- `../mix-plan/workload-context-for-debug.md`
- `collector/internal/vendors/parsed.go`
- `collector/internal/collector/collector.go`
- `collector/internal/remote/cache.go`

## Scope

### Metrics and fixtures

- Create privacy-safe fixture manifests for cold, unchanged, one appended family,
  malformed, oversized, disappearing-file, partial, and incomplete-scan cases.
- Record candidate/selected families, metadata/header/body bytes, operation
  counts, parser/total duration, normalized response size, hardware, filesystem,
  SSH RTT, build, requested window, and limits.
- Provide one reproducible narrow measurement command without committing private
  transcript content.

### Normalized family schema

- Define versioned family/session/file identities, parent/spawn relationships,
  bounded display/count/model/token/cost/status facts, approved metadata,
  fingerprints, and completeness/stale state.
- Specify required/optional fields, syntax, ranges, lengths, counts, nesting,
  ordering, and deterministic truncation.
- Exclude raw rows, prompts, unrestricted tool output, absolute paths,
  environment values, working directories, edited paths, and raw commands.
- Add adapters to existing shared composition without exposing transport types.

### Protocol and state machine

- Define bounded request, handshake, changed/unchanged/skipped family,
  provisional tombstone, vendor-complete, and request-complete NDJSON records.
- Define the known-fingerprint request ceiling and overflow behavior. Protocol v1
  uses an explicit `baseline_mode=none` bounded recollection when fingerprints do
  not fit; omitted fingerprints never mean missing or deleted families. A
  baseline-free response may authorize deletion only by returning a bounded
  authoritative family inventory after complete vendor enumeration. If that
  inventory does not fit, it reports partial coverage and commits no tombstones.
- Include request/sequence identity, applicable vendor, baseline identity,
  parser/schema versions, capabilities, counts, and timing.
- Negotiate version ranges rather than exact build equality.
- Treat fingerprint keys as comparison data only, never paths.
- Implement a pure accumulator that produces a proposed cache generation.
- Allow a validated changed family to publish; retain last good facts on failed
  replacement; authorize tombstones only after complete vendor enumeration.
- Reject mixed/replayed requests, stale baselines, conflicts, duplicate actions,
  unknown required versions, exceeded limits, and trailing content.

## Acceptance criteria

- Local, SFTP-shaped, and helper-shaped fixtures compose equivalent root cards.
- The field privacy allowlist is explicit and reviewable.
- Interruption before vendor completion commits no deletion.
- Fixtures cover partial response, unstable file, stale baseline, conflict,
  deletion, and narrower/broader windows.
- Baseline measurements are reproducible and distinguish metadata/header/body
  cost by vendor.
- T01 publishes proposed cold, warm, changed-family, first-result, request-size,
  response-size, and resource targets for T07, with the measurement evidence and
  environment used to derive them.
- A fingerprint set larger than the request limit has a deterministic fixture and
  cannot cause deletion or unbounded input/output.
- Contract/protocol packages perform no SSH, filesystem, or durable cache I/O.

## Proposed T07 targets

These are initial gates for the synthetic fixture harness, not claims about the
real-host benchmark. T07 should replace them only with T01-approved measurements
from the real workload and record the environment alongside each result.

| Target | Initial value | Evidence/interpretation |
| --- | ---: | --- |
| Cold normalized response | ≤ 32 MiB | Protocol ceiling; fixture response is 1.6 KiB per cold vendor observation. |
| Warm unchanged body bytes | 0 | Both unchanged fixture observations transfer no body bytes. |
| Known-fingerprint request | ≤ 256 KiB | Protocol ceiling; overflow switches atomically to `baseline_mode=none`. |
| Per-record normalized facts | ≤ 1 MiB | Protocol ceiling; raw transcript content is never a record field. |
| First result | ≤ configured collection deadline | Fixture maximum is 7 ms; real-host value remains to be measured. |
| Incomplete refresh deletion | 0 tombstones committed | Accumulator tests prove deletion requires vendor completion and inventory. |

## Handoff

T01 handoff — 2026-09-01

Changed: `internal/remotefacts`, `internal/remoteprotocol`,
`internal/remotemetrics`, `internal/collector/remote_facts_test.go`, and the
T01 contract documentation.

Decisions: transport-neutral normalized family replacements; bounded strict
NDJSON; atomic no-baseline overflow; path-free opaque identities; changed-family
prior fingerprints must match the cached family, with empty prior for new
families when a known baseline is used, while baseline-free replacements omit
the unavailable prior; provisional tombstones require authoritative vendor inventory; skipped
families retain immutable last-good facts under a transient stale overlay;
proposal baseline identity advances to the request ID; family membership is one
tree rooted at `family_id`; requested-vendor lifecycle is enforced.

Focused tests: `go test ./internal/remotefacts ./internal/remoteprotocol
./internal/remotemetrics ./internal/collector` — passed. Measurement command:
`go run ./internal/remotemetrics/cmd/measure --manifest
internal/remotemetrics/testdata/fixture-manifest.json` — passed; synthetic
totals are 16 observations, 990 metadata bytes, 800 header bytes, 15,600 body
bytes, 74 operations, and 10,450 response bytes.

Remaining blocker/risk: none for T02. Real benchmark fixtures and host
measurements remain T07 work; the committed manifest contains synthetic metrics
only.

## Focused tests

Run only contract/protocol package tests, named composition parity tests, and one
small fixture measurement. Do not run the real-host matrix or full collector
suite.

## Out of scope

No cache-file migration, SFTP behavior change, helper executable, installer, or
UI work.
