# SSH helper post-implementation notes

Status: active implementation record; T01–T05 complete; T06 is next

Last updated: 2026-09-01

This file records what was actually implemented, decisions that downstream
tasks must preserve, validation evidence, and deviations or remaining work. Add
one section when each master-plan task reaches `review` or `done`.

## Current state

| Task | State | Implementation reference |
| --- | --- | --- |
| T01 — Contracts, metrics, and fixtures | done | Commit `a400074` |
| T02 — Cache v2 and incremental SFTP | done | Branch `hlu/ssh-mix-02`; commits `bcba054`, `c66217f` |
| T03 — Linux helper and SSH transport | done | Branch `hlu/t03-helper-and-ssh-transport`; commits `0a9dc20`, `96dac46` |
| T04 — Helper lifecycle and compatibility | done | Branch `hlu/ssh-mix-04`; commit `ecbaab6` |
| T05 — Manager, setup UI, diagnostics, and docs | done | Branch `hlu/ssh-mix-05`; commits `cd4179f`–`a071c1a` |
| T06–T07 | not started | Follow the master-plan dependency order |

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
- T02 is complete. Its one deferred limitation is the Claude background re-home
  display-convergence case documented below.
- T03 resolved the initial Linux architecture/libc policy with static
  `CGO_ENABLED=0` builds for `amd64` and `arm64`.
- T01 intentionally includes no cache migration, SFTP behavior change, helper
  executable, installer, manager integration, or UI change.

## T02 — Cache v2 and incremental SFTP

Completed: 2026-09-01. Branch: `hlu/ssh-mix-02`. Implementation commit:
`bcba054` (`feat(remote): add cache v2 and incremental SFTP`). Documentation
completion commit: `c66217f` (`docs(ssh): record T02 completion`).

### What landed

- Atomic cache-v2 persistence stores normalized family facts, aggregate and
  contributing fingerprints, stale reasons, per-family last-success time,
  coverage, generation identity, and versioned Codex header mappings.
- Valid cache-v1 cards remain visible as stale until the first v2 commit; v1
  fingerprints are never treated as a v2 baseline.
- Incremental SFTP uses the shared accumulator for family-level changed,
  unchanged, skipped, and authorized tombstone records.
- Claude and Codex have independent byte budgets under one session deadline.
- Unchanged refreshes transfer no transcript bodies and do not reopen unchanged
  Codex headers.
- Initial collection waits for the first `ListView` history window.

### Corrections completed during review

- Live `Open` and nested `ReadDir` operations repeat path validation, preventing
  post-listing symlink replacement from escaping the allowlist.
- Changed families receive a fresh post-parse fingerprint and are marked
  `unstable_file` rather than committed if size or mtime changes.
- Corrupt, oversized, unstable, and otherwise skipped families preserve valid
  unrelated facts while making refresh health partial.
- Parser/schema changes invalidate cached facts and versioned Codex headers.
- Oversized known-family requests perform true bounded baseline-free
  recollection; incomplete inventories cannot authorize deletion or coverage.
- Session deadline identity survives read, EOF, and close processing.
- Cache-v2 loading validates file size, collection counts, coverage, timestamps,
  duplicate entries, and normalized family facts before use.

### Validation evidence

The final implementation passed focused remote/vendor/facts/protocol/metrics/
collector tests, race tests for `internal/remote` and `internal/remoteprotocol`,
`go build ./...`, `go vet ./...`, and `git diff --check`. All remote collection
tests use fake SFTP/read sources; no real SSH host is required.

### Known deferred limitation

Claude background-session re-home collapsing only sees families reparsed in the
same refresh. If the predecessor is unchanged, the new background family may
temporarily appear separately until the predecessor is reparsed. This is a
display-convergence issue only; it does not corrupt cache state, authorize an
unsafe deletion, or lose normalized facts. Persisting re-home detection state or
deliberately reparsing the predecessor remains outside T02 acceptance criteria.

## T03 — Linux helper and SSH transport

Completed: 2026-09-01. Commits: `0a9dc20`
(`feat(remote): add the Linux collection helper and SSH exec transport`) and
`96dac46` (`fix(remote): harden helper transport and warm scans`).

Implementation branch: `hlu/t03-helper-and-ssh-transport`.

### What landed

- `collector/cmd/coslash-helper` provides stateless `version`/`capabilities`
  and `collect` commands with bounded request decoding and documented exit codes.
- `collector/internal/remotehelper` owns fixed-root discovery, no-follow reads,
  bounded enumeration, family grouping, incremental comparison, parsing,
  stability retries, inventories, tombstones, and streamed NDJSON emission.
