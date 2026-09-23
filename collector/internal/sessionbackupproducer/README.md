# Complete-backup producer

`sessionbackupproducer` is the local owner of complete `session-backup/v1`
capture. It supports local and SSH Codex sources. Other agents return the
product-visible `complete_backup_unsupported` blocker and never produce a
metadata-only bundle.

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
`Read` calls until `Discard` is explicit. Cancellation observed before
publication leaves only private staging behind; the atomic rename is the commit
point, after which cancellation cannot discard a published or reused bundle.
The spool root and bundle directories are mode `0700`; artifact files and
manifests are mode `0600`.

The producer scans both `.codex/sessions` and `.codex/archived_sessions`, and
reads only the attributable rows of `.codex/session_index.jsonl`. It does not
open a Codex database, helper/cache implementation files, credentials, or
machine-wide configuration. Any skipped discovery path or unreadable rollout
header fails the complete capture rather than being omitted. Capture errors
use stable blocker codes and must not include source content, paths, or
identities in logs or diagnostics.

Focused verification is:

```sh
go test ./internal/sessionbackupproducer
go test ./sessionbackup/v1/...
```
