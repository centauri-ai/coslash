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
null arrays, except optional `unattributedTokens`, `commandOutputs`, `skills`, `pullRequestLinks`, `detailsIncomplete`, digest annotations, and subagent command `output`, which are omitted when absent. A false `detailsIncomplete` is omitted.
Usage arrays declare models in ascending model-name order; all other arrays
retain their declared order. Integers use the shortest base-10
form (no leading zero or plus sign); booleans and null use their lowercase JSON
literals.

Object members occur in the following order:

- Record: `schemaVersion`, `sourceId`, `agent`, `sessionId`, `parentSessionId`, `revisionId`, `session`.
- Session: `name`, `summary`, `status`, `cwd`, `branch`, `editedFileCount`, `durationMs`, `usage`, optional `unattributedTokens`, `costMicroUsd`, `unpricedModels`, `subagents`, `startedAtMs`, `lastActivityAtMs`, `entrypoint`, `model`, `contextTokens`, `contextWindow`, `turns`, `toolUses`, `errors`, `compactions`, `firstPrompt`, `commands`, optional `commandOutputs`, optional `skills`, `commits`, `commitShas`, `pullRequests`, optional `pullRequestLinks`, `todos`, `digest`, `fileEdits`, `synthesis`, `synthesisPending`, `declaredGoal`, optional `detailsIncomplete`.
- Model usage: `model`, `inputTokens`, `outputTokens`, `cacheCreationInputTokens`, `cacheCreation1hInputTokens`, `cacheReadInputTokens`, `costMicroUsd`.
- Unattributed usage: `inputTokens`, `outputTokens`, `cacheCreationInputTokens`, `cacheCreation1hInputTokens`, `cacheReadInputTokens`, `costMicroUsd`.
- Subagent: `id`, `name`, `model`, `status`, `task`, `result`, `durationMs`, `spawnedAtTurn`, `toolUses`, `commands`, `usage`, optional `unattributedTokens`, `costMicroUsd`; a subagent command uses `label`, `command`, optional `output`.
- Pull request link: `url`, `repository`, `number`.
- Todo: `text`, `done`; digest entry: `turn`, `category`, `description`, `answer`, `subagentId`, `timeMs`, optional `sourceEntryId`, `parentEntryId`, `branchId`, `inherited`, `active`, `contextSelected`, `contextDescription`.
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

`pullRequestLinks` lists the pull requests that an agent created in the
session, at most 100. Each `url` is `https://github.com/<owner>/<repo>/pull/<n>`,
and `repository` (`owner/repo`) and `number` repeat its parts. URLs are unique
and items are sorted by `repository`, then `number`. When the list is not empty,
`pullRequests` equals its length. Records without links stay valid.

Session timestamps must be positive Unix milliseconds no later than
`9999-12-31T23:59:59.999Z`. This keeps the shared record within the supported
PostgreSQL persistence range before a consumer performs timestamp conversion.
Session and subagent costs are nullable when neither the source nor token usage
provides an estimate; a present zero remains distinct from an unknown cost.
Costs are integer micro-USD values from 0 through 4,294,967,295,999,999. The
upper bound guarantees exact conversion through the reference implementation's
private floating-point dollar model.

## Normative validity rules

A consumer must reject a record unless all of these rules hold:

- The input is at most 64 MiB, is one canonical JSON object as specified above,
  has every required field, has no unknown fields or trailing JSON value, and
  uses `schemaVersion` exactly `full-session-record/v1`.
- `sourceId` and `sessionId` are identifiers. `parentSessionId` is either empty
  or a different identifier from `sessionId`. An identifier is 1 through 512
  UTF-8 bytes, has no leading or trailing whitespace, contains no slash or
  reverse solidus, and contains no Unicode code point U+0000 through U+0020 or
  U+007F. `agent` is exactly `codex`, `claude`, `cursor`, `opencode`, `grok`, or `pi`.
- `revisionId` is the lowercase hexadecimal SHA-256 described above. Every
  file-change `sha256` is likewise the lowercase SHA-256 of `text`, and every
  change ID is an identifier unique across the record.
- Every string is valid UTF-8 and at most 1 MiB. A file-change `text` is valid
  UTF-8 and at most 32 MiB. File-edit paths, usage model names, and change
  operations are nonempty.
- At most 100,000 array items occur in aggregate across session usage,
  unpriced models, subagents, commands, commits, commit SHAs, todos, digest,
  file edits, subagent commands and usage, file changes, and synthesis goals
  and key decisions. No individual array or JSON collection nesting may exceed
  that limit. Null and empty arrays are both valid and remain distinct.
