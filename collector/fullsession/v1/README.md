# `full-session-record/v1`

`full-session-record/v1` is the storage-neutral contract for one immutable,
complete parsed session revision. It is additive to `session-snapshot/v1` and
does not change v1 sharing bytes or behavior.

The record includes the supported parsed session envelope, detail, subagents,
and every ordered file-change body. It excludes raw vendor transcript rows,
SSH configuration, credentials owned by coSlash, sockets, and transport
commands. File-change IDs are opaque identifiers, never filesystem paths.

`sourceId`, `agent`, `sessionId`, and the content-derived `revisionId` form the
exact read identity. `revisionId` is SHA-256 over canonical JSON with an empty
revision field. Each change body independently declares its UTF-8 byte count
and SHA-256.

The first C01 producer is Codex. Adding another parser requires an explicit
field-parity decision; schema support alone does not claim producer coverage.

## Included-field inventory

| Surface | Producer | Transport/cache representation | Observable assertion |
| --- | --- | --- | --- |
| Source/session identity and revision | Remote manager + canonical record freezer | Changed-family full record; cached full-record row | Exact source, agent, session, and revision are required for reads |
| Session envelope, timing, repository, usage, and cost | Codex parser plus existing metadata enrichment | Typed `session` fields | Local/SSH record equality |
| Prompts, summary, goals, commands, commits, todos, digest, synthesis | Codex parser/composer | Typed `session` detail fields | Restart and unchanged-refresh equality |
| Subagent identity, task/result, commands, usage, and cost | Codex family composition | Typed `subagents` list | Ordered value equality |
| File-edit summaries | Codex file-edit accumulator | Typed `fileEdits` rows | Ordered value equality |
| File-change kind, operation, counts, and exact text | Codex file-edit accumulator | Change metadata in the record row; text in cache `changeBodies` | Per-body byte count/hash and exact read assertion |
| Raw transcript rows and SSH/coSlash configuration | Excluded | No representation | Reflection/fixture review and transport allow-list |

Published valid and invalid consumer fixtures live under
[`testdata/fixtures`](testdata/fixtures). Run `go generate ./fullsession/v1`
to regenerate their bytes and manifest hashes.

Measure fixtures or an approved private corpus without printing content or
identity fields:

```sh
go run ./fullsession/v1/cmd/measure --collector-version <commit> <record.json>...
```
