# T02 — Cache v2 and incremental SFTP

Status: review

Depends on: T01

## Objective

Implement atomic family-level persistence and make SFTP a deterministic,
incremental, best-effort producer of the shared normalized contract.

## Context

`remote/cache.go` currently stores composed roots and agent-wide fingerprints.
Startup may refresh with `since=0`; vendors share a byte counter; strict parsing
lets one file discard a vendor; Codex discovery repeatedly reads every header;
path validation performs repeated SFTP `lstat`/`realpath` operations. Preserve
the existing allowlist and no-symlink guarantees.

Read `collector/internal/remote/{manager,cache,source,sftp,limits}.go`, Claude and
Codex discovery/source files, and the T01 contracts.

## Scope

### Cache v2

- Persist normalized facts per family, contributing fingerprints, state/reason,
  last success, parser/schema version, coverage, and generation identity.
- Retain Codex path-to-session/parent mappings so unchanged headers are reusable.
- Validate loaded bounds and values before use.
- Write temp, sync, close, atomic rename, and sync the containing directory where
  supported; retain `0600` files and `0700` directories.
- Keep valid cache v1 cards visible as stale until the first v2 commit; never
  reinterpret v1 state.
- Recover safely from corruption and interrupted writes.

### Incremental SFTP

- Wait for the first `ListView` window before initial collection.
- Build one bounded manifest from directory metadata and reuse it.
- Reuse unchanged Codex header mappings and family facts.
- Skip oversized files before header/body reads.
- Parse and report independently per family with stable skip/failure reasons.
- Give vendors independent byte budgets under the overall deadline.
- Fingerprint before/after parsing; retry briefly or mark unstable.
- Reduce validation RTTs only with equivalent path-security proof.
- Preserve timeout classification through read, close, and EOF paths.

## Acceptance criteria

- Atomic partial merges retain failed families and apply only authorized
  tombstones.
- One oversized/corrupt family does not hide unrelated Claude or Codex results.
- Unchanged refresh transfers no transcript bodies and rereads no unchanged
  Codex headers.
- Incomplete scan cannot delete cached state.
- Vendor budgets do not race, and UI coverage/window/truncation is accurate.

## Focused tests

Use temporary cache directories and fake `ReadSource`/SFTP operations. Run only
affected remote/vendor packages and named composition tests. Assert operation and
byte counts; do not use a real SSH host.

## Out of scope

No helper executable, SSH exec, managed installation, or UI work.

## Handoff

T02 handoff — 2026-09-01

Changed: `internal/remote/{cache,source,manager,collect,generation}.go` and new
tests (`cache_test.go`, `source_test.go`, `fake_test.go`,
`collect_integration_test.go`, `manager_test.go`); `internal/vendors/source.go`
(`ParseSourceFilesStrict` now returns partial results and per-file failures
instead of discarding everything on one bad file); `internal/vendors/claude/
source.go` and `internal/vendors/codex/source.go` (new `BuildRemoteFamilies`,
`ParseRemoteFiles`, `RemoteMetadata`; Codex gets `ResolveHeaders`/
`CachedHeader` for fingerprint-gated header reuse; old monolithic
`CollectRemote` removed from both, since nothing else used it).

Decisions:

- Cache v2 (`CachedSnapshotV2`) persists `remoteprotocol.Generation` directly
  (families keyed by vendor+family id, each carrying its `remotefacts.Family`
  facts and comparison fingerprint) plus coverage/timing display fields and
  the Codex path-fingerprint→header cache. Written to a separate
  `snapshot-v2.json` file via temp+fsync+close+rename+directory-fsync,
  `0600`/`0700`, and rejects on load if any embedded family fails
  `remotefacts.Validate` (whole file degrades to cold rather than partially
  trusting it).
- A v1 cache stays loadable and visible as `StateStale` display data only: on
  load it's wrapped into a `CachedSnapshotV2` shell with empty `Families` and
  `BaselineID`, so the next refresh always starts from an empty accumulator
  generation. A v1 fingerprint is never compared against anything.