- `startedAtMs` is positive and no later than the timestamp limit above;
  `lastActivityAtMs` is between it and that limit, inclusive. All token,
  duration, context, turn, tool-use, error, compaction, pull-request, digest
  turn/time, edit addition/deletion, and subagent spawn/tool-use counts are
  nonnegative signed 64-bit integers. `editedFileCount` equals the number of
  `fileEdits`.
- Every non-null session, subagent, unattributed, and model cost is within the
  micro-USD range above. Session and subagent costs may be null; model costs
  are required and may be null only for Pi. Session and subagent usage arrays
  are strictly ascending by nonempty model name.
- `commandOutputs` is absent when the producer did not collect outputs. When
  present, it has one item per `commands` item in the same order, and `commands`
  is not empty. An item is null for a command without a result and an empty
  string for a real empty output. Each non-null item and each subagent command
  `output` is valid UTF-8, obeys the 1 MiB string limit, and counts toward the
  item budget like a command string. The Hub never truncates outputs. Claude
  producers keep the first 8 KiB and the last 8 KiB of an output longer than
  16 KiB, joined by `\n… [N bytes truncated] …\n`, where N is the number of
  bytes removed.
- `unattributedTokens` is allowed for Pi sessions and for Grok sessions and
  their subagents.
  Its five counters obey the same nonnegative integer bounds as model usage.
  For Grok, the same session or subagent has no attributed usage entries,
  and its inner and enclosing `costMicroUsd` must have equal presence and value.
  Two null costs mean unknown; two present zeros mean known zero. Costs obey
  the shared micro-USD bounds. The enclosing nullable cost is authoritative;
  a private token struct's default float zero is not evidence of known cost.
- Every file edit has a nonempty path, positive `edits`, and no negative count.
  Every file change has kind `diff` or `content`, a nonempty operation, exact
  UTF-8 `byteCount`, an exact body hash, and no negative count.
- Every subagent has an identifier ID, nonnegative optional duration and spawn
  turn, nonnegative tool uses and valid commands/usage. Todo, digest, subagent,
  and synthesis strings obey the shared string limit.

The first C01 producers are Codex and Claude. Adding another parser requires an
explicit field-parity decision; schema support alone does not claim producer
coverage. Grok local portable composition preserves model-attributed usage,
model-unattributed session totals, and nested child accounting. Grok local
complete-backup capture preserves each member's source artifacts; it does not
add Grok to remote collection or a sync queue. Grok uses either model rows or
unattributed counters for each member; child accounting is repeated in its
direct parent's subagent projection and its own complete record, without being
added to the parent's counters. Sum complete member records once, not their
duplicate subagent projections.

Grok's source parser preserves present zero counters as known usage, including
totals without model rows. Absent or null counters alone leave usage unknown.
Missing, partial, or incomplete source cost stays null; no model
or price is inferred for unattributed totals. Portable inner and enclosing costs
must agree so the existing private nullable session/subagent cost can preserve
every valid record through both adapter directions. The adapter rejects an
independently populated nonzero private unattributed float cost rather than
discarding it. Unavailable accounting, incomplete detail, and branch-context
facts remain rejected for agents other than Pi.

## Pi accounting and context snapshots

Pi records represent independent sessions: `parentSessionId` is empty and
`subagents` is null or empty. A header's `parentSession` is private fork evidence,
not a subagent relationship. Verified inherited work is excluded from the fork's
accounting before composition.

For Pi, `usage:null` with absent `unattributedTokens` means unavailable token
accounting. `usage:[]` means known zero attributed usage. Present unattributed
counters are additive to model counters. Model and unattributed `costMicroUsd`
values are independently nullable; a known aggregate cost does not imply a
known component cost. Null means unknown and a present zero means known zero.
Never infer model attribution or component prices from an aggregate. The private
adapter uses `ModelTokens.CostKnown` to distinguish an explicit component cost
from its default float zero. Pi composition preserves source aggregate cost
without running local price estimates.

Digest annotations are Pi-only. Each nonempty entry or branch ID obeys the
identifier rules above. Annotation strings obey the shared UTF-8 and string
bounds. `inherited`, `active`, and `contextSelected` are optional booleans;
present false differs from absence. `contextDescription` is an optional string;
a present empty string differs from absence. History remains in chronological
source order. Active ancestry and context retained after compaction or edits
are different facts and must not be flattened into one selection flag.

