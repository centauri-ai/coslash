# SSH helper post-implementation notes

Last updated: 2026-09-01

## T02 — Cache v2 and incremental SFTP

Status: done

Implementation branch: `hlu/ssh-mix-02`

Initial implementation commit: `bcba054`

### Delivered

- Added an atomic, permissioned cache-v2 snapshot containing normalized family
  facts, fingerprints, stale reasons, per-family last-success timestamps,
  coverage, generation identity, and versioned Codex header mappings.
- Kept valid cache-v1 cards visible as stale display data until the first v2
  commit without treating v1 fingerprints as a v2 baseline.
- Reworked SFTP collection around the shared `remoteprotocol.Accumulator`, with
  family-level changed, unchanged, skipped, and authorized tombstone records.
- Added independent Claude and Codex byte budgets under one session deadline.
- Added bounded manifest reuse so unchanged families transfer no transcript
  bodies and unchanged Codex files do not require another header read.
- Deferred initial collection until the first `ListView` supplies the actual
  requested history window.

### Review corrections

The implementation review found and corrected several boundary cases before
T02 was marked done:

- Live `Open` and nested `ReadDir` operations now repeat `lstat`/`realpath`
  validation. Cached directory metadata remains usable for manifest `Stat`
  calls without allowing a post-listing symlink replacement to escape the
  allowlist.
- Changed families receive a fresh fingerprint after parsing. A size or mtime
  change produces a stable `unstable_file` skip instead of committing facts
  under the wrong fingerprint.
- Corrupt, oversized, unstable, and otherwise skipped families preserve valid
  unrelated families while making refresh health partial.
- Parser/schema version changes force family reparse; Codex header-cache entries
  are also parser-versioned.
- Requests whose complete known-family set exceeds the protocol bound perform a
  genuine bounded baseline-free recollection. Incomplete inventories do not
  authorize tombstones or advance coverage.
- Established-session context errors survive read, EOF, and close paths, so a
  deadline remains classified as a timeout.
- Cache-v2 loading rejects oversized files, excessive collection counts,
  invalid coverage, invalid timestamps, duplicate entries, and invalid family
  facts before use.

### Validation

The final implementation passed:

- focused remote, vendor, normalized-facts, protocol, metrics, and collector
  package tests;
- race tests for `internal/remote` and `internal/remoteprotocol`;
- `go build ./...`;
- `go vet ./...`;
- `git diff --check`.

All remote collection tests use fake SFTP/read sources; no real SSH host is
required.

### Known deferred limitation

Claude background-session re-home collapsing only sees families reparsed in the
same refresh. If the predecessor family is unchanged, the new background family
may temporarily appear as a separate card until the predecessor is reparsed.
This is a display-convergence issue only: it does not corrupt cache state,
authorize unsafe deletion, or lose normalized facts. A complete fix requires
persisting re-home detection state or deliberately reparsing the predecessor and
remains outside T02 acceptance criteria.

### Remaining plan

T03–T07 remain separate work items. In particular, T07 still owns full rollout
validation, release packaging, platform matrices, and real-host end-to-end
testing.
