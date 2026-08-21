# P4 — Remove Linux collector surfaces and harden integration

Status: done locally on 2026-08-21, including a real-host timing check.

## Purpose

Finish the replacement: remove the abandoned Linux-processing implementation,
align release/privacy/troubleshooting docs, and validate the complete Mac-only
data path.

## Required outcome

- Delete remote snapshot/probe framing, Linux launch/handoff execution and storage,
  feature-specific Linux release archives/smoke jobs, and collector installation
  docs.
- Remove obsolete capability/version/launchable-agent API fields and frontend copy
  after confirming no active consumer remains.
- Update README, privacy, troubleshooting, diagnostics, and release behavior to
  state the exact Linux prerequisites and Mac cache/transfer behavior.
- Add an end-to-end fake SSH/SFTP test that starts from settings, reads agent
  fixtures, serves API data, refreshes changed files, survives failure, and removes
  the source safely.
- Perform one manual check against a real Linux host with no coSlash binary.

## Acceptance

- Repository search finds no product path requiring `~/.local/bin/coslash` on a
  remote host and no Linux handoff cache.
- Release output returns to supported macOS artifacts unless Linux builds are
  independently required by another feature at implementation time.
- Docs clearly distinguish: system SSH on Mac, SSH/SFTP server and agent data on
  Linux, and no Linux coSlash installation.
- Privacy docs state which remote files may be read, transfer/cache limits, that
  raw transcript bytes are not persisted, and how disable/remove behave.
- Full Go/frontend verification passes, including race tests and fake-host failure
  cases.

## Verification

Run the master-plan verification baseline, release checks relevant to the final
artifact set, `git diff --check`, and repository searches for all removed command,
capability, installation, and handoff identifiers.

## Completion note

Record measured real-host refresh time/bytes, the absence of a Linux coSlash
binary, final guardrails, residual limitations, and any follow-up optimization
that is justified by evidence.

## Implemented locally

- Repository and release searches confirm this lineage contains no active remote
  snapshot/probe frame, Linux launch/handoff store, remote install guide, Linux
  coSlash release archive, or remote `~/.local/bin/coslash` dependency. The
  unrelated local `session-snapshot/v1` sharing serializer remains.
- README, privacy, and troubleshooting now state the SSH/SFTP-only Linux
  prerequisites, exact read allowlist, safety ceilings, Mac-only parsing, raw-byte
  persistence boundary, monitoring-only actions, and disable/remove behavior.
- An end-to-end test starts from remote settings, launches the fake system-SSH
  process, serves a real read-only SFTP protocol, parses Claude and Codex files on
  the Mac side, fills the normalized cache, retains last-good sessions after a
  connection failure, and removes only that source cache.
- The shipped implementation records opaque path/size/mtime fingerprints but
  reparses bounded selected files. Reusing normalized per-file facts is deferred
  until real-host measurements justify the added cache complexity.

Automated Go, race, frontend, formatting, and production-build checks pass. A
manual check through a configured proxy-backed OpenSSH alias also passed:
ordinary SFTP negotiation succeeded, bounded connection testing completed in
about 6.0 seconds, and background refresh returned one normalized Codex session
in about 5.4 seconds. Claude had no candidate files on that account. The path
executed no Linux coSlash command and required no Linux-side installation.

The current health contract records round-trip time and bounded file counts, not
transport bytes, so the manual check did not claim a byte measurement. The proxy
client emitted an optional Mac-side performance dependency suggestion; it is not
a Linux prerequisite. A live network interruption was not induced; the
automated mid-refresh disconnect test covers last-good fallback.
