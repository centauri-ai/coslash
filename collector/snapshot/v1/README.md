# `session-snapshot/v1`

This directory is the public, deployment-independent contract for session data
that may leave a developer machine. The local `session.Session` type is not a
wire type. Publishers map it through `internal/sessionexport`, which is an
explicit allow-list, before preview, local export, or upload.

## Protocol

- Schema version: `session-snapshot/v1`
- Media type: `application/vnd.coslash.session-snapshot.v1+json`
- Aggregate limit: 256 KiB of uncompressed canonical JSON
- Breaking changes: publish a new version; never mutate the meaning of v1

The envelope includes collector version, agent, source session ID, canonical
session start in Unix milliseconds, repository identity, structural redaction
and truncation records, and a SHA-256 content hash.

`contentHash` is `sha256:` plus the lowercase digest of the canonical JSON with
the `contentHash` member omitted. Canonical JSON uses the member order of the
Go wire structs, UTF-8 `encoding/json` string escaping, no insignificant
whitespace, no trailing newline, source order for meaningful lists, sorted
model lists, and integer micro-USD costs. Preview and upload must use the exact
byte slice returned by `sessionexport.Marshal`.

## Allow-list and budgets

| Shape | Limit |
| --- | ---: |
| Collector version | 64 UTF-8 bytes |
| Agent identifier | 64 lowercase ASCII characters; starts alphanumeric, then letters, digits, `_`, or `-` |
| Source IDs | 512 UTF-8 bytes |
| Canonical repository | 1,024 UTF-8 bytes |
| Name | 512 UTF-8 bytes |
| Summary and digest description/answer | 4 KiB each |
| Repository-relative paths | 1 KiB each |
| Branch and entrypoint | 512 UTF-8 bytes each |
| Model names | 256 UTF-8 bytes each; 100 models |
| Declared goal and first prompt | 16 KiB each |
| Digest | 200 entries |
| Todos | 200 entries; 2 KiB text each |
| File edits | 2,000 entries |
| Commit subjects | 200 entries; 2 KiB each |
| Subagents | 100 entries; 8 KiB task/result each |
| Human subagent command labels | 200 total; 512 bytes each |

Text is normalized to LF and truncated only at a valid UTF-8 boundary. Metadata
paths are RFC 6901 JSON Pointers that resolve against the exported payload.
Changes to retained values use their exact path; omitted source values use the
nearest retained container.

When the individually bounded fields still exceed the aggregate limit,
`sessionexport.Marshal` deterministically reduces optional evidence in this
order:

1. subagent command labels;
2. subagent result and task prose;
3. digest answers;
4. older digest entries, retaining the newest entries;
5. excess todos, commit subjects, and file-edit details.

Every aggregate reduction uses the `aggregate_budget` truncation reason and
records original/exported bytes or item counts. Repository identity, session
identity and time, status/counts, usage/cost, and redaction facts are never
removed. Export fails only when that mandatory core cannot fit. Preview and
upload consume the same fitted byte slice, so no smaller payload is created
silently after review.

The allow-list includes bounded session metadata, aggregate counts, raw token
counts, frozen estimated costs, digest/planning evidence, todos, file-change
statistics, commit subjects, git drift, and bounded subagent facts.

## Exclusions and structural redaction

The mapper never exports raw top-level commands, raw subagent commands, local
synthesis, synthesis state, compaction seed, parser state, tool output, file
diffs, raw transcripts, assistant reasoning, log paths, credentials, or
environment variables. Human command labels are exported only when distinct
from the raw command fallback.

Working directories and edit paths are made repository-relative. Values that
cannot be proven to be inside the repository are omitted and recorded in
`redactions`; absolute local paths never cross the boundary. Repository remote
credentials are removed earlier by canonical repository identity resolution.
As defense in depth, every allow-listed text field also redacts common bearer,
token, key, secret assignment, credentialed-URL, and private-key patterns. Only
the affected JSON path and `credential_pattern` reason enter metadata.

## Validation and compatibility

`Decode` rejects unknown fields, non-canonical JSON, bad hashes, negative
counts, null collection fields, over-budget fields, unsafe paths, and oversized
payloads. The JSON Schema mirrors the portable shape; UTF-8 byte limits and
canonical hashing remain protocol checks because JSON Schema `maxLength`
counts Unicode code points rather than encoded bytes.

Adding a field to the private local model changes no bytes until the explicit
mapper, schema, documentation, fixtures, and compatibility tests are updated.
Agent identifiers are intentionally extensible within v1; adding a producer
does not require a new schema version. Unknown object fields remain rejected.

The generated `boundary-metadata`, `boundary-work`, `boundary-subagent`, and
`boundary-items` fixtures pin exact valid text and collection limits across
public and server conformance. `boundary_test.go` separately proves exact-limit
acceptance and limit-plus-one rejection for bounded text and collections.

## Payload-cap acceptance

The 256 KiB limit was accepted on 2026-08-17 from a content-free measurement of
506 approved local sessions. Pre-fit p50/p95/p99 were 6,266/29,519/82,361
bytes; the maximum was 210,488 bytes, with zero degradation and zero rejection.
Rerun `internal/sessionexport/cmd/measure` after material parser, allow-list, or
budget changes. Revisit the cap if degradation exceeds 1% or any mandatory-core
rejection occurs. Never retain private session content in the report.
