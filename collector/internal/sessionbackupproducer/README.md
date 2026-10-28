# Complete-backup producer

`sessionbackupproducer` is the local owner of complete `session-backup/v1`
capture. It supports local and SSH Codex plus local Claude, local OpenCode,
local Cursor IDE/CLI, local Grok, and local Pi sources. Claude SSH, OpenCode SSH, and Cursor SSH
return the product-visible
`complete_backup_unsupported` blocker and never produce a metadata-only bundle.

`Manager.Start` exposes an asynchronous `preparing` state with `Status`,
`Wait`, and `Cancel`. `Prepare` is the synchronous, context-cancellable core
used by tests and non-HTTP callers. A successful preparation returns the
stable selection, structural coverage, exact byte totals, and complete backup
hash. A failed preparation returns coverage problems and leaves no completed
manifest.

Cancellation and deadline expiry retain their context error identity, including
when parsing or writing a frozen artifact was interrupted. They do not establish
that the source is malformed; callers may retry the unchanged input. Actual
invalid data still returns the specific preparation coverage problem.

Raw rollouts are streamed once from the live source to a private spool while
their hashes and lengths are computed. After the live source passes its
post-copy fingerprint check, parsing runs only against those frozen rollout
files; processed records and exact changes therefore describe the same bytes
that the bundle retains without a second full SSH read. Revision-matched
persisted synthesis is attached for local sources. The spool
survives collector restarts and remains available through `Open` and bounded
`Read` calls until `Discard` is explicit. Cancellation observed through the
publishing handoff removes its new bundle but never discards a reused bundle.
The spool root and bundle directories are mode `0700`; artifact files and
manifests are mode `0600`. Local OpenCode projects family-attributed SQLite rows
and parses them in one read transaction. A source write observed during
preparation blocks completion.

Before a processed record is frozen for upload, absolute file-edit paths inside
the recorded working directory become repository-relative. Paths elsewhere
under the source home become `~/…`; paths outside both are reduced to their
final path component. This applies across supported readers and the sanitized
paths determine the record revision. Windows drive paths use the same rules.

The Codex producer scans both `.codex/sessions` and `.codex/archived_sessions`, and
reads only the attributable rows of `.codex/session_index.jsonl`. It does not
open a Codex database, helper/cache implementation files, credentials, or
machine-wide configuration. Any skipped discovery path or unreadable rollout
header fails the complete capture rather than being omitted. Capture errors
use stable blocker codes and must not include source content, paths, or
identities in logs or diagnostics.

The Claude producer scans `.claude/projects` without a recent-file cap and
freezes every selected family transcript plus present subagent metadata,
workflow state, and workflow journal inputs. Present malformed or unstable
inputs block completion. Parsed records and optional revision-matched persisted
synthesis are built from the frozen files. A failed refresh keeps earlier
verified spool revisions available.

The Cursor producer binds each local transcript fragment to one session ID and
projects only attributed rows from IDE `state.vscdb`, conversation search and
tracking databases, or the CLI's per-session chat store. CLI `meta.json` is
retained exactly because it supplies the working directory. Older CLI versions
leave `cwd` out of it; the chat's parent folder is named by the MD5 of its
working directory, so another chat in that folder whose recorded `cwd` has that
MD5 supplies it. Resuming a CLI chat from another folder can leave an empty
stub store for the same chat; beside the real store, the stub holds nothing and
is left out, while two stores that both hold rows stay ambiguous. A chat with
no working folder, such as an IDE chat in an empty window, backs up with the
explicit empty repository (`"vcs": "none"`). The verified parsed
record carries `cursor-ide` or `cursor-cli`: IDE continuation opens the
workspace and never claims exact chat resume; CLI continuation may resume the
specific chat. Missing, ambiguous, changing, or cross-session inputs block
publication. Attributed rows that `session-backup-db-rows/v1` cannot carry,
such as text that is not UTF-8 or a value over the 64 MiB document bound, also
block publication; they are reported as non-retryable `artifact_invalid`
metadata rows, not as unattributable. Cursor SSH remains owned by the later
relay task.

The Grok producer freezes each verified root and recursive subagent family in
root-first order. Selecting a child resolves to that root; forks and worktree
sessions stay independent. It retains the authoritative `updates.jsonl`,
`summary.json`, and present usage, signals, plan, goal-state, prompt-context,
compaction and subagent metadata/output inputs. External fork/worktree
classification retains only the attributable summary proof, never its transcript.
Missing required, malformed
present, ambiguous or changing family inputs block publication. Parsing uses
only the private frozen tree, without active-session metadata or process probes.
Usage without a model breakdown remains unattributed, and partial or missing
cost remains unknown. Grok SSH remains unsupported.
The queue reuses this producer's input enumeration for bounded source hashes,
including all recursive members and optional input presence. Discovery activity
and canonical root revision do not substitute for this family source revision.
The revision hook returns ordinary failures per family. Cancellation and deadline
errors stop the scan and retain their context error identity. Exported revision
errors use the same preparation classification as capture, including size limits.

Pi capture selects one unique header ID from the configured local roots.
It retains exact root JSONL and up to 256 ancestor transcripts as private evidence.
Every artifact belongs to the selected root. Fork ancestry never creates
additional members or subagent parents. Parsing reads only the frozen copies.
Stable partial tails and unavailable ancestry preserve useful detail with
incomplete-detail status and truthful accounting. Invalid root data blocks capture.

`PiSourceRevision(ctx, inputs)` accepts `pi.SessionInputs` from one planning
snapshot. It uses the same bounded temporary copies, parser, and normalization
as capture. Both source and member revisions hash indexed file lengths and
SHA-256 values, effective leaf selection, and the portable record revision.
Portable composition uses the fixed internal source ID `pi-source-revision`.
This includes ancestry verification, context, accounting, and observed Skills.
Runtime modality, PID, heartbeat, and original evidence paths remain excluded
from curated records. Paths recorded inside native bytes remain private evidence.

Before publication, capture obtains fresh discovery and runtime evidence.
It checks input identity and presence, file identity, and actual byte hashes.
It then projects the frozen copies against the new effective selection.
This detects leaf-only changes and same-size edits with restored timestamps.
The queue must compare its source revision with the prepared manifest before upload.
V4 transfers only parsed records. Pi SSH remains unsupported.

Focused verification is:

```sh
go test ./internal/sessionbackupproducer
go test ./sessionbackup/v1/...
```
