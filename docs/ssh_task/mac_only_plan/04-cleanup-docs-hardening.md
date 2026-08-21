# P4 — Remove Linux collector surfaces and harden integration

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
