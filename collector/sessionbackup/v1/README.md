# `session-backup/v1`

`session-backup/v1` is the additive, storage-neutral contract for one complete
logical session-family backup. It does not add raw bytes to
`full-session-record/v1`, change that record's revision, or reinterpret the
existing snapshot and parsed-record upload contracts.

The backup unit is one visible card: its root session and every recursively
attributed child that contributes to the card. The root is member ordinal 0;
children follow in deterministic depth-first order with siblings sorted by
`memberId`. An associated child is not another shared revision. A child that
cannot be associated and appears as its own card is the root of a separate
backup.

## Producer and artifact inventory

Complete backup v1 supports local Codex and SSH Codex only. Claude, Cursor,
and OpenCode, whether local or SSH, must surface the blocking code
`complete_backup_unsupported`; they must not fall back to a metadata-only
success.

| Kind | Source bytes | Local Codex | SSH Codex | Completeness rule |
| --- | --- | --- | --- | --- |
| `raw-transcript` | Exact rollout JSONL for each family member under `.codex/sessions` or `.codex/archived_sessions` | Read every discovered family rollout from both trees | Read every discovered family rollout from both trees during freeze | Exactly one per discovered rollout; an absent, unreadable, malformed, or changing discovered file blocks completion |
| `raw-sidecar` | Exactly attributed `session_index.jsonl` row whose `id` is a family member | Project matching rows when the file exists | Project matching rows when the file exists | A missing index file may be omitted; a malformed or unreadable matching row blocks completion |
| `raw-metadata-rows` | Not a Codex v1 input | Prohibited | Prohibited | Codex v1 has no shared SQLite artifact; `session_index.jsonl` is its only shared metadata source |
| `parsed-session-record` | Canonical `full-session-record/v1` | Required for every represented member | Same | Exactly one per declared member; it must match source, agent, member identity, lineage, and revision |
| `exact-change-body` | Exact UTF-8 body keyed by the parsed record's change ID | Present for every parsed change | Same | Bytes must equal the body in the referenced parsed record |
| `session-enrichment` | Canonical [`enrichment.schema.json`](enrichment.schema.json) document containing only repository identity, repository-local-only state, filesystem fallback branch, Git drift, and last-edit time | Required per represented member | Required from the remote cached/frozen overlay | Capture all five fields, using JSON `null` for absent nullable values |
| `synthesis` | Canonical [`synthesis.schema.json`](synthesis.schema.json) persisted cache record: `agent`, `sessionId`, `mtime`, `model`, `generatedAt`, and `synthesis` | Present when persisted for the frozen revision | Present when persisted for the frozen revision | `mtime` must equal the member's `synthesisRevisionMs`; parsed synthesis, when present, must equal the cache record's synthesis object |

The fixture includes every Codex v1 input kind, root and child parsed records,
two exact change bodies, both enrichment branches, and a persisted synthesis
record. It deliberately contains no invented Codex database. No credentials,
absolute user paths, private transcripts, or customer identities are present.

For SSH, these inputs are reachable only through the existing allowlist:
`.codex/sessions`, `.codex/archived_sessions`, and
`.codex/session_index.jsonl`. A producer must not infer or open another remote
Codex path.

## Canonical manifest and identity

[`schema.json`](schema.json) is the language-neutral manifest shape and
[`database-rows.schema.json`](database-rows.schema.json) defines the generic
attributed shared-database projection reserved for a future named producer.
[`enrichment.schema.json`](enrichment.schema.json) and
[`synthesis.schema.json`](synthesis.schema.json) pin the two processed
documents. Canonical bytes are
UTF-8 JSON produced by the field order in the schema/Go structs, with no
insignificant whitespace, BOM, or trailing newline. Arrays use these orders:

- `requiredVersions`: ascending byte order;
- `members`: root first, then deterministic depth-first family order;
- `artifacts`: member ordinal, kind, then logical name;
- `artifactCounts`: kind in ascending byte order.

