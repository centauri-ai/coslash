# P1 — Shared parser boundary and read-only SFTP

## Purpose

Prove the Mac can safely discover and parse remote Claude/Codex files through the
system SSH client without installing or executing coSlash on Linux.

## Required outcome

- Introduce a small read-source interface for bounded `ReadDir`, `Stat`, `Open`,
  and canonical path resolution.
- Adapt Claude/Codex discovery and parsers to explicit roots/readers while keeping
  existing local collection behavior and OpenCode untouched.
- Add an OS-backed local source and an SFTP-backed remote source.
- Start system SSH in noninteractive subsystem mode and connect
  `github.com/pkg/sftp.NewClientPipe` to its pipes.
- Resolve the SFTP home, enforce the design allowlist, reject symlinks/escapes,
  and expose no write operations.
- Add deterministic inventory selection and byte/file/depth/deadline limits.
- Measure representative small, large, append-only, forked, and torn transcript
  fixtures; use the results to lock constants before P2.

## Scope

Expected ownership is vendor discovery/parser seams plus a focused SFTP transport
package. Do not add settings, cache, HTTP routes, frontend behavior, or remove old
Linux feature code in this packet.

## Acceptance

- OS and memory/SFTP-shaped sources produce equivalent normalized Claude/Codex
  facts except for documented source-specific liveness/Git facts.
- Exact SSH argv is tested; alias text is one validated argv element and no remote
  shell command is supplied.
- Tests cover SFTP negotiation, missing roots, permission errors, symlinks,
  canonical escape, cancellation, stderr bounds, file/count/depth/byte limits,
  torn JSONL, forks, and process reaping.
- Claude PID validation is best effort through numeric `/proc/<pid>` stat only;
  Codex remote liveness remains unknown.
- Local collection regressions fail existing tests; OpenCode never touches the
  remote source.

## Verification

From `collector/`:

```text
gofmt -l ./internal
go vet ./...
go test ./...
go test -race ./internal/remote/...
```

## Handoff to P2

Document the read-source API, normalized collection result, error categories,
locked limits, measured transfer sizes/times, and shutdown contract.

## Implementation record

Status: Done locally on `ssh-mac/p1-parser-sftp` on 2026-08-21.

Delivered:

- `vendors.ReadSource` with OS-backed discovery, parsing, JSON sidecars, metadata,
  modification times, workflow files, and fork-parent reads;
- strict remote parsing that returns any main-transcript failure instead of
  accepting a partial refresh;
- `remote.OpenSession`, which requests only the SFTP subsystem through system
  SSH and owns cancellation, bounded stderr, process cleanup, and a read-only
  source;
- canonical allowlisted roots, symlink rejection, numeric Claude `/proc/<pid>`
  liveness checks, and file/total-byte/entry/depth limits;
- deterministic newest-file selection with explicit candidate/selected counts
  and truncation in `vendors.RemoteCollection`;
- Mac-side Claude and Codex remote collectors. Codex remote approval state is
  conservative and never invokes the Mac's Codex CLI for a Linux session.

Locked guardrails:

| Limit | Value |
|---|---:|
| Connect timeout | 8 seconds |
| Refresh deadline | 30 seconds |
| One file | 32 MiB |
| One refresh | 128 MiB |
| Candidate files per agent | 2,000 |
| Directory entries per refresh | 10,000 |
| Relative path depth | 16 |
| Diagnostic stderr | 8 KiB |

Measurement: an in-process SFTP server transferred a synthetic 4 MiB file in
about 9 ms on the development Mac. This is a deterministic regression fixture,
not a real-network performance claim. Exact boundary tests accept 32 MiB and
reject 32 MiB plus one byte. P2 should retain these ceilings until real-host
acceptance provides evidence to lower them.

Verification passed from `collector/`:

```text
gofmt -l ./cmd ./internal
go mod verify
go vet ./...
go test ./...
go test -race ./internal/remote/...
```

The focused suite covers OS/SFTP fact equivalence, missing optional roots,
permissions, symlinks and canonical escape, byte/entry/depth limits, strict
partial-parse failure, torn JSONL, Codex fork accounting, exact SSH argv,
handshake cancellation, stderr overflow, and SSH child reaping.
