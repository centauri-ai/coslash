# SSH helper post-implementation notes

Status: active implementation record; T01 complete

Last updated: 2026-09-01

This file records what was actually implemented, decisions that downstream
tasks must preserve, validation evidence, and deviations or remaining work. Add
one section when each master-plan task reaches `review` or `done`.

## Current state

| Task | State | Implementation reference |
| --- | --- | --- |
| T01 — Contracts, metrics, and fixtures | done | Commit `a400074` |
| T02 — Cache v2 and incremental SFTP | not started | Depends on T01; cache compatibility window still needs approval |
| T03 — Linux helper and SSH transport | not started | Depends on T01; initial Linux target/libc strategy still needs approval |
| T04–T07 | not started | Follow the master-plan dependency order |

## T01 — Contracts, metrics, and fixtures

Completed: 2026-09-01. Commit: `a400074`
(`feat(collector): add remote collection contracts`).

### What landed

- `collector/internal/remotefacts` defines the versioned, transport-neutral
  family schema and adapters to and from shared composition inputs.
- `collector/internal/remoteprotocol` defines bounded request and strict NDJSON
  response records plus a pure proposed-generation accumulator.
- `collector/internal/remotemetrics` defines privacy-safe measurement manifests,
  validation, summarization, a deterministic fixture, and a narrow CLI command.
- `collector/internal/collector/remote_facts_test.go` verifies that local,
  SFTP-shaped, and helper-shaped facts compose into equivalent cards.
- The source design and seven-task implementation plan now contain the Phase 0
  defaults and downstream handoff boundaries.

### Contract decisions downstream tasks must preserve

- A family is the atomic replacement unit and forms one bounded tree rooted at
  `family_id`; cycles, unknown parents, and extra roots are invalid.
- Wire facts contain an explicit allowlist. They cannot represent transcript
  rows, prompts, raw commands, unrestricted tool output, environment values,
  working directories, edited paths, or absolute paths.
- Fingerprint keys are opaque comparison data. They reject path separators and
  must never be resolved or opened as paths from a Mac request.
- Requests and responses are bounded by byte, record, family, inventory, and
  nesting limits. Unknown fields, replayed/mixed identities, invalid ordering,
  and records for unrequested vendors are rejected.
- Changed records against a known baseline must identify the exact prior family
  fingerprint. Baseline-free overflow intentionally omits that unavailable
  prior and requires a bounded authoritative inventory before deletion.
- A skipped replacement retains immutable last-good facts and attaches a
  transient stale reason. An unchanged result clears that reason.
- Tombstones are provisional until complete vendor enumeration and authoritative
  inventory prove absence. Interruption or incomplete inventory cannot delete
  cached families.
- A proposal advances its baseline identity to the request ID. The request's
  baseline ID always identifies the prior committed generation.
- Valid changed families may be published from a partial refresh; broader cache
  coverage advances only after all requested vendors complete authoritatively.

### Boundary issues fixed during review

- Preserved last-good family state across skipped-then-unchanged refreshes.
- Made generation identity advance instead of retaining the stale baseline ID.
- Enforced requested-vendor lifecycle and rejected family actions after that
  vendor's completion record.
- Required all family sessions to descend from the declared family root.
- Kept baseline-free overflow capable of replacing an existing cached family
  without prior comparison data.

### Validation evidence

The final uncached focused run passed:

```sh
go test -count=1 \
  ./internal/remotefacts \
  ./internal/remoteprotocol \
  ./internal/remotemetrics \
  ./internal/collector
```

The reproducible content-free measurement also passed:

```sh
go run ./internal/remotemetrics/cmd/measure \
  -manifest ./internal/remotemetrics/testdata/fixture-manifest.json
```

Synthetic totals were 16 observations, 990 metadata bytes, 800 header bytes,
15,600 body bytes, 74 operations, 10,450 response bytes, 14 ms maximum total,
and 7 ms maximum first result. These numbers verify the harness only; they are
not real-host performance claims.

### Guidance for T02

- Persist normalized family facts and stale overlay separately so a failed
  replacement cannot destroy the last-good completeness state.
- Generate cache v2 from proposed generations and perform durable mutation only
  through the atomic store boundary; never write once per family record.
- Preserve generation ID, coverage window, parser/schema versions, family
  aggregate fingerprint, contributing fingerprints, and Codex header mappings.
- Treat v1 fingerprints as incompatible input. Keep the valid v1 card snapshot
  visible as stale until the first valid v2 generation commits.
- Add crash/corruption tests around temp write, sync, close, rename, directory
  sync, load validation, and v1 fallback.
- Do not begin with a real SSH host. Use bounded fake `ReadSource` operations and
  assert metadata, header, body, and operation counts.

### Guidance for T03

- Serialize dedicated protocol structs; do not serialize parser, cache, or
  composition structs directly.
- Stream and validate records in sequence while bounding stdin, stdout, stderr,
  duration, record size, record count, and total response size.
- Preserve already validated family changes on truncation, while withholding
  coverage and unproven tombstones.
- Implement `baseline_mode=none` exactly as specified when the complete known
  set cannot fit; never send a partial known baseline.
- Keep helper source discovery fixed to its allowlist and independent of paths
  or identifiers supplied by the Mac.

### Remaining work and limitations

- The checked-in workload is synthetic and content-free. T07 must collect and
  approve real-host cold, warm, changed-family, first-result, request/response,
  CPU, and peak-RSS evidence before rollout.
- T02 still needs explicit approval of the cache-v1 compatibility/retention
  window before its persistence format is finalized.
- T03 still needs explicit approval of the initial Linux architecture/libc
  support policy before release artifact work is finalized.
- T01 intentionally includes no cache migration, SFTP behavior change, helper
  executable, installer, manager integration, or UI change.
