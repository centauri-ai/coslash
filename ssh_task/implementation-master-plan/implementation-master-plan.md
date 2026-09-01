# SSH helper implementation master plan

Status: implementation in progress; T01–T04 complete

Last updated: 2026-09-01

Source design: `../mix-plan/ssh-helper-design-plan.md`

Implementation record: [post-implementation-notes.md](post-implementation-notes.md)

## Outcome

Deliver an optional Linux helper that parses Claude Code and Codex transcripts
beside the data and streams bounded, versioned family facts to the Mac. The Mac
owns settings, durable cache, composition, health, filtering, and UI. Hosts where
installation is declined, unsupported, or blocked retain deterministic
best-effort SFTP.

Completion means large transcripts do not cross SSH as raw bytes, unchanged
refreshes avoid transcript and Codex-header reads, valid families survive
partial failures, and incomplete scans cannot delete cached families.

## Why seven work items

The work is grouped by architectural boundary rather than by individual type or
package. Schema, protocol, and fixtures must be designed together; cache and
incremental SFTP share one reuse model; helper collection and SSH streaming share
one protocol boundary; manager, UI, and documentation form one user-visible
integration. Helper lifecycle, security hardening, and final validation stay
separate because they have distinct risk and approval gates.

## Status vocabulary

- `not_started`: implementation has not begun.
- `in_progress`: one owner is actively working within the task scope.
- `blocked`: an unresolved decision or dependency prevents progress.
- `review`: code and focused tests are complete; review is pending.
- `done`: acceptance criteria and focused tests pass, with handoff recorded.

## Work items

| ID | Task | Status | Depends on | Current blocker | Brief |
| --- | --- | --- | --- | --- | --- |
| T01 | Contracts, metrics, and fixtures | done | — | — | [Brief](01-contracts-metrics-and-fixtures.md) |
| T02 | Cache v2 and incremental SFTP | done | T01 | — | [Brief](02-cache-and-incremental-sftp.md) |
| T03 | Linux helper and SSH transport | done | T01 | — | [Brief](03-helper-and-ssh-transport.md) |
| T04 | Helper lifecycle and compatibility | done | T03 | — | [Brief](04-helper-lifecycle-and-compatibility.md) |
| T05 | Manager, setup UI, diagnostics, and docs | not_started | T02, T03, T04 | Final consent copy needs product approval | [Brief](05-manager-ui-diagnostics-docs.md) |
| T06 | Security and fault hardening | not_started | T02–T05 | Threat-model review required | [Brief](06-security-and-fault-hardening.md) |
| T07 | Full validation and rollout gate | not_started | T01–T06 | All implementation tasks must be done | [Brief](07-full-validation-and-rollout.md) |

## Dependency path

```text
T01 contracts/fixtures
 ├─> T02 cache + SFTP ──────────┐
 └─> T03 helper + SSH ─> T04 lifecycle
                                │
                         T05 integration/UI
                                │
                         T06 hardening
                                │
                         T07 full validation
```

T02 and T03 can run in parallel after T01. T04 can start when T03's helper build,
capability handshake, and exec boundary are stable. T05 waits for both collection
paths and lifecycle semantics so it integrates the real contracts once.

## Phase 0 recommended defaults

Resolve these while completing T01:

1. Helper installation is optional and explicitly consented initially.
   Compatible installed helpers are reused rather than reinstalled.
2. Start with statically linked Linux `amd64` and `arm64` when the helper works
   with `CGO_ENABLED=0`; otherwise document the minimum libc.
3. Use a versioned user-owned executable directory. If approved locations are
   `noexec`, stay on SFTP rather than requiring privilege.
4. Authenticate signed release metadata containing artifact digests against a
   public key embedded in the Mac application, with rotation/revocation rules.
5. Remote v1 excludes working directories, edited paths, raw commands, prompts,
   unrestricted tool output, and absolute paths.
6. Write cache v2 separately and atomically. Keep a valid v1 snapshot visible as
   stale until v2 first commits; never reinterpret v1 fingerprints as v2 state.
7. Negotiate compatible protocol/schema ranges. Mac and helper build versions do
   not need to match exactly.
8. Bound the known-fingerprint request. For protocol v1, if the selected
   fingerprints do not fit, send `baseline_mode=none` and perform a bounded full
   recollection of the requested window. Deletion still requires a bounded
   authoritative family inventory from a complete vendor scan; if that inventory
   cannot fit, coverage is partial and no tombstone commits. Omitted fingerprints
   never imply deletion. Add digest/paging later only if measurements require it.