Consumers that need the selected conversation, including synthesis and
last-turn checks, must exclude rows with `contextSelected:false`. Include rows
with true or absent `contextSelected`, preserving their declared order; absence
retains legacy behavior. For included rows, use `contextDescription` instead of
`description` when present, including a present empty string. Do not overwrite
the stored chronological `description`. Neither `active` nor `inherited` is a
context filter. An absent `active` or `inherited` means unknown, not false.
The session's model, context occupancy, prompt and summary already describe the
selected context; raw chronological history must not override them.

`detailsIncomplete:true` is Pi-only and reports incomplete source detail in an
otherwise complete parsed record. It is omitted when false. It does not claim
that interrupted family capture is safe to publish.

The producer freezes context selections for each record. Selected-leaf changes,
including verified changes without transcript writes, change the revision when
they change parsed context facts. Process identity, absolute evidence paths,
heartbeat timestamps and live status are local overlays and do not enter the
revision. Pi is admitted only to local portable composition, not SSH collection.
Legacy snapshot export remains unchanged. Capture, queueing and Hub acceptance
require their own producer/consumer support before uploads can be enabled.

## Included-field inventory

| Surface | Producer | Transport/cache representation | Observable assertion |
| --- | --- | --- | --- |
| Source/session identity and revision | Remote manager + canonical record freezer | Changed-family full record; cached full-record row | Exact source, agent, session, and revision are required for reads |
| Session envelope, timing, transcript-supplied working directory/branch, usage, and cost | Claude and Codex parsers plus allowlisted vendor metadata | Typed `session` fields | Canonical local/helper/SFTP byte equality |
| Prompts, summary, goals, commands, commits, todos, digest | Claude and Codex parsers/composers | Typed `session` detail fields | Restart and unchanged-refresh equality |
| Command outputs (session and subagent) | Claude and Codex parsers (tool call IDs); Cursor IDE/CLI result stores (command text and order); Grok and OpenCode parsers (same tool record) | Optional `commandOutputs` and subagent command `output`; dropped from a record that exceeds a bound; excluded from the local list API, remote facts, and snapshot export | Adapter round-trip and revision equality |
| Grok model-unattributed counters and nullable cost | Grok parser and local portable composition | Optional `unattributedTokens` on session and direct subagent rows | Source-fixture totals and nested-family adapter byte round trips |
| Pi accounting, completeness and branch/context annotations | Pi parser and local portable composition | Nullable usage/costs, optional digest annotations and `detailsIncomplete` | Canonical fixtures, context revision changes and lossless adapter round trips |
| Subagent identity, task/result, commands, usage, and cost | Claude and Codex family composition | Typed `subagents` list | Ordered value equality |
| File-edit summaries | Claude and Codex file-edit accumulators | Typed `fileEdits` rows | Ordered value equality |
| File-change kind, operation, counts, and exact text | Claude and Codex file-edit accumulators | Change metadata in the record row; text in cache `changeBodies` | Per-body byte count/hash and exact read assertion |
| Repository identity/local-only flag, filesystem fallback branch, Git drift, last-edit time, and transient local review state | Excluded local/runtime enrichment | No v1 field | Schema inventory and three-path equality test |
| Raw transcript rows and SSH/coSlash configuration | Excluded | No representation | Reflection/fixture review and transport allow-list |

Local UI composition may add repository identity, a fallback branch, Git
ahead/behind state, or last-edit time after parsing. Those machine-dependent
values are deliberately outside this storage-neutral revision; a branch is
included only when the transcript or portable vendor metadata supplied it.
Remote collection does not run AI synthesis, so its v1 field remains null.

Published valid and invalid consumer fixtures, including identifier, cost,
timestamp, aggregate-item, and negative-count boundaries, live under
[`testdata/fixtures`](testdata/fixtures). Run `go generate ./fullsession/v1`
to regenerate their bytes and manifest hashes.

Measure fixtures or an approved private corpus without printing content or
identity fields:

```sh
go run ./fullsession/v1/cmd/measure --collector-version <commit> <record.json>...
```

## Captured skills

Optional `skills` contains `{name, content}` entries in observed order. Identical
name and content pairs appear once; different content under the same name stays
as separate entries. Content is the instruction text captured in the session,
never a later read of the current skill file. An empty content string means the
instructions were unavailable. Each name is nonempty and has no surrounding
whitespace. Each content string obeys the command-output UTF-8, NUL and 1 MiB
limits. Oversized content includes a truncation marker and its captured-content
SHA-256, so different versions remain distinct after bounding. Entries count toward the aggregate item budget.

The collector keeps skills from child sessions in ancestor session collections.
Absent skills remain omitted, so existing record bytes and revisions do not
change. Hub exposes captured skills only in the owner's own space. Deploy Hub's
contract support before a collector starts uploading this optional field.