- `collector/internal/remote/helperexec*.go` drives the helper through system
  SSH using fixed argv construction, bounded concurrent pipes, ControlMaster
  reuse, incremental protocol accumulation, timeouts, and process-group cleanup.
- Reproducible `CGO_ENABLED=0` builds target Linux `amd64` and `arm64` without a
  runtime libc dependency.
- Permanent helper, transport, protocol, privacy, and command tests cover the
  acceptance boundary instead of relying on removed or one-off tests.

### Decisions downstream tasks must preserve

- The helper is stateless and reads only its fixed allowlist beneath one
  `os.Root` handle. Request identifiers and fingerprint keys are comparison data
  and never become filesystem paths.
- The remote shell command contains only a narrowly validated helper path and a
  closed subcommand. Request data travels on bounded stdin and is never
  interpolated into argv or shell syntax.
- Directory entries are read in fixed-size batches. The aggregate entry limit
  is enforced while reading so one directory cannot allocate without a bound
  before rejection.
- Codex family facts carry bounded opaque file-key-to-session/parent mappings.
  A known baseline supplies those mappings so unchanged files avoid header
  reads; new or changed files reread and validate headers. If mappings or
  fingerprints exceed request bounds, the entire baseline is discarded.
- Codex's parser-derived first-prompt fallback is cleared by the helper adapter.
  Approved session-index metadata names remain available, while prompt text has
  no intentional helper wire field.
- A file changing during parse is retried within a fixed budget. A Codex header
  identity change makes the family unstable rather than publishing facts under
  stale grouping.
- `request_complete` is not an early stdout cutoff. The Mac continues a bounded
  drain through EOF and rejects blank, malformed, excessive, or trailing output.
- Helper resource-limit exit code 6 maps to the distinct `output_limit` reason;
  missing, blocked, incompatible, helper failure, SSH failure, partial coverage,
  timeout, and cancellation remain distinguishable.
- Completed family records may survive a later interruption only in the pure
  proposed generation. The caller remains responsible for one atomic durable
  cache commit.

### Boundary issues fixed during review

- Restored permanent fake-process and helper tests required by the acceptance
  criteria, including success, incompatibility, malformed/truncated output,
  stdout/stderr floods, nonzero exits, hung children, cancellation, and input
  injection.
- Rejected content after `request_complete` instead of silently accepting it.
- Replaced unbounded `ReadDir(-1)` allocation with bounded batched enumeration.
- Added cache-carried Codex header mappings so unchanged warm scans do not need
  to reopen every rollout header.
- Prevented Codex first-prompt fallback text from entering helper facts.
- Added a distinct helper resource-limit exit and transport classification.
- Required exactly one bounded request line and rejected trailing request data.

### Validation evidence

The final focused suite passed:

```sh
go test \
  ./internal/remotehelper \
  ./internal/remote \
  ./internal/remoteprotocol \
  ./internal/remotefacts \
  ./internal/vendors/codex \
  ./internal/collector \
  ./cmd/coslash-helper
```

Concurrency and static checks passed:

```sh
go test -race ./internal/remote ./internal/remotehelper
go vet \
  ./internal/remotehelper \
  ./internal/remote \
  ./internal/remoteprotocol \
  ./internal/remotefacts \
  ./internal/vendors/codex \
  ./cmd/coslash-helper
```

Cross-build validation produced statically linked ELF binaries for
`linux/amd64` and `linux/arm64` with `CGO_ENABLED=0`.

### Guidance for T02 integration

- Persist each family's Codex header mappings with its contributing file
  fingerprints and include them when constructing a bounded known helper
  baseline.
- If the known baseline overflows, send no partial mappings or fingerprints;
  preserve the atomic `baseline_mode=none` fallback.
- Decide explicitly how a baseline-free authoritative inventory reaches the
  cache commit boundary. The current accumulator proposal applies tombstones but
  does not retain the inventory itself.

### Guidance for T04 and T05

- T04 may rely on static Linux `amd64`/`arm64` binaries and the capability
  handshake. It still owns install paths, signed metadata, digest verification,
  revocation, upgrades, uninstall, and `noexec` fallback policy.
- T05 must integrate helper selection and cache commit policy, expose safe timing
  and byte diagnostics, and add UI copy for every helper health reason.

### Remaining work and limitations

- Managed installation, artifact authentication, compatibility lifecycle,
  manager selection, settings, UI, and real-host rollout validation remain in
  T04–T07.
- T03 uses fake-process transport tests, not a real SSH host, as required by its
  task boundary. T07 still owns real-host latency, CPU, RSS, and corpus gates.
