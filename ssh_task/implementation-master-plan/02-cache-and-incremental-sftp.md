# T02 — Cache v2 and incremental SFTP

Status: not_started

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
