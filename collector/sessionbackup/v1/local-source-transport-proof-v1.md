# Local source and transport proof matrix

- **Identity:** `local-source-transport-proof/v1` at Local base
  `c102350809cea644e6bfd2bb1f8899df84d0d73e`.
- **Scope:** synthetic fixtures and source inspection for t4-15. No live SSH
  host, Hub v4 upload, or vendor private corpus is claimed here.
- **Consumers:** t4-16 through t4-20, the v4 upload owner t4-13, IC-1, and
  G-local-network.

The authoritative complete bundle remains [session-backup/v1](README.md).
The table distinguishes a parser or an SSH facts cache from a complete backup.
`P` means synthetic producer plus independent bundle verifier passed; `U3`
means the synthetic v3 HTTP upload, receipt, finalize and resume checks passed.
Neither mark establishes a v4 upload. `B` names work that must be built and
proved before complete sync can be enabled.

| Source/path | Producer and raw/parsed coverage | Upload, restart, resume | Disposition |
| --- | --- | --- | --- |
| Codex local | `P`: both rollout trees, attributed index rows, root/child parsed records, exact changes, enrichment and revision-matched synthesis. | `U3`; spool reopen after manager restart. v4: B. | t4-16 builds v4 mapping and connected round trip. |
| Codex SSH | `P` using a synthetic `ReadSource` and remote enrichment; exact rollouts are frozen before parsing. Remote allowlist has both rollout trees and index. | Spool reopen passed. The v3 uploader accepts the common bundle type, but SSH-to-HTTP upload, live SSH interruption and v4 remain B. | t4-16 builds v4; t4-20 proves live relay and recovery, including any revision-matched remote synthesis input. |
| Claude Code local | Transcript/child parser exists under `.claude/projects`, but no complete family raw/parsed exporter. | `complete_backup_unsupported`; no upload or spool. | B: t4-17 must inventory every family input and prove round trip. |
| Claude Code SSH | Remote facts helper reads `.claude/projects`, `.claude/sessions`, `.claude/jobs`; it has no complete backup export for this agent. | `complete_backup_unsupported`; no upload or spool. | B: t4-17 exporter, t4-20 SSH allowlist/freeze proof. |
| OpenCode local | SQLite session/message/part parsing exists; there is no attributed raw database-row projection or complete exporter. | `complete_backup_unsupported`; no upload or spool. | B: t4-18 must snapshot and attribute all required rows, retaining SQLite null, integer, real, blob and order semantics. |
| OpenCode SSH | The helper advertises only Claude and Codex facts and has no OpenCode database allowlist/export. | `complete_backup_unsupported`; no upload or spool. | B: t4-18 source exporter, t4-20 bounded SSH relay. |
| Cursor IDE local | Transcript parser and IDE `state.vscdb`/metadata readers exist; no complete, attributed raw export. | `complete_backup_unsupported`; no upload or spool. | B: t4-19 must bind IDE transcript and metadata rows to each member. |
| Cursor IDE SSH | Helper has no Cursor capability or allowlist. | `complete_backup_unsupported`; no upload or spool. | B: t4-19 exporter, t4-20 SSH relay. |
| Cursor CLI local | Transcript parser and CLI chat-store metadata readers exist; no complete raw export. CLI and IDE must retain their entrypoint identity. | `complete_backup_unsupported`; no upload or spool. | B: t4-19 must prove CLI store attribution independently of IDE. |
| Cursor CLI SSH | Helper has no Cursor capability or allowlist. | `complete_backup_unsupported`; no upload or spool. | B: t4-19 exporter, t4-20 SSH relay. |

## Evidence and limits

`internal/sessionbackupproducer/producer_test.go` verifies a sanitized Codex
root and child, archived rollout, repeated exact index rows, content hashes,
bounded reads and restart reopening for local and SSH source kinds. The SSH
test substitutes a synthetic local `ReadSource`; it does not connect to SSH.
`TestUnsupportedAgentsNeverOpenSourceOrPublishBundle` covers Claude,
OpenCode and Cursor under both source kinds. Cursor IDE and CLI are separate
product lanes, but the current `Selection` carries only `agent=cursor`, so
both hit the same blocker. `TestInterruptedRefreshRetainsVerifiedLastGoodBundle`
and the existing mutation/cancellation tests cover a failed replacement.

