# Mac-only remote session design

Status: approved for implementation on 2026-08-21. This supersedes the collector-on-Linux design in
[`02-remote-hosts-technical-design.md`](02-remote-hosts-technical-design.md) for the next implementation, but preserves the completed plan as historical context.

## Purpose

Let the macOS coSlash app display Claude Code and Codex sessions stored on one
Linux host without installing or running coSlash on Linux. SSH transports files;
all coSlash discovery, parsing, aggregation, caching, and API work runs on the Mac.

## Required outcome

- Linux requires only its existing SSH server with an enabled SFTP subsystem and
  read access to the user's agent data.
- coSlash uses the Mac's system `ssh` client and existing SSH aliases, keys,
  host-key checks, `ProxyJump`, and agent configuration.
- Claude and Codex transcript data is read through SFTP and parsed locally.
- Raw remote transcripts are not written to the Mac cache. Only normalized,
  bounded session facts and file fingerprints are persisted.
- Local sessions continue to include Claude, Codex, and OpenCode. Remote v1
  includes Claude and Codex only.
- Connection failures preserve the last good remote view and never block local
  collection.

## Product boundary

This redesign is monitoring-first.

| Behavior | Mac-only v1 |
|---|---|
| List, cards, inspector, cost, token and file-edit facts | Supported from remote files |
| Names and transcript-derived branch/cwd | Supported when present in agent data |
| Claude liveness | Best effort only when its PID metadata and `/proc/<pid>` are visible through SFTP |
| Codex liveness | Unknown; SFTP cannot reproduce the current local `lsof` probe |
| Git dirty state or live branch probe | Not supported; no remote Git commands or worktree scan |
| Copy handoff | Supported; generated on the Mac |
| Resume / Start Fresh | Deferred; hidden for remote sessions |
| Remote Commands, diff, synthesis, preview, and sharing | Remain local-only unless separately designed |

The UI must not represent an old or recently modified transcript as a proven live
process. Recency and liveness are separate facts.

## Architecture

```text
macOS coSlash
  -> system ssh using the saved alias
  -> request the SFTP subsystem
  -> enumerate and stream allowlisted agent files
  -> parse with local Claude/Codex adapters
  -> normalize, bound, and cache session facts
  -> serve the existing source-aware API and UI

Linux
  -> OpenSSH/SFTP file operations only
  -> no coSlash binary, daemon, package, cache, handoff store, or parsing
```

### Transport

The Mac starts a noninteractive system SSH process with `BatchMode=yes`, an
eight-second connect timeout, no TTY, the validated alias as one argv element,
and the `sftp` subsystem. A Go SFTP client is connected to the process stdin and
stdout. `github.com/pkg/sftp.NewClientPipe` explicitly supports this system-SSH
shape; the dependency is compiled into the Mac binary and installs nothing on
Linux.

The transport resolves the remote SFTP home with `RealPath(".")`. SFTP paths use
POSIX path semantics and never rely on shell `~` expansion. The initial read
allowlist is:

- `.claude/projects`, `.claude/sessions`, and `.claude/jobs`;
- `.codex/sessions`, `.codex/archived_sessions`, and
  `.codex/session_index.jsonl`;
- numeric `/proc/<pid>` entries only for best-effort validation of a PID already
  obtained from Claude metadata.

Canonical roots are resolved once. Directory traversal rejects symlinks and any
canonical path outside those roots. The transport exposes only read, stat, and
bounded directory-list operations; no write/remove/chmod methods enter the
coSlash interface.

### Local parser boundary

Current vendor code directly calls `os`, `filepath`, `exec`, and
`os.UserHomeDir`. Refactor only Claude/Codex file discovery and parsing behind a
small read-source interface. Local collection uses an OS-backed source; remote
collection uses an SFTP-backed source. Parsers accept `io.Reader` plus explicit
path/stat metadata so both sources share parsing behavior.

Process and repository probes stay outside the portable parser. Each source
supplies the facts it can prove; missing facts remain unknown rather than being
inferred.