9. Disabling a host leaves its helper installed and says so. Removing a host
   offers an explicit “remove and uninstall helper” action before deleting the
   local alias/settings, plus a disclosed “remove host only” alternative. Never
   perform remote deletion silently.
10. Support the current and immediately previous compatible protocol/schema for
    at least one stable Mac release. Prompt to upgrade deprecated helpers;
    incompatible or revoked helpers are not executed and use visibly degraded
    SFTP until upgraded.

## Implementation rules

- Read the assigned brief and referenced design/code before editing.
- Preserve unrelated worktree changes.
- Keep normalized domain types independent of SFTP and helper packages.
- Do not serialize internal Go structs as the wire contract.
- Treat remote output and cached identifiers as untrusted.
- Never resolve or open a path supplied by the Mac request on Linux.
- Keep changes inside the current task; record newly discovered scope or blockers
  in this file.
- Update the table status and add a handoff note when a task changes state.

## Test policy

For T01–T06, run only focused tests:

- Format changed Go files.
- Run named tests while iterating and the narrow changed package before handoff.
- Run one adjacent package only when a public boundary changes.
- Run touched Vitest files; run the TypeScript build only for shared type changes.
- Build one native helper target during iteration.
- Use short bounded fuzz/race runs only for the security cases added in T06.

Do not repeatedly run `go test ./...`, the entire frontend suite, release
packaging, cross-platform artifact matrices, real-host end-to-end tests, or the
complete benchmark matrix. T07 owns those expensive checks after implementation
converges. If a larger test is necessary earlier for a safety boundary, record
the reason and result in the handoff.

## Handoff format

```text
Txx handoff — YYYY-MM-DD
Changed: <packages/files>
Decisions: <contract choices>
Focused tests: <commands and results>
Remaining blocker/risk: <none or exact issue>
```

## Global blockers and change log

- 2026-09-01: T04 completed on branch
  `hlu/ssh-mix-04`. It fixes the optional helper location to
  `~/.local/lib/coslash/helpers/<version>/coslash-helper`, supports static
  Linux `amd64`/`arm64` ELF artifacts authenticated by canonical signed
  metadata, and makes initial installation and later upgrades separately
  consented. Signed expiry and a durable monotonic sequence prevent metadata
  replay. The production OpenSSH/SFTP adapter performs bounded probing,
  no-follow owner/mode validation, exclusive temporary upload, fsync, digest
  verification, `noexec` detection, atomic activation, rollback to a verified
  previous helper, and authenticated exact-path uninstall. Incompatible,
  revoked, tampered, or failed helpers are never selected for execution, and
  transport failures retain their own accurate fallback reasons.

- 2026-09-01: T03 completed on branch
  `hlu/t03-helper-and-ssh-transport` in commits `0a9dc20` and `96dac46`.
  The stateless helper, bounded SSH exec transport, static Linux
  `amd64`/`arm64` builds, warm Codex header reuse, and permanent failure-path
  tests are ready for T04 and downstream integration. The initial target/libc
  blocker is resolved: both artifacts build with `CGO_ENABLED=0` and are
  statically linked. Follow-up boundaries:
  - **T02 integration:** persist bounded Codex file-header mappings and include
    them in known helper baselines; request overflow already falls back
    atomically to `baseline_mode=none`.
  - **T02/T05 integration:** decide whether baseline-free cache generations
    retain authoritative inventories or never prune, because the current
    proposed generation does not preserve the response inventory.
  - **T05:** add frontend types and copy for helper missing, blocked,
    incompatible, failed, partial, and output-limit health reasons.
  - **T06:** retain review of display-field provenance. T03 clears Codex's
    prompt-derived fallback name before facts cross the helper boundary.
- 2026-09-01: T01 completed in commit `a400074`. The normalized family
  contract, bounded protocol, merge accumulator, privacy census, deterministic
  fixtures, and synthetic measurement command are ready for T02 and T03.
  Real-host performance measurements remain a T07 rollout input. The T02 cache
  and T03 Linux target approval gates were subsequently resolved in their
  completion records.
- 2026-09-01: Consolidated the original 12-task plan into seven boundary-owned
  tasks to reduce handoffs and duplicated context. At that point implementation
  had not started.
- 2026-09-01: T02 completed on branch `hlu/ssh-mix-02` in commits `bcba054`
  and `c66217f`. Cache v2, incremental SFTP, review corrections, focused tests,
  and the implementation handoff are complete. See
  [post-implementation notes](post-implementation-notes.md).