Every artifact has a unique traversal-free relative `logicalName`, member
provenance, `source`, `kind`, stable `sourceKey`, media type, `identity`
encoding, exact byte length, and lowercase SHA-256. Artifact contents may
retain exact local values, including paths; logical names may not contain an
absolute path, backslash, empty component, `.` component, or `..` component.

`completeBackupSha256` is the one family revision identity. Compute it as the
lowercase SHA-256 of the canonical manifest with that field set to the empty
string. Because the preimage contains ordered membership, per-member source
revisions, and every artifact length/hash, any membership or artifact change
invalidates approval. `source.sourceRevision` identifies the producer's frozen
source snapshot; `producer` records collector and parser provenance;
`repository` records stable repository identity without requiring an absolute
filesystem path.

Canonical object member order is normative:

- Manifest: `schemaVersion`, `canonicalVersion`, `requiredVersions`, `source`,
  `repository`, `producer`, `family`, `members`, `artifacts`, `summary`,
  `captureProblems`, `completeBackupSha256`.
- Source: `kind`, `sourceId`, `agent`, `sourceRevision`; repository:
  `canonical`, `vcs`; producer: `name`, `version`, `parserVersion`; family:
  `familyId`, `rootMemberId`.
- Member: `ordinal`, `memberId`, `parentMemberId`, `sourceRevision`,
  `synthesisRevisionMs`.
- Artifact: `ordinal`, `logicalName`, `memberId`, `source`, `kind`,
  `sourceKey`, `mediaType`, `encoding`, `byteLength`, `sha256`.
- Summary: `artifactCount`, `artifactCounts`, `totalBytes`; artifact count:
  `kind`, `count`; capture problem: `code`, `memberId`, `kind`, `retryable`.
- Enrichment: `repository`, `repositoryLocalOnly`,
  `filesystemFallbackBranch`, `git`, `lastEditAtMs`; Git drift: `baseBranch`,
  `ahead`, `behind`.
- Synthesis record: `agent`, `sessionId`, `mtime`, `model`, `generatedAt`,
  `synthesis`; synthesis: `goals`, `outcome`, `keyDecisions`, `nextStep`.
- Database projection: `schemaVersion`, `database`, `tables`; table: `name`,
  `columns`, `rows`; row: `memberId`, `values`; value: `type`, `value`.

Strings use JSON double quotes. Escape quotation mark and reverse solidus as
`\"` and `\\`; use `\b`, `\f`, `\n`, `\r`, and `\t` for those controls and
lowercase `\u00xx` for remaining U+0000 through U+001F controls. Encode other
valid Unicode directly as UTF-8 except `<`, `>`, `&`, U+2028, and U+2029,
which are `\u003c`, `\u003e`, `\u0026`, `\u2028`, and `\u2029`. Integers use
the shortest base-10 form; booleans and null use lowercase JSON literals.

`summary` gives bounded review data: total artifacts, counts by kind, and total
bytes. The top-level `completeBackupSha256` field carries the complete hash.
Preparation may carry a `CaptureProblem`, but a
completed canonical manifest requires `captureProblems` to be empty. An
unavailable, unreadable, unstable, un-attributable, or expected-but-missing
artifact is therefore a visible blocker, never an omission.

Stable blocker codes are `complete_backup_unsupported`,
`artifact_unavailable`, `artifact_unreadable`, `artifact_unstable`,
`artifact_unattributable`, and `artifact_invalid`. `memberId` and `kind` scope
the problem without copying raw content into UI/error telemetry; `retryable`
drives retry affordance. A producer may serialize this draft review state, but
must not assign a complete hash or present it as a verified manifest.

Required semantic versions are closed for v1. Consumers reject a version they
do not understand rather than guessing. `requiredVersions` is sorted and must
contain both `full-session-record/v1` and `session-backup/v1`; the optional
database projection version does not make that projection a Codex input. This
is exercised by the published unknown-version fixture.

The canonical manifest is limited to 64 MiB and 100,000 members and artifacts.
Each artifact is limited to 512 MiB, and all declared artifacts together are
limited to 4 GiB. Consumers must reject these bounds before allocating artifact
buffers and may stream artifacts that do not require semantic decoding.

## Shared metadata databases

