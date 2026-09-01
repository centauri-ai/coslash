# SSH helper implementation master plan

Status: implementation in progress

Last updated: 2026-09-01

Source design: `../mix-plan/ssh-helper-design-plan.md`

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
| T02 | Cache v2 and incremental SFTP | not_started | T01 | Cache compatibility window needs approval | [Brief](02-cache-and-incremental-sftp.md) |
| T03 | Linux helper and SSH transport | done | T01 | — | [Brief](03-helper-and-ssh-transport.md) |
| T04 | Helper lifecycle and compatibility | not_started | T03 | Install path, signing scheme, and `noexec` policy need approval | [Brief](04-helper-lifecycle-and-compatibility.md) |
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

- 2026-09-01: Consolidated the original 12-task plan into seven boundary-owned
  tasks to reduce handoffs and duplicated context. No implementation has started.
- 2026-09-01: T03 implemented and moved to review. Its Phase 0 blocker is
  resolved: the helper builds reproducibly for `linux/amd64` and `linux/arm64`
  with `CGO_ENABLED=0` and links statically, so no minimum libc has to be
  documented. Scope discovered while implementing it:
  - **T02/T05 (deletion authority).** `remoteprotocol.Generation` keeps
    `VendorComplete` but not the vendor inventory, and the accumulator deletes
    only through tombstones. A `baseline_mode=none` response therefore carries the
    inventory that is its *only* deletion authority into a proposal that cannot
    use it. The cache commit layer must either retain the inventory or treat
    baseline-free refreshes as never pruning. The helper already emits the
    inventory truthfully.
  - **T05 (health copy).** Helper collection adds `helper_missing`,
    `helper_not_executable`, `helper_incompatible`, `helper_failed`, and
    `output_limit`. Go-side copy exists; frontend types and user-facing wording
    are still needed.
  - **T02 (Codex warm discovery).** The v1 family/request contract now carries
    bounded opaque file-header mappings. Cache v2 must persist them and include
    them in known helper baselines; overflow already falls back atomically to
    `baseline_mode=none`.
  - **T06 (privacy review).** The helper adapter clears Codex's prompt-derived
    fallback name. T06 should retain a regression audit of all display-field
    provenance, but prompt text is no longer intentionally emitted by T03.
  - **T07 (response size).** `Record.Counts` and `Record.Timing` are non-pointer
    structs, so `"counts":{},"timing":{}` appears on every record — roughly 30
    bytes each. Harmless at v1 ceilings; worth pointers only if measurement says
    so.
  - **T04 (install path).** The transport takes the helper path as a validated
    parameter with no default, accepting an absolute or `~/`-relative path. The
    install location, `noexec` policy, and signing scheme remain T04's to choose.