- T06 must perform the broader threat-model and fault-injection pass even though
  the T03-specific review findings now have regressions.

## T04 — Helper lifecycle and compatibility

Completed: 2026-09-01. Branch: `hlu/ssh-mix-04`. The T04 implementation commit
(`feat(remote): add helper lifecycle policy`) was amended after review.

### What landed

- Canonical Ed25519-signed release metadata selects bounded Linux `amd64` and
  `arm64` artifacts by normalized remote platform and authenticated digest.
- Release metadata has a signed expiry and monotonic sequence. A private,
  atomic local high-water-mark store rejects replay of older signed metadata;
  app-shipped minimum sequences and revoked keys provide release-time recovery.
- `SSHLifecycleRemote` implements the lifecycle boundary using the existing
  bounded OpenSSH control connection and SFTP subsystem: fixed platform probe,
  exact home-relative path resolution, owner/mode/symlink validation, exclusive
  temporary creation, fsync, SHA-256 verification, `noexec` detection, and
  atomic OpenSSH POSIX rename.
- Setup reuses a verified compatible helper without upload. Initial install and
  later upgrade consent are distinct. A deprecated previous helper remains
  executable until a current replacement passes digest and capability checks.
- Failed activation removes the failed current artifact and returns the verified
  previous helper as a visible deprecated rollback instead of trapping future
  setup attempts on the failed version.
- Revoked previous helpers are never executed but do not block installation of
  a safe current artifact. Capability and SSH transport errors retain distinct
  classifications.
- Uninstall verifies fresh signed metadata, probes the host platform, and
  removes only an exact version present in that authenticated platform manifest.
  Repetition is harmless; disabling a host performs no lifecycle operation.

### Corrections completed during review

- Replaced the test-only lifecycle boundary with a production SSH/SFTP adapter.
- Added durable metadata anti-rollback and expiry checks so old signed documents
  cannot silently undo later artifact revocation.
- Made activation verification and capability failures roll back to the known
  previous helper and remove the rejected current artifact.
- Stopped reporting SSH/authentication/timeouts as unsupported platforms and
  stopped reporting generic capability transport failures as required upgrades.
- Required authenticated manifest membership before exact-path uninstall.
- Replaced host-format-dependent ELF tests with deterministic synthetic Linux
  ELF fixtures, allowing the Mac package tests to compile on Darwin.

### Validation evidence

The final focused suite passed:

```sh
GOCACHE=/tmp/coslash-t04-cache go test -count=1 \
  ./internal/remote ./internal/remoteprotocol ./cmd/coslash-helper
GOCACHE=/tmp/coslash-t04-cache go test -race ./internal/remote
GOCACHE=/tmp/coslash-t04-cache go vet \
  ./internal/remote ./internal/remoteprotocol ./cmd/coslash-helper
GOCACHE=/tmp/coslash-t04-cache make helper
```

The native helper was a statically linked Linux `amd64` ELF. The remote package
and its tests also cross-compiled successfully for Darwin/arm64. `git diff
--check` passed.

### Guidance for T05, T06, and T07

- T05 creates `FileMetadataSequenceStore` in private app state and provides the
  production-provider boundary for a fetched signed document and exact artifact
  bytes. T07 must supply and validate approved release public keys, revoked-key
  data, minimum accepted sequence, and the production metadata/artifact sources.
- T05 must keep initial-install and upgrade consent separate, present deprecated
  rollback and degraded-SFTP reasons, and complete uninstall before forgetting
  host settings when that explicit removal option is chosen.
- T06 should fault-inject interruption around exclusive upload, fsync, atomic
  rename, verification, rollback removal, and sequence-state persistence, and
  repeat adversarial symlink substitution review against supported OpenSSH
  servers.

### Remaining work and limitations

- Manager/UI wiring and host-removal UX belong to T05. Approved release-key
  material, metadata publication, artifact delivery, and enablement belong to
  the T07 rollout gate.
- The implementation requires the OpenSSH `statvfs@openssh.com`,
  `fsync@openssh.com`, and `posix-rename@openssh.com` SFTP extensions. Missing
  extensions produce an installation failure and retain usable SFTP collection;
  there is no weaker or privileged fallback.
- Real-host interruption, hostile-race, `noexec`, and extension-compatibility
  matrices remain T06/T07 validation gates.

## T05 — Manager, setup UI, diagnostics, and documentation

Completed: 2026-09-01. Branch: `hlu/ssh-mix-05`.
Implementation and correction commits: `cd4179f`, `aac30ad`, `2293a75`,
`544aa86`, `aa88492`, and `a071c1a` (after prerequisite merge `60cec7f`).
Production release trust and artifact-publication inputs are T07 rollout gates;
they are not remaining T05 implementation work.

