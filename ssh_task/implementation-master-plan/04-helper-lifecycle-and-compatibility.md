# T04 — Helper lifecycle and compatibility

Status: not_started

Depends on: T03

## Objective

Implement explicit-consent platform detection, artifact authentication,
installation, reuse, upgrade, rollback, verification, and exact-scope uninstall.

## Context

The helper is not installed on every connection. Setup probes the remote host and
reuses a compatible verified helper. A missing or incompatible helper may be
installed/upgraded after consent. Declined, unsupported, `noexec`, or failed
installation leaves the host on best-effort SFTP.

## Decisions required before editing

- Initial Linux architectures and static/libc strategy.
- Exact approved versioned user-owned install base and `noexec` behavior.
- Signed release metadata, embedded trust root, rotation, and revocation.
- Whether initial consent may authorize later automatic helper maintenance.
- Protocol/schema support window, deprecation notice period, and behavior for
  deprecated, incompatible, and revoked helpers.
- Host disable/removal policy: disabling retains the helper; removal offers an
  explicit uninstall-before-forgetting choice and never silently deletes it.

## Scope

- Detect OS/architecture through fixed commands with bounded output.
- Select only an artifact in authenticated release metadata.
- Transfer to an exact versioned temporary path; verify size, signature/digest,
  executable format, ownership, and mode before activation.
- Use race-resistant no-follow, directory-relative operations; reject symlinked,
  unexpectedly owned, or broadly writable paths.
- Activate atomically, capability-check it, and retain the prior compatible
  helper until success.
- Reuse a compatible helper without reinstalling it.
- Mark still-compatible helpers as deprecated according to policy and expose an
  upgrade-required state before they leave the support window. Never execute an
  incompatible or revoked helper; return a visible degraded-SFTP option.
- Roll back after failed verification and classify stable lifecycle reasons.
- Uninstall only exact known helper paths and remove no unrelated data.
- Provide an uninstall operation that T05 can complete before local host settings
  are removed. Disabling alone performs no remote write or uninstall.
- Make every operation idempotent and safe after interruption.

## Acceptance criteria

- Repeated setup with a compatible helper performs no upload/reinstall.
- Missing, compatible-old, incompatible, tampered, revoked, interrupted,
  `noexec`, rollback, and exact uninstall cases are deterministic.
- Current, deprecated-but-compatible, unsupported, and revoked version states map
  to explicit reuse, upgrade prompt, or non-execution behavior.
- No operation requires root or mutates agent data.
- Failure/decline returns a usable, accurately labeled SFTP state.

## Focused tests

Use temporary directories and fake SSH/transfer operations. Run lifecycle and
adjacent exec tests only. Build/verify one local test artifact; do not publish or
run the release matrix.

## Out of scope

No general package manager, self-update, privileged fallback, manager/UI
integration, or release rollout.
