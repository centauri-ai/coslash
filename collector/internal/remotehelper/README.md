# Linux collection helper

`coslash-helper` parses Claude and Codex transcripts beside the data and writes
bounded protocol v1 records to stdout. The Mac keeps settings, cache,
composition, health, and UI. The helper keeps nothing: no daemon, no listener,
and no state between runs. Raw transcript rows never leave the Linux host;
complete supported parsed Codex records do cross the trusted SSH boundary.

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

Process liveness is probed outside those paths. Claude uses signal 0 against
a PID named in its own session metadata. Codex uses `lsof -a -c codex -Fn`,
which lists rollout files a local Codex process has open and does not contact
the network. The SFTP fallback runs that same `lsof` command over SSH. A
missing or failed probe leaves the session without a live status. Separately
from the helper, the Mac runs one bounded `git remote get-url origin` over SSH
for up to 64 working directories that sessions recorded with a branch; see
[data and privacy](../../../docs/data-and-privacy.md).

## Limits

Response limits arrive in the request. Traversal limits are helper-owned and not
negotiable: 200,000 directory entries, depth 16 beneath a root, 512 MiB per file,
and a four-minute deadline. Local reads cost disk and CPU rather than SSH
bandwidth, which is why the per-file bound is far above the SFTP one.

## Families and fingerprints

A family is one card's replacement unit. Grouping is cheap and happens before
any body is opened: Claude groups by transcript path, Codex by the parent chain
in header rows. For Codex, unchanged file size/mtime fingerprints reuse bounded
opaque key-to-session/parent mappings supplied with the known baseline; only new
or changed files reread their header. Each family's fingerprint is a digest over
its files' opaque keys, sizes, and modification times, plus the approved metadata
facts for its sessions — so a session that only changed liveness or name still
recollects, which a file-only fingerprint would report as unchanged forever.

A fingerprint that matches the Mac's cached value yields `unchanged_family` and
no transcript read. Anything else is parsed. Fingerprint equality is an
optimisation, not proof of immutability: after parsing, every file is re-stated,
and a family whose files moved is re-parsed up to twice before it is reported
skipped with a structured reason.

One changed-family line carries the bounded display facts and exactly one
complete record for every session in that family. The helper measures the whole
line with the same wire encoder used for output. If the aggregate cannot fit the
negotiated per-record limit, it emits a `vendor_budget_exceeded` skip and
withholds completion; it never publishes only the root or another subset. If
whole changed families collectively exceed the response limit, it reserves the
completion envelope, publishes the families that fit as limited coverage, and
persists a cursor so the next incremental refresh starts with the omitted tail.

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
delete or replace cached data. Whole records received before the failure remain
diagnostic proposal state only; the Mac retains its prior complete durable
generation.

## Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | `request_complete` was emitted |
| 2 | usage error |
| 3 | ran, but coverage is partial |
| 4 | the request was rejected |
| 5 | internal failure |
| 6 | negotiated output/resource limit reached |

126 and 127 come from the remote shell, not the helper, and mean the executable
is blocked or missing. The Mac maps each of these to a distinct health reason so
the UI can offer the right repair.

## Privacy

stdout carries bounded `internal/remotefacts` rows plus, for changed Codex
families, a validated `full-session-record/v1`. The complete record can contain
parsed prompts, summaries, commands, working directories, subagent detail, and
file-change bodies. It contains no raw transcript rows, SSH configuration,
coSlash credentials, sockets, or environment values. stderr remains bounded
diagnostics that the Mac redacts before showing.
