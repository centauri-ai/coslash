# Windows support stack validation handoff

This document preserves the Windows validation context for a follow-up Codex
session on another machine. It is intentionally committed to
`cvu/windows-support`, not to the stacked pull requests.

Last updated: 2026-09-21 (America/Los_Angeles).

## Current stack

The Windows work is split across seven dependent pull requests. The head SHAs
below were current when this handoff was written.

| PR | Branch | Responsibility | Current head |
| --- | --- | --- | --- |
| [#250](https://github.com/centauri-ai/coslash/pull/250) | `cvu/windows-01-foundation` | Windows amd64 compilation and operating-system boundaries | `1abe27ded2757ee96e3cd2544c2bb47b74d36c6a` |
| [#251](https://github.com/centauri-ai/coslash/pull/251) | `cvu/windows-02-security` | Credential Manager, ACLs, and protected local state | `2fad8f9669bb127f6dd32a2baad79b4247513d39` |
| [#252](https://github.com/centauri-ai/coslash/pull/252) | `cvu/windows-03-discovery` | Claude, Codex, and OpenCode discovery | `83c006579cb6a5948533b2118c9fddbf00b71fb4` |
| [#253](https://github.com/centauri-ai/coslash/pull/253) | `cvu/windows-04-launch` | Native launch, browser, terminal, settings, and diagnostics | `168f39dee5f568ebd321c15aab1baa3754447afc` |
| [#254](https://github.com/centauri-ai/coslash/pull/254) | `cvu/windows-05-remote` | Guided SSH, remote collection, resume, and handoff | `2bc31b53aa7ed5b8bf48c66394d41991aca01ca4` |
| [#255](https://github.com/centauri-ai/coslash/pull/255) | `cvu/windows-06-cursor` | Cursor discovery and launch | `65f25eb5a2777c9d969522b361d83388328087c3` |
| [#256](https://github.com/centauri-ai/coslash/pull/256) | `cvu/windows-07-packaging` | Windows artifacts and candidate validation | `c0800995ed34afbcefb315aba0e9e367ab24a708` |

All Collector, Frontend, and Release jobs passed on these heads. PRs #255 and
#256 were rebased after the final #254 fix; their own patches remained
equivalent. No planning or progress documents are committed in the stack.

## Validated environment

The committed result at
`docs/windows-validation-results/windows-amd64.json` records a complete pass
for the earlier monolithic `cvu/windows-support` implementation at source
commit `ca2d9327ddc55bb03b096f90292d9db4c3606cc6`:

- Windows 11 Pro 24H2, build 26100, amd64.
- Standard non-administrator user.
- Windows PowerShell 5.1.26100.7920.
- Windows Terminal available.
- Defender antivirus and real-time protection enabled.
- Windows package tests, `go vet`, native build, and version check passed.
- Claude, Codex, and OpenCode discovery, resume, and fresh handoff passed.
- Paths containing spaces, non-ASCII text, emoji, and handoff cleanup passed.
- Session-state and UI parity, browser launch, credential restart, Windows to
  Linux SSH, and standard-user install/replace checks passed.
- Cursor session counting and filtering were also observed during parity
  validation.

That result is evidence for the monolithic branch, not a substitute for
rerunning the final stacked candidate. The stack was subsequently rebased and
received test-isolation, launch, remote-lifecycle, Cursor, and packaging
follow-ups.

Two later automated reports were deliberately kept outside the repository:

- `$env:TEMP\coslash-windows-validation.json`
- `$env:TEMP\coslash-windows-candidate-validation.json`

Both passed at the then-current packaging head, including package tests,
`go vet`, native build/version, and packaged-executable smoke coverage. The
candidate smoke test verified the embedded frontend, authenticated API, and
embedded Linux helper assets. The candidate executable was also kept outside
the repository at `$env:TEMP\coslash-windows-amd64.exe`.

Do not assume these machine-local paths exist on another Windows machine, and
do not commit generated JSON reports or binaries to the stacked PRs.

## Findings and fixes by PR

### #250: foundation

- Split Unix-only process, lock, permission, and browser behavior behind build
  tags so the collector builds natively for Windows amd64.
- Established Windows-safe defaults without making WSL a runtime dependency.

### #251: security

- Added Windows Credential Manager support and Windows ACL enforcement for
  tokens, settings, synthesis caches, remote caches, and metadata.
- Isolated credential tests from the real user vault. Tests must use disposable
  adapter state and must not depend on credentials installed on the developer's
  machine.

### #252: discovery

- Added native Windows discovery for Claude, Codex, and OpenCode data and live
  process metadata.
- Removed test dependencies on the developer's actual profile, installed agent
  CLIs, live processes, and session databases by adding injectable platform
  adapters and controlled fixtures.

### #253: launch, diagnostics, and local parity

- Added native Windows terminal and browser launching. Windows PowerShell is the
  supported shell, with Windows Terminal preferred when available and a
  PowerShell fallback when it is not.
- Removed `/bin/sh` assumptions from portable launch and review tests.
- Updated OpenCode review arguments and Windows Cursor executable resolution.
- Corrected diagnostics and guidance so a native backend reports Windows agent
  locations and launch capability instead of Unix `~/.…` paths.

### #254: remote SSH lifecycle

- Removed Unix SSH-control-socket assumptions from Windows paths while retaining
  the optimized Unix implementation behind platform files.
- Added guided authentication through a terminal, refresh, remote resume, fresh
  handoff, cancellation, Windows-safe metadata persistence, and process cleanup.
- Centralized timeout behavior, created fresh SSH state for tests, and separated
  POSIX-only fixtures from Windows package tests.
- Clarified that interactive password authentication is not reusable background
  authentication. Reuse requires `ssh-agent` or another configured key agent;
  coSlash must never persist the password.
- Fixed a Linux CI regression in commit `2bc31b5`: clean stdout EOF was causing
  local cleanup to mask meaningful helper exit codes such as resource exhaustion
  and missing executable. Clean EOF now preserves the helper or shell exit
  classification, while malformed or oversized streams are still terminated
  immediately and all waits remain bounded.

The final #254 fix reproduced the failing Linux package test on an authorized
Linux host, then passed `go test ./internal/remote`. GitHub CI subsequently
passed Collector, Frontend, and Release.

### #255: Cursor

- Added native Cursor session paths, live-process mapping, executable discovery,
  resume, and launch behavior.
- Restacked without patch changes after the #254 lifecycle fix.

### #256: packaging

- Added Windows amd64 artifact construction and validation workflow coverage.
- `make -C collector dist` now cross-compiles the native backend with
  `CGO_ENABLED=0 GOOS=windows GOARCH=amd64`, the embedded frontend, and both
  Linux helper assets. Its persistent output is
  `collector/dist/windows/coslash-windows-amd64.exe`.
- Added a PowerShell validation script and persistent candidate smoke test.
- The smoke test checks that the packaged native executable serves its embedded
  frontend, enforces API authentication, and contains both Linux helper assets.
- The release build asserts that the executable exists, writes a SHA-256
  checksum, and executes the artifact on a `windows-2025` runner to verify its
  version and packaged behavior.
- Restacked without patch changes after the #254 lifecycle fix.

The native `.exe` build is therefore present, but release publication is not
complete. The workflow's `publish` job currently downloads only the macOS
`release-artifacts`, and its asset list contains only `dist/*.tar.gz` and
`dist/checksums.txt`. It does not download or upload the separate
`windows-release-artifacts` containing the executable and Windows checksum.
The `publish` job also depends on `smoke` but not `smoke-windows`, so a Windows
smoke failure would not prevent publication. Both corrections belong in #256:

1. Make `publish` depend on `smoke-windows`.
2. Download `windows-release-artifacts` in `publish` and add the executable and
   its checksum to the release asset upload and verification logic.

PR CI being green does not close this gap: its job named `Release` builds and
smokes the macOS development/release binaries; it does not exercise a tagged
GitHub Release publication.

## Architecture decision: native Windows, not WSL-only

WSL should not be required to run coSlash on Windows. A backend running inside
WSL sees that distribution's Linux home, processes, paths, credentials, and
terminal behavior. It cannot provide correct native discovery and launch for
Windows Claude, Codex, OpenCode, or Cursor sessions, Windows Credential Manager,
Windows ACLs, Windows Terminal, or the default Windows browser.

WSL remains useful as an optional Linux test environment. The shipped Windows
application and diagnostics must use the native Windows backend. If diagnostics
are opened against a backend intentionally running inside WSL, Linux paths and
Linux session visibility are expected and should not be presented as native
Windows support.

## Validation still required on the final stack

Run the following against exact head
`c0800995ed34afbcefb315aba0e9e367ab24a708`, or update this document with the
new exact head if the stack moves first:

1. Run `collector/scripts/windows-validation.ps1` from Windows PowerShell 5.1
   as a standard user, once from source and once against the persistent packaged
   executable. Keep both JSON outputs outside the repository.
2. Confirm native diagnostics show Windows paths and capabilities for Claude,
   Codex, OpenCode, and Cursor.
3. For each agent, verify discovery, exact resume, fresh handoff, a path with
   spaces, and a practical non-ASCII path.
4. Verify browser launch, settings persistence, diagnostics, and terminal
   fallback behavior.
5. Against an authorized Linux SSH host, verify Authenticate, refresh, remote
   session discovery, resume, fresh handoff, and clean cancellation.
6. Inspect `Get-Service ssh-agent` and `ssh-add.exe -l` without enabling a
   service, elevating, changing system configuration, or storing a password.
7. Confirm standard-user packaging/install behavior and record unmodified
   Defender and SmartScreen results. Unsigned locally built files without
   Mark-of-the-Web do not exercise the same reputation path as a downloaded
   release artifact.
8. After the #256 publication fix, run a release dry-run and confirm the Windows
   executable, its checksum, and the `smoke-windows` gate are all included.
9. Finish with `git status --porcelain`; the tested checkout must remain clean.

## Guardrails for the next session

- Fetch before changing the stack: the branches were rebased concurrently
  during validation. Use exact `--force-with-lease` values and publish dependent
  branches bottom-up or atomically.
- Put fixes in the earliest PR that introduces the failing invariant, then
  rebase later PRs without changing their patches.
- Do not weaken or delete tests to obtain a pass. Tests must not depend on the
  developer's installed agents, credentials, live sessions, network access,
  wall-clock timing, or unordered maps.
- Do not commit generated validation JSON, executables, transcripts, logs,
  screenshots, credentials, host details, or planning/progress documents to the
  stacked PRs.
- Sanitize failures before sharing them. Preserve the error category and
  reproduction while omitting usernames, hostnames, credential material, and
  session content.
