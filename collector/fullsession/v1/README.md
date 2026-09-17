# `full-session-record/v1`

`full-session-record/v1` is the storage-neutral contract for one immutable,
complete parsed session revision. It is additive to `session-snapshot/v1` and
does not change v1 sharing bytes or behavior.

The record includes the portable parsed session envelope, detail, subagents,
and every ordered file-change body. Portable means values obtained from the
transcript or allowlisted vendor metadata before local filesystem probes run.
It excludes raw vendor transcript rows, SSH configuration, credentials owned
by coSlash, sockets, and transport commands. File-change IDs are opaque
identifiers, never filesystem paths.

`sourceId`, `agent`, `sessionId`, and the content-derived `revisionId` form the
exact read identity. `revisionId` is SHA-256 over canonical JSON with an empty
revision field. Each change body independently declares its UTF-8 byte count
and SHA-256.

Every family member has its own complete record. `parentSessionId` is empty for
a root and identifies the direct parent for a child, preserving descendants
without reducing child sessions to the display-oriented `subagents` list.

## Canonical JSON

Canonical records use UTF-8 without a BOM, insignificant whitespace, or a
trailing newline. Every field is present, including null pointers and empty or
null arrays. Usage arrays declare models in ascending model-name order; all
other arrays retain their declared order. Integers use the shortest base-10
form (no leading zero or plus sign); booleans and null use their lowercase JSON
literals.

Object members occur in the following order:

- Record: `schemaVersion`, `sourceId`, `agent`, `sessionId`, `parentSessionId`, `revisionId`, `session`.
- Session: `name`, `summary`, `status`, `cwd`, `branch`, `editedFileCount`, `durationMs`, `usage`, `costMicroUsd`, `unpricedModels`, `subagents`, `startedAtMs`, `lastActivityAtMs`, `entrypoint`, `model`, `contextTokens`, `contextWindow`, `turns`, `toolUses`, `errors`, `compactions`, `firstPrompt`, `commands`, `commits`, `commitShas`, `pullRequests`, `todos`, `digest`, `fileEdits`, `synthesis`, `synthesisPending`, `declaredGoal`.
- Model usage: `model`, `inputTokens`, `outputTokens`, `cacheCreationInputTokens`, `cacheCreation1hInputTokens`, `cacheReadInputTokens`, `costMicroUsd`.
- Subagent: `id`, `name`, `model`, `status`, `task`, `result`, `durationMs`, `spawnedAtTurn`, `toolUses`, `commands`, `usage`, `costMicroUsd`; a subagent command uses `label`, `command`.
- Todo: `text`, `done`; digest entry: `turn`, `category`, `description`, `answer`, `subagentId`, `timeMs`.
- File edit: `path`, `additions`, `deletions`, `edits`, `isNew`, `changes`; file change: `id`, `kind`, `text`, `operation`, `additions`, `deletions`, `byteCount`, `sha256`.
- Synthesis: `goals`, `outcome`, `keyDecisions`, `nextStep`.

Strings use JSON double quotes. Escape quotation mark and reverse solidus as
`\"` and `\\`; use `\b`, `\f`, `\n`, `\r`, and `\t` for those controls and
lowercase `\u00xx` for the remaining U+0000 through U+001F controls. Encode
other valid Unicode directly as UTF-8 except `<`, `>`, `&`, U+2028, and U+2029,
which are respectively `\u003c`, `\u003e`, `\u0026`, `\u2028`, and `\u2029`.
Solidus is not escaped. The published `valid/escaping.json` fixture exercises
these rules. Hashes are lowercase hexadecimal SHA-256 digests of exactly these
bytes; compute the revision with `revisionId` set to the empty string.

Session timestamps must be positive Unix milliseconds no later than
`9999-12-31T23:59:59.999Z`. This keeps the shared record within the supported
PostgreSQL persistence range before a consumer performs timestamp conversion.
Session and subagent costs are nullable when neither the source nor token usage
provides an estimate; a present zero remains distinct from an unknown cost.

The first C01 producer is Codex. Adding another parser requires an explicit
field-parity decision; schema support alone does not claim producer coverage.

## Included-field inventory

| Surface | Producer | Transport/cache representation | Observable assertion |
| --- | --- | --- | --- |
| Source/session identity and revision | Remote manager + canonical record freezer | Changed-family full record; cached full-record row | Exact source, agent, session, and revision are required for reads |
| Session envelope, timing, transcript-supplied working directory/branch, usage, and cost | Codex parser plus allowlisted vendor metadata | Typed `session` fields | Canonical local/helper/SFTP byte equality |
| Prompts, summary, goals, commands, commits, todos, digest, synthesis | Codex parser/composer | Typed `session` detail fields | Restart and unchanged-refresh equality |
| Subagent identity, task/result, commands, usage, and cost | Codex family composition | Typed `subagents` list | Ordered value equality |
| File-edit summaries | Codex file-edit accumulator | Typed `fileEdits` rows | Ordered value equality |
| File-change kind, operation, counts, and exact text | Codex file-edit accumulator | Change metadata in the record row; text in cache `changeBodies` | Per-body byte count/hash and exact read assertion |
| Repository identity/local-only flag, filesystem fallback branch, Git drift, and last-edit time | Excluded local filesystem enrichment | No v1 field | Schema inventory and three-path equality test |
| Raw transcript rows and SSH/coSlash configuration | Excluded | No representation | Reflection/fixture review and transport allow-list |

Local UI composition may add repository identity, a fallback branch, Git
ahead/behind state, or last-edit time after parsing. Those machine-dependent
values are deliberately outside this storage-neutral revision; a branch is
included only when the transcript or portable vendor metadata supplied it.

Published valid and invalid consumer fixtures live under
[`testdata/fixtures`](testdata/fixtures). Run `go generate ./fullsession/v1`
to regenerate their bytes and manifest hashes.

Measure fixtures or an approved private corpus without printing content or
identity fields:

```sh
go run ./fullsession/v1/cmd/measure --collector-version <commit> <record.json>...
```