Codex v1 has no shared SQLite input and no allowed database label, table, or
attribution column. Its only shared metadata source is
`.codex/session_index.jsonl`, represented as `raw-sidecar`. The generic format
below remains defined and separately tested so a future producer can name an
allowed database and attribution rule without copying whole database files;
it is not permission for HS-02 to open a Codex database locally or over SSH.

Shared databases are projected, not copied. `session-backup-db-rows/v1` has a
stable database label and tables sorted by name. Columns retain source schema
order. Each selected row has the manifest member ID used for attribution and
one typed value per column; rows are sorted by their canonical JSON bytes.

SQLite values serialize as follows:

| Storage class | `type` | `value` |
| --- | --- | --- |
| NULL | `null` | empty string |
| INTEGER | `integer` | shortest signed base-10 integer |
| REAL | `real` | 16 lowercase hexadecimal digits containing the big-endian IEEE-754 binary64 bits |
| TEXT | `text` | exact UTF-8 text |
| BLOB | `blob` | canonical padded RFC 4648 base64 |

The producer selects rows with the source's authoritative session key inside a
read transaction, assigns each row to exactly one family member, and freezes
the database revision with the other source inputs. A row attributed to a
member outside the artifact's member is invalid. Whole-database copies,
unrelated rows, ambiguous rows, and partial query results block completion.

## Fixtures and verifier

Generate byte-identical public fixtures:

```sh
go generate ./sessionbackup/v1
```

Verify a bundle without client-internal session types:

```sh
go run ./sessionbackup/v1/cmd/verify ./sessionbackup/v1/testdata/fixtures/valid/family
```

The verifier checks canonical shape and hash, exact length/hash of every blob,
parsed-record provenance and parent linkage, exact change-body equality,
canonical enrichment and persisted synthesis shape, synthesis revision and
content binding, undeclared files, and traversal names. Published invalid
fixtures cover a missing or duplicate artifact, wrong size, wrong hash, unsafe
name, and unknown required version. Tests additionally reject cross-session
generic database rows and delete and mutate every valid artifact one at a
time.

## Aggregate measurement and configurable limits

Exact completed bundles can be measured without printing content, paths,
source IDs, repository IDs, session IDs, or artifact names:

```sh
go run ./sessionbackup/v1/cmd/measure --collector-version <commit> <bundle-dir>...
```

Before the producer is available, the approved local Codex corpus lower bound
can be measured from both `.codex/sessions` and `.codex/archived_sessions`
with the same privacy property. The command emits the aggregate report but
exits nonzero if any discovered input is unreadable:

```sh
go run ./sessionbackup/v1/cmd/measure-codex-source --collector-version <commit>
```

Raw-source evidence is a lower bound because a complete bundle also carries
parsed records, exact changes, enrichment, and synthesis. Operational
per-backup, chunk, and workspace limits are discovery/configuration values, not
manifest constants. The checked-in contract therefore validates integrity and
ordering without silently truncating a bundle at an operational threshold.

On 2026-09-22, the approved local Codex corpus measurement at collector commit
`4a41ab2c870f644d08cfdcf330f18e42b0cb4d30` reported only these aggregates:
687 families, 1,026 raw artifacts, zero unreadable artifacts, 3,012,306,081
total raw bytes, family p50 1,519,483 bytes, p95 15,323,010 bytes, p99
39,774,130 bytes, maximum family 190,894,559 bytes, and maximum artifact
163,420,007 bytes. No content or identity was recorded.

Initial runtime recommendations are a 1 GiB per-backup limit, 4 MiB upload
chunks, and a 50 GiB workspace capacity. The per-backup limit is 5.6 times the
observed maximum raw-family lower bound, leaving room for parsed and processed
artifacts; a 4 MiB chunk keeps the largest observed raw artifact near 40 chunks
and a limit-sized backup at 256 chunks; the workspace recommendation is over
16 times the measured raw corpus so immutable revisions and processed
artifacts have headroom. HS-02 should rerun exact completed-bundle measurement
and tune these advertised values if its complete p99/maximum evidence requires
it. Exceeding any configured value rejects the whole backup before completion.
