# Hub sharing is explicit and consent binds to the exact reviewed audience

- Status: Accepted
- Date: 2026-08-11
- Source: eb6e2ca5, 87121d50, 752d0940, 249c6962

## Decision

- Sharing to coSlash Hub is opt-in and off by default. Session data leaves the machine only after the user starts a share and approves it.
- There is no standing auto-share rule, no repository rule, and no background sync. Each share covers one session or one bounded batch that the user selected.
- Before approval, the user sees the exact content that will be sent and the destination. The destination is the workspace name, its audience version, and its current member count.
- Approval binds the frozen source revision, the content hash and byte count, the destination workspace, the audience version and member count, the server identity, and the advertised capacities. A change in any of them requires a new review.
- The bytes that the user reviewed are the bytes that coSlash uploads. Preview and upload use one canonical serializer. A retry reads only the approved spool, never the current source.
- A Hub share is a snapshot, not a live feed. A session that continues after a share stays stale in Hub until its author shares again.
- coSlash never downgrades a payload. If the Hub or the agent does not support the selected format, the share stops with a visible blocker. It does not fall back to a metadata-only upload.
- The collector enforces privacy, eligibility, and consent gates where it produces the data. The frontend does not enforce them.

## Context

v0.1 has no automated secret scanner. The first prompt, commands, tool output, and file bodies can contain credentials. Every member of the destination workspace can read every field of an approved upload. Thus the exact preview and the named audience are the primary privacy controls, not extras.

Consent must attach to a specific revision that the author saw. A live mirror sends content that the author never reviewed. Workspace membership can grow after an upload, so the preview states the member count as a current fact. The audience version lets the collector detect that the audience changed between review and upload (752d0940).

If someone "simplifies" this design, these failures come back:

- An auto-share rule sends sessions that nobody reviewed, including pasted secrets.
- Consent bound only to "a workspace" or to its size lets an upload reach people the user did not approve.
- A second serializer, or a re-read of the source at retry, uploads bytes that differ from the reviewed bytes.

## Alternatives rejected

- A separate private sync companion or second executable. coSlash uses one public binary with the exporter embedded in the collector.
- A local outbox or companion-process protocol.
- A live, continuously updated Hub mirror.
- A metadata-only fallback when complete backup is unsupported (`collector/internal/sessionbackupproducer/README.md`).

## Consequences

- Hub views go stale while sessions continue. The UI says so instead of implying a live mirror.
- Complete backup v1 supports Codex only. Other agents show `complete_backup_unsupported`.
- Complete backups are not redacted. [`docs/data-and-privacy.md`](../data-and-privacy.md) warns the user before approval.
- Contracts: [`collector/snapshot/v1/README.md`](../../collector/snapshot/v1/README.md), [`collector/sessionbackup/v1/README.md`](../../collector/sessionbackup/v1/README.md), [`collector/internal/sessionpreview/README.md`](../../collector/internal/sessionpreview/README.md), and [`collector/internal/sessionbackupproducer/README.md`](../../collector/internal/sessionbackupproducer/README.md). Do not copy their field lists here.
- Enforcement: `collector/internal/sessionexport`, `collector/internal/sessionpreview`, `collector/internal/sessionbackupproducer`, `collector/internal/hubclient`, and the Hub routes in `collector/cmd/coslash`.
- Open requests that conflict with this decision (public links, sanitized reports) are in [`docs/ideas.md`](../ideas.md).
