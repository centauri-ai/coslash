# Complete-backup producer

`sessionbackupproducer` is the local owner of complete `session-backup/v1`
capture. It supports local and SSH Codex plus local Claude, local OpenCode,
and local Cursor IDE/CLI sources. Claude SSH, OpenCode SSH, and Cursor SSH
return the product-visible
`complete_backup_unsupported` blocker and never produce a metadata-only bundle.

`Manager.Start` exposes an asynchronous `preparing` state with `Status`,
`Wait`, and `Cancel`. `Prepare` is the synchronous, context-cancellable core
used by tests and non-HTTP callers. A successful preparation returns the
stable selection, structural coverage, exact byte totals, and complete backup
hash. A failed preparation returns coverage problems and leaves no completed
manifest.

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

Focused verification is:

```sh
go test ./internal/sessionbackupproducer
go test ./sessionbackup/v1/...
```
