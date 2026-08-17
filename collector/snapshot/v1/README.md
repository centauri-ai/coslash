# `session-snapshot/v1`

This directory is the public, deployment-independent contract for session data
that may leave a developer machine. Private collector models are not wire
types; publishers must explicitly map into this contract.

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
model lists, and integer micro-USD costs. Publishers must use the exact byte
slice returned by `snapshotv1.Marshal`.

## Shape and budgets

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

## Validation and compatibility

`Decode` rejects unknown fields, non-canonical JSON, bad hashes, negative
counts, null collection fields, over-budget fields, unsafe paths, and oversized
payloads. The JSON Schema mirrors the portable shape; UTF-8 byte limits and
canonical hashing remain protocol checks because JSON Schema `maxLength`
counts Unicode code points rather than encoded bytes.

Adding or changing a wire field requires coordinated updates to the Go types,
schema, documentation, and compatibility tests. Agent identifiers are
intentionally extensible within v1. Unknown object fields remain rejected.