`internal/hubclient/backup_test.go` proves synthetic v3 create, reviewed chunk
bytes, receipt matching, rate-limit retry, finalize, idempotent create after
restart, and canonical accepted route. `internal/remoteprotocol/protocol_test.go`
proves that an interrupted remote facts stream cannot commit a deletion.
These are separate boundaries: a cached parsed fact does not establish a raw
artifact or a completed upload. The receipt and retry repair from Tier3
`fffdd168982dce6993efc8978177c00f42cdd332` is already present on the
accepted base; its commit is not an ancestor. The current base additionally
bounds `Retry-After` by the operation budget.

## v4 transport compatibility contract

**Identity:** `local-transport-v4/v1`. This is the Local input contract for
t4-16 and t4-13. The accepted server `v4-core/2` OpenAPI at integration SHA
`ca115918c2afd7a409c75ec2fc5c0a764c9e1e90` and selected
`storage-v4/v1` establish the following compatibility boundary. The final
upload request, signed-chunk receipt and finalize wire details remain owned by
t4-13; this document does not assign meanings to their unspecified fields.

1. A Local source becomes eligible only after the producer has a canonical,
   verified `session-backup/v1` family with no capture problems. Bind the
   source kind/ID, agent, root and child IDs, ordered membership, parent
   lineage, source revisions, parsed records, exact bodies, enrichment and
   every required raw artifact to that frozen family. A local parsed record,
   remote facts generation, or metadata create alone is incomplete. Preserve
   null versus zero/empty, integer and real precision, source row order, and
   nested lineage through the round trip.
2. The source bundle's `completeBackupSha256` is the review/freeze identity.
   Retain the exact manifest and bytes in the private spool across process
   restart. A changed source or consent requires a new review. Missing,
   unreadable, unstable or un-attributable input blocks publication and leaves
   the prior verified bundle intact.
3. `storage-v4/v1` requires ordered artifact offsets and lengths, application
   SHA-256 for each chunk and complete artifact, and immutable chunks scoped
   to the authenticated space. A provider ETag, generation, or legacy v3 chunk
   receipt is not proof of v4 acceptance. Retry must reconcile by the same
   authorized upload/session, content hash, manifest and missing-chunk plan;
   a queued request or `finalizing` state is not success.
4. The currently published `/v4/uploads` operations are `POST` create,
   `GET` status, `DELETE` abort, `POST /chunks:sign`, `PUT /chunks/{artifact}/{chunk}`
   proxy, and `POST /finalize`. Create accepts `idempotencyKey`, session
   metadata and `manifest.contentSha256`/`producerVersion`. Status identifies
   `uploadId`, `sessionId` and state (`open`, `finalizing`, `completed`,
   `failed`, `aborted`, `expired`); `missing` is presently an unconstrained
   object array. Finalize returns `202`. These fields do not yet specify how a
   full `session-backup/v1` manifest, artifact IDs, chunk claims, signed PUT
   verification, `revisionId` and accepted detail are bound. T4-13 must
   publish those shapes and error/retry rules before t4-16 can activate v4.
5. Local must require a completed server status with the expected immutable
   revision and independently readable artifact/detail proof before reporting
   success. `upload_incomplete`, `hash_mismatch`, `space_full`, `upload_expired`,
   `auth_revoked`, `session_deleted`, `rate_limited` and unknown stable problem
   codes must remain distinguishable. RFC 9457 `code` and resource authority
   come from the server contract. An interrupted upload retains last-good
   reads; retry may reuse verified same-space chunks only after reconciliation.
6. `/v1`–`/v3` and old Local continue their existing behavior. A v3 shared
   workspace backup or its receipt cannot become a v4 personal session or
   fixed team copy by relabeling. Keep v4 mutation disabled until producer,
   server route, authorization, receipt and readable result are connected.

T4-16 must compare the final t4-13 wire contract against these invariants and
record any changed contract version. IC-1 owns a connected create → Codex
upload/resume → accepted revision → owner list/detail path plus denial/retry.
G-local-network owns live local and SSH interruption and restart coverage for
all supported sources on a frozen candidate. Rollback disables v4 writes while
keeping the existing routes and last-good reads available.