### What landed

- The production manager has one helper/SFTP result boundary and performs
  read-only discovery on restart. It executes only a freshly authenticated,
  platform-matched, digest-verified, owner/mode-verified, capability-compatible
  helper. Compatible verified helpers are reused without uploading again.
- Missing or declined setup, unsupported platforms, `noexec`/blocked installs,
  incompatibility or revocation, verification failures, and installation
  failures retain an explicitly labelled SFTP path. Once a verified helper is
  selected, helper protocol, data, output-limit, and execution failures remain
  helper failures and do not silently retry through SFTP.
- Setup and status responses distinguish consent, installation, reuse, upgrade,
  deprecated-active rollback, compatibility, verification, helper-test, and
  SFTP-fallback outcomes. The helper collection test reports its own operation
  result rather than inferring success from durable snapshot coverage.
- Helper ownership is persisted with the SSH alias and reported separately from
  currently discoverable helper state. Setup, status, exact uninstall, and host
  removal/alias-change flows are exposed through guarded backend APIs and strict
  frontend decoders.
- Alias replacement and host removal stage uninstall or release-only ownership
  intent in the Settings draft. The backend applies it in the same Settings
  replacement transaction; closing the dialog or a failed save does not mutate
  ownership, and failed uninstall restores the former settings and ownership.
- Corrupt ownership records fail closed while retaining a displayable fallback
  machine state. Explicit transactional release-only removal is the recovery
  path because an exact trusted version is unavailable for uninstall. Legacy
  `{version}` records migrate only when they can bind to the configured alias.
- Machines shows active transport, helper state/version, setup and removal
  controls, stale/partial coverage, and degraded-SFTP explanations. README,
  privacy, and troubleshooting documentation now match the implemented consent,
  connection-test, verification, collection, diagnostics, and removal behavior.
- Diagnostics expose bounded structured transport/helper facts and per-family
  stale provenance without remote stderr. The local-machine API omits empty
  remote-only `transport` and `helperProbeState` enum fields, preserving the
  backend/frontend machine-response contract.

### Corrections completed during review

- Split release loading into authenticated metadata and selected-architecture
  artifact retrieval; artifact bytes are fetched only for a consented install
  or upgrade. Restart discovery and uninstall require metadata but no artifact
  download.
- Required fresh lifecycle verification before helper execution instead of
  treating persisted ownership as proof that an installed helper remains safe.
- Preserved visible SFTP fallback only for lifecycle states where fallback is
  valid, and prevented runtime helper failures from being hidden by SFTP.
- Made ownership changes transactional with Settings replacement and restored
  the former settings if release/uninstall cannot complete.
- Added fail-closed corrupt-record recovery and alias-bound migration of the
  legacy ownership format.
- Removed remote stderr from exported diagnostics, corrected helper-test success
  semantics, omitted empty remote-only machine enums, and aligned
  troubleshooting copy with the actual connection test.

### Validation evidence

The final focused backend checks passed:

```sh
GOCACHE=/tmp/coslash-review6-gocache go test -count=1 \
  ./internal/remote ./internal/remoteprotocol ./internal/remotefacts \
  ./internal/remotehelper ./internal/diagnostics ./cmd/coslash
GOCACHE=/tmp/coslash-review6-gocache go vet \
  ./internal/remote ./internal/remoteprotocol ./internal/remotefacts \
  ./internal/remotehelper ./internal/diagnostics ./cmd/coslash
```

The final frontend checks passed with 31 tests across three files, followed by
a successful production build:

```sh
cd frontend
npm test -- --run \
  src/pages/coslash/lib/session.test.ts \
  src/pages/coslash/lib/handoff.test.ts \
  src/pages/coslash/lib/host-strip.test.ts
npm run build
```

`git diff --check 60cec7f..HEAD` also passed.

### Downstream handoff

- T07 must provide approved embedded signing keys, revocation data and minimum
  sequence, published signed metadata/endpoint, and a production artifact
  source. Until then, the unavailable provider fails closed and never uploads or
  executes unverified code; this feature gate does not block T05 completion or
  T06 hardening.
- T06 owns interruption/crash-consistency and adversarial lifecycle tests,
  including Settings replacement, upload/fsync/rename, verification, rollback,
  ownership persistence, and hostile remote filesystem races.
- T07 owns real-host and release validation: supported OpenSSH/SFTP extension
  coverage, `noexec` behavior, Linux architecture artifacts, corpus and resource
  gates, production metadata/artifact delivery, and rollout approval.