- Incremental SFTP reuses T01's `remoteprotocol.Accumulator` verbatim as the
  merge engine: each refresh diffs Claude and Codex against the cached
  baseline concurrently (independent `Source.ForVendor` byte budgets), builds
  synthetic `changed_family` / `unchanged_family` / `skipped_family` /
  `provisional_tombstone` records in memory, then applies them to one
  in-process `Accumulator` exactly as a helper's NDJSON response would be
  applied. Family-level failure isolation and vendor-level deletion authority
  therefore come from the already-reviewed T01 state machine, not new logic.
- `remote.Source` gained a directory-listing cache: entries discovered by an
  already-validated `ReadDir` (proven non-symlink by the SFTP listing itself,
  canonical path derived from the already-proven parent) are reused by later
  manifest `Stat` calls on the same session. Operations that follow the live
  path (`Open` and nested `ReadDir`) re-run `lstat`/`realpath`, preventing a
  post-listing symlink replacement from escaping the allowlist. Combined with
  the pre-existing size-check-before-open ordering, an oversized file is
  skipped before any real open. Per-vendor budgets come from
  `Source.ForVendor(maxBytes)`, a view sharing validation and the entry-count
  cap but metering bytes independently.
- Changed families are freshly fingerprinted after parsing and marked
  `unstable_file` instead of committed when size or mtime moved. Parser/schema
  version mismatches force reparse, family skips make manager health partial,
  and an oversized known-family request performs a true baseline-free bounded
  recollection instead of accidentally retaining old facts.
- Cache v2 loading is size- and collection-bounded, validates coverage and
  timestamps, persists per-family last-success time, and versions Codex header
  entries so a header parser change invalidates them. Established-session
  deadline identity is retained through read/EOF and close processing.
- `ApplySettings` no longer calls `kickRefreshLocked` on restart; the first
  refresh now starts from `ListView`'s existing staleness check, so initial
  collection waits for the UI's real requested window instead of `since=0`.
- Deferred, documented limitation: a Claude background-rehome merge
  (`collapseBackgroundRehomes`) only fires within one refresh's freshly
  reparsed batch. If the predecessor transcript is unchanged this refresh, the
  merge can't be detected and the two sessions show as separate cards until
  the predecessor is reprocessed for another reason. Never unsafe (no crash,
  no invalid cache, no data loss) — just a display convergence gap. See the
  master plan change log for the full reasoning.

Focused tests: `go build ./...`; `go vet ./...`; `go test
./internal/remote/... ./internal/vendors/... ./internal/remotefacts/...
./internal/remoteprotocol/... ./internal/remotemetrics/... ./internal/collector/...`
— all passed. New `internal/remote` tests use fake SFTP operations and fake
`ReadSource`/parse functions only; no real SSH host. They cover: v2 atomic
persistence and bounds validation, v1-stays-stale-until-first-v2-commit,
directory-cache RTT elimination and continued symlink rejection, independent
per-vendor byte budgets, oversized-file skip before any real open,
unchanged-refresh reopening neither a Claude transcript nor a Codex header,
one corrupt family not hiding an unrelated family, a hard vendor failure not
tombstoning the other vendor's cache, a narrower window not tombstoning a
family merely outside it, a genuinely deleted family being tombstoned only
with full inventory, and `ApplySettings` not starting a refresh before the
first `ListView`. Review follow-ups additionally cover post-listing symlink
replacement, mutation during parsing, parser-version invalidation, corrupt
family partial health, oversized/invalid cache rejection, aggregate and
per-vendor byte budgets, and baseline-free fallback above the known-family
limit.

Remaining blocker/risk: none for T02's acceptance criteria. The background-
rehome convergence gap above is real but out of scope for this task's
acceptance criteria; flagging for whoever next touches Claude remote
composition. T03–T07 remain not started.
