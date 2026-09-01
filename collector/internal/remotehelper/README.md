# Linux collection helper

`coslash-helper` parses Claude and Codex transcripts beside the data and writes
bounded protocol v1 records to stdout. The Mac keeps settings, cache,
composition, health, and UI. The helper keeps nothing: no daemon, no listener, no
state between runs, and no transcript ever leaves the machine.

## Commands

| Command | Input | Output |
| --- | --- | --- |
| `version` (alias `capabilities`) | none | one capability document |
| `collect` | one JSON request line on stdin | NDJSON records |

The request travels on stdin only. Nothing in it is ever treated as a path, a
name, or a command, and the remote command line carries no request data at all.

## Read boundary

Reads resolve beneath one open handle on the SSH user's home directory
(`os.Root`), so a path component swapped for a symlink mid-traversal cannot
redirect a read outside that tree. Directory entries that are symlinks are
dropped rather than followed, and files open with `O_NOFOLLOW` after an `lstat`
that already rejected links.

The allowlist matches the SFTP transport, so both transports read the same files:

```text
.claude/projects   .claude/sessions   .claude/jobs
.codex/sessions    .codex/archived_sessions   .codex/session_index.jsonl
```

Process liveness is the only probe outside those paths: signal 0 against a PID
that Claude's own session metadata names.

## Limits

Response limits arrive in the request. Traversal limits are helper-owned and not
negotiable: 200,000 directory entries, depth 16 beneath a root, 512 MiB per file,
and a four-minute deadline. Local reads cost disk and CPU rather than SSH
bandwidth, which is why the per-file bound is far above the SFTP one.

## Families and fingerprints

A family is one card's replacement unit. Grouping is cheap and happens before
any body is opened: Claude groups by transcript path, Codex by the parent chain
in header rows. Each family's fingerprint is a digest over its files' opaque
keys, sizes, and modification times, plus the approved metadata facts for its
sessions — so a session that only changed liveness or name still recollects,
which a file-only fingerprint would report as unchanged forever.

A fingerprint that matches the Mac's cached value yields `unchanged_family` and
no transcript read. Anything else is parsed. Fingerprint equality is an
optimisation, not proof of immutability: after parsing, every file is re-stated,
and a family whose files moved is re-parsed up to twice before it is reported
skipped with a structured reason.

Families outside the requested window are neither confirmed nor replaced. Their
absence from a response is never deletion, and the inventory still proves they
exist.

## Deletion

`vendor_complete` asserts that the vendor's whole allowlisted tree was
enumerated, so it is emitted only when the scan skipped nothing, hit no limit,
and finished inside the deadline. A missing vendor root is complete coverage of
zero families; an unreadable directory is not. Tombstones name known families
that a complete scan did not find, and they commit only against the bounded
authoritative inventory. An interrupted or incomplete scan therefore cannot
delete a cached family — it publishes what it collected and leaves the rest.

## Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | `request_complete` was emitted |
| 2 | usage error |
| 3 | ran, but coverage is partial |
| 4 | the request was rejected |
| 5 | internal failure |

126 and 127 come from the remote shell, not the helper, and mean the executable
is blocked or missing. The Mac maps each of these to a distinct health reason so
the UI can offer the right repair.

## Privacy

Only the facts in `internal/remotefacts` cross the boundary. stdout carries no
transcript rows, prompts, tool output, absolute paths, working directories, or
environment values, and stderr is bounded diagnostics that the Mac redacts before
showing. Card display names are derived from a session's own title or first
message, which the schema allows as bounded display text.
