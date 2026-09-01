# T07 — Full validation and rollout gate

Status: not_started

Depends on: T01–T06 all done

## Objective

Run the expensive integrated validation once implementation converges, compare
against baseline, and make a go/no-go decision for feature-flag rollout.

## Preconditions

- T01–T06 acceptance criteria and focused tests pass.
- Master-plan blockers are resolved or explicitly accepted by named owners.
- Protocol/schema/cache versions and release artifact inputs are frozen for the
  validation candidate.
- Approved production signing keys, revoked-key data, and minimum accepted
  metadata sequence are embedded in the validation candidate.
- Signed release metadata is published at the production endpoint, and the
  selected Linux `amd64`/`arm64` artifacts are available from the production
  artifact source with authenticated digests matching that metadata.

## Full validation matrix

- `go test ./...`, `go vet ./...`, Go formatting, frontend full tests/lint/format
  check/build, and applicable smoke tests.
- Race tests for concurrency-sensitive remote packages and sustained bounded
  protocol fuzzing.
- Reproducible Linux `amd64`/`arm64` helper builds and signature/digest checks.
- Production-provider tests for key rotation/revocation, metadata expiry and
  rollback protection, endpoint failure, architecture selection, artifact
  digest mismatch, and fail-closed behavior before rollout enablement.
- End-to-end cold, unchanged, changed-family, broader-window, incompatible-helper,
  deprecated/revoked helper, install/upgrade/rollback/uninstall, disable,
  remove-only, remove-and-uninstall, fingerprint-request overflow, fallback,
  malformed, oversized, disappearing-file, slow-disk, timeout, and output-flood
  cases.
- Golden parity among local, SFTP-normalized, and helper-normalized facts.
- Privacy inspection of wire, cache, logs, diagnostics, and telemetry.
- Agent-box benchmark comparison against T01.

## Provisional release targets

Replace these only with T01-approved measured targets:

| Metric | Gate |
| --- | ---: |
| Helper cold refresh p95 | ≤60 s |
| Unchanged warm refresh p95 | ≤5 s |
| One changed active family p95 | ≤15 s |
| Time to first publishable family p95 | ≤5 s |
| Repeat-refresh SSH receive bytes | ≤1 MiB |
| Cold normalized response | ≤2% of selected transcript bytes |
| Raw transcript bytes crossing SSH | 0 |
| Valid selected families published | 100% |
| Unchanged bodies/Codex headers reread | 0 |
| Cached families lost after incomplete scan | 0 |
| Cross-vendor failure amplification | 0 |
| Stuck `connecting` refreshes | 0 |

Also report install/upgrade and helper invocation success, fallback/partial/stale
rates, cache failures, Mac/helper CPU and peak RSS, local bytes read, request/
response sizes, and per-phase durations.

## Rollout gate

- Ship behind an explicit feature flag with installation optional.
- Enable the production provider only after the approved trust material,
  metadata endpoint, and artifact source pass the full validation matrix.
- Prefer helper only on supported, verified hosts after consent.
- Preserve documented SFTP fallback.
- Define rollback criteria for correctness, privacy, lifecycle, latency, crash,
  and resource regressions before enabling a cohort.
- Do not make helper the default until the agreed observation window passes with
  content-free telemetry and usable diagnostics.

## Deliverable

Attach commands/results, benchmark comparison, parity report, security/privacy
signoff, residual risks, and a clear go/no-go recommendation to the master-plan
handoff. Mark the project complete only after this gate passes.