### Refresh and cache

- At most one SFTP refresh is active for the configured host.
- The first refresh lists bounded directory depth/count and selects recent roots
  before opening transcript content.
- A fingerprint consists of canonical relative path, size, and modification
  time. Unchanged files reuse normalized cached facts.
- Changed files are streamed and reparsed under per-file, per-refresh, session,
  and time caps. A truncated refresh is visible as incomplete and does not claim
  full coverage.
- A successful refresh atomically replaces normalized cache state. Failure keeps
  the prior view and uses bounded retry/backoff.
- Raw transcript bytes exist only in bounded in-memory readers during parsing and
  are never logged or persisted by the remote cache.

The first implementation should reparse a changed file completely. Incremental
JSONL tail parsing is a later optimization only if measurements show the bounded
approach is too expensive.

## Security and privacy constraints

- Accept one SSH alias matching the existing closed alias grammar; do not accept
  a hostname, key path, password, port, remote command, or arbitrary remote root
  from the API.
- Preserve OpenSSH host-key verification. Never add `StrictHostKeyChecking=no` or
  a replacement known-hosts file.
- Background refresh uses `BatchMode=yes`; setup guidance may ask the user to run
  `ssh <alias>` manually once.
- Bound stderr and redact it before diagnostics. Never include transcript text,
  remote absolute paths, credentials, or resolved SSH configuration.
- Cancel and reap the SSH child on timeout, settings removal, or app shutdown.
- Removing a machine deletes only its normalized local cache. Disabling keeps the
  cache but hides the source and stops refreshes.

## Removed Linux-side surfaces

The replacement implementation deletes or does not port:

- `coslash snapshot`, `snapshot --probe`, `launch`, and `handoff put` remote
  subcommands;
- the `remote-session-view/v1` framed Linux-to-Mac snapshot contract;
- the Linux handoff store and Linux agent execution path;
- Linux coSlash release archives and remote installation instructions added only
  for this feature.

The source-aware settings, identity, cache lifecycle, API envelope, health UI,
machine badges, stale fallback, diagnostics, and Copy handoff behavior remain
useful and should be adapted rather than rebuilt.

## Acceptance

- A Linux fixture with only OpenSSH/SFTP and Claude/Codex data appears in the Mac
  board without a coSlash executable on Linux.
- Tests fail if the background path supplies a remote shell command or writes
  through SFTP.
- Host aliases using normal OpenSSH config, including a fake `ProxyJump` fixture,
  reach the same system-SSH argv boundary.
- Host-key, authentication, permission, missing-root, timeout, cancellation,
  oversized-file, excessive-directory, torn-JSONL, and mid-refresh disconnect
  cases return stable bounded health and retain last-good data.
- Local parser fixtures produce the same normalized facts through OS and SFTP
  read sources, except for explicitly unavailable liveness/Git facts.
- No Linux artifact or Linux installation step remains in release/docs for this
  feature.

## Locked decisions

- Remote v1 is monitoring-first: Resume and Start Fresh are deferred; Copy
  handoff remains available.
- P1 starts with safety ceilings of 32 MiB per file, 128 MiB per refresh, 2,000
  candidate files per agent, and a 30-second refresh deadline. P1 must measure
  representative fixtures and may lower these limits, or propose a documented
  change with evidence, before P2 consumes them.

## Technical references

- OpenSSH documents `-s` as a subsystem request and `-T` as disabling TTY
  allocation: [ssh(1)](https://man.openbsd.org/ssh.1).
- OpenSSH documents `BatchMode=yes` as disabling password and host-key prompts:
  [ssh_config(5)](https://man.openbsd.org/ssh_config.5).
- The SSH server must expose an SFTP subsystem such as `sftp-server` or
  `internal-sftp`: [sshd_config(5)](https://man.openbsd.org/sshd_config.5).
- The selected Go client supports connecting to a system `ssh` process through
  stdin/stdout: [`sftp.NewClientPipe`](https://pkg.go.dev/github.com/pkg/sftp#NewClientPipe).
