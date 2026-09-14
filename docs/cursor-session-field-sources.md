# Cursor session field sources

This document maps Cursor data to the coSlash session model in
`collector/internal/session/session.go`. It records the preferred source, the
fallback source, and the limits for each field.

The Cursor formats in this document are observed local formats. They are not a
public Cursor API. Cursor can change or remove these formats in a later release.

See `docs/cursor-agent-transcripts-session-model.md` for the investigation that
identified these sources.

## Source classes

| Code | Source | Meaning |
|---|---|---|
| `T` | Transcript | Data in an agent transcript JSON stream. |
| `I` | IDE store | Data in Cursor IDE databases. |
| `C` | CLI store | Data in Cursor CLI databases. |
| `S` | SDK store | Data in Cursor SDK databases. |
| `P` | coSlash probe | Data that coSlash reads from Git or the filesystem. |
| `Y` | coSlash synthesis | Data that the coSlash synthesizer creates. |

Confidence levels have these meanings:

- **High:** The source stores the field directly or supports an exact join.
- **Medium:** The value needs a conversion or depends on source coverage.
- **Low:** The value is an estimate or a text-based inference.
- **None:** No observed source can supply the value.

An empty value and a zero value are not always equivalent. The collector must
use `nil` when the model permits it and the value is unknown.

## Feature parity checklist

- [x] **Local launch/resume:** Resume Cursor CLI sessions exactly with `agent --resume <id>`; open IDE session workspaces with `cursor --reuse-window`; leave SDK resume disabled. Cursor does not expose an IDE chat deep link, so opening an IDE workspace does not restore the conversation.

Last reviewed: 2026-09-10.

This checklist compares Cursor with the current Claude, Codex, and OpenCode
vendors. Update it when Cursor support changes.

Status labels have these meanings:

- **Implemented:** The collector supports the capability and has a test.
- **Partial:** The collector supports only part of the capability.
- **Deferred:** A known source exists, but the collector does not read it yet.
- **Unavailable:** No reliable local source exists today.

Full parity means that Cursor supplies the same useful session behavior as the
other vendors. It does not require Cursor to supply data that Cursor does not
store. Keep unavailable items visible and document their reason.

### Vendor registration and discovery

- [x] **Implemented:** Define `vendors.AgentCursor`.
- [x] **Implemented:** Register Cursor in the local collector.
- [x] **Implemented:** Use `~/.cursor/projects` as the discovery root.
- [x] **Implemented:** Find transcripts below `agent-transcripts` directories.
- [x] **Implemented:** Accept UUID and `agent-<uuid>` transcript IDs.
- [x] **Implemented:** Require matching directory and filename IDs for root transcripts; use the filename ID for nested `subagents` transcripts.
- [x] **Implemented:** Report source health and a missing source root.
- [x] **Implemented:** Return the complete structured family when any member meets the transcript mtime cutoff.
- [x] **Implemented:** Apply the shared candidate-file limit during local collection while keeping root and subagent transcript families together.
- [ ] **Deferred to a later PR:** Add Cursor to remote source scanning and remote collection.
- [ ] **Deferred to a later PR:** Add remote file-family parsing and fingerprint support.

See [Observed storage lanes](#observed-storage-lanes) for the path contract.

### Transcript decoding

- [x] **Implemented:** Decode user, assistant, and `turn_ended` records.
- [x] **Implemented:** Decode `text` and `tool_use` content blocks.
- [x] **Implemented:** Accept object and string tool-input payloads.
- [x] **Implemented:** Return `vendors.ErrInvalidData` for malformed JSON.
- [x] **Implemented:** Ignore unknown record and content-block types.
- [x] **Implemented:** Parse RFC 3339 timestamps.
- [x] **Implemented:** Parse Cursor human-readable UTC-offset timestamps.
- [ ] **Remaining:** Test torn final JSON records against real Cursor output.
- [ ] **Remaining:** Add fixture coverage for all observed tool-input variants.
- [ ] **Remaining:** Add compatibility fixtures from multiple Cursor versions.

### Core session fields

- [x] **Implemented:** Set `Agent` to `cursor`.
- [x] **Implemented:** Set `ID` from the validated transcript path.
- [x] **Implemented:** Set `StartedAt` from the first embedded timestamp.
- [x] **Implemented:** Set `LastActivityTime` from transcript mtime.
- [x] **Implemented:** Set `DurationMs` from the first timestamp to transcript mtime.
- [x] **Implemented:** Count user records as `Turns`.
- [x] **Implemented:** Count tool blocks as `ToolUses`.
- [x] **Implemented:** Count failed terminal records as `Errors`.
- [x] **Implemented:** Extract `FirstPrompt` from `<user_query>`.
- [x] **Implemented:** Build user and first-prompt digest entries.
- [x] **Implemented:** Use Shell cwd, common edit directories, then authoritative side-store workspace paths; leave ambiguous workspace slugs unresolved.
- [x] **Implemented:** Count and stop final terminal errors without turning them into a live session status.
- [x] **Implemented:** Load IDE, CLI, and SDK names from their side stores.
- [x] **Implemented:** Load recorded summaries from `conversation_summaries`, with trimmed IDE subtitles as fallback.
- [x] **Implemented:** Set `Entrypoint` from exact IDE, CLI, or SDK lane membership; leave ambiguous IDs unset.
- [x] **Implemented:** Prefer typed side-store creation/update times for IDE and SDK sessions. For CLI sessions, prefer the stored creation time and retain transcript mtime as the activity fallback.

See [`Session`](#session) and [`SessionDetails`](#sessiondetails) for field
precedence.

### Commands, todos, and digest

- [x] **Implemented:** Extract Shell and `run_terminal_cmd` commands.
- [x] **Implemented:** Preserve Shell command descriptions for child projection.
- [x] **Implemented:** Use the last `TodoWrite` snapshot.
- [x] **Implemented:** Map completed todo states to `Todo.Done`.
- [x] **Implemented:** Add todo digest entries when an item newly becomes completed.
- [x] **Implemented:** Pair non-first user questions ending in `?` with the next assistant text; retain unanswered questions with an empty answer.
- [ ] **Unavailable:** Add compaction or recap entries. Cursor stores no event.

See [`DigestEntry`](#digestentry) and [`Todo`](#todo) for source details.

### File edits

- [x] **Implemented:** Collect paths from `Write`, `StrReplace`, and `Delete`.
- [x] **Implemented:** Collect paths from string and object `ApplyPatch` inputs.
- [x] **Implemented:** Count attempted edit operations per path.
- [x] **Partial:** Calculate line counts from replacement and patch inputs.
- [x] **Partial:** Retain intended patch, replacement, and write content.
- [x] **Implemented:** Parse every file section in a multi-file patch and retain per-file line counts and patch text.
- [x] **Implemented:** Use the explicitly referenced latest IDE checkpoint to strengthen file paths, counts, and new-file state while retaining transcript change details.
- [x] **Implemented:** Mark `*** Add File:` patch sections as new files; do not infer creation otherwise.
- [ ] **Unavailable:** Confirm tool success from transcript JSONL.
- [ ] **Unavailable:** Recover an exact per-tool edit time from transcript JSONL.

See [`FileEdit`](#fileedit) for the attempt-versus-result boundary.

### Session families and subagents

- [x] **Implemented:** Return the root and all structured descendants when any family member is selected.
- [x] **Implemented:** Preserve `agent-<uuid>` as an independent SDK session ID.
- [x] **Implemented:** Do not infer parentage from prompt or mtime similarity.
- [x] **Implemented:** Join CLI children through `subagentInfo.parentAgentId`.
- [x] **Implemented:** Join IDE children through Composer relationship data.
- [ ] **Deferred:** Reconstruct SDK child relationships. SDK metadata joins through `agents.agent_id`, but the observed schema contains no parent identifier, so SDK sessions remain roots.
- [x] **Implemented:** Populate parent `Spawns` and child `ParentID` data.
- [x] **Implemented:** Populate `Subagent` task, result, status, duration, tools, commands, tokens, and cost.
- [x] **Implemented:** Promote child activity through shared family composition.
- [x] **Implemented:** Reject cycles in the combined path and metadata parent graph.
- [x] **Implemented:** Preserve all descendants in the root's flat local `subagents` list, with each child's immediate `parentId`.

Local JSON and the frontend retain `parentId`. The frozen snapshot v1 format
retains the flat descendant list but omits this local field; it does not encode
hierarchy or change child names.

See [`Subagent`](#subagent) and [Lane detection and source
precedence](#lane-detection-and-source-precedence) for join rules.

### Models, tokens, context, and cost

- [x] **Implemented:** Use the newest IDE bubble model.
- [x] **Implemented:** Use the IDE composer model when no bubble has a model.
- [x] **Implemented:** Load the CLI `lastUsedModel` value when present.
- [x] **Implemented:** Use the model from the highest SDK turn.
- [x] **Implemented:** Normalize Cursor Claude, GPT, and Grok families without a version-specific alias list.
- [x] **Implemented:** Treat Cursor's `default` routing label as unknown (`null`), not as a model ID.
- [x] **Implemented:** Price observed Cursor Composer 2 and 2.5 variants from explicit `models.json` entries.
- [x] **Implemented:** Resolve context windows for recognized models through the shared model table.
- [x] **Implemented:** Aggregate valid SDK input, output, cache-read, and cache-write usage by normalized model.
- [x] **Implemented:** Use the highest-turn valid SDK usage for current context tokens.
- [x] **Implemented:** Load IDE `contextTokensUsed` and `contextTokenLimit` as authoritative current-context values.
- [x] **Implemented:** Keep IDE `contextTokensUsed` as current-context occupancy instead of treating it as cumulative input usage.
- [x] **Implemented:** Load IDE `usageData.*.costInCents` as authoritative recorded cost when every entry is valid.
- [x] **Implemented:** Preserve cost knownness independently from token availability, including authoritative zero cost.
- [x] **Implemented:** Estimate SDK cost through `session.AttachCost` from trusted per-model usage.
- [ ] **Unavailable:** Split reliable IDE and CLI input and output tokens.
- [ ] **Unavailable:** Load reliable CLI token and cost data.
- [ ] **Unavailable:** Recover one-hour cache creation tokens.

See [`ModelTokens`](#modeltokens) for exact source paths and limitations.

### Status and liveness

- [x] **Implemented:** Preserve terminal failures through error and stopped facts without reporting them as running.
- [x] **Implemented:** Do not treat a missing terminal record as proof of life.
- [ ] **Deferred until live-status semantics are defined:** Read persisted IDE and SDK status values and map them alongside authoritative liveness behavior.
- [x] **Implemented:** Detect IDE sessions from Cursor processes holding UUID-specific `AgentStores/.../.sync/index.sqlite` files and CLI sessions from Cursor Agent processes holding UUID-specific `~/.cursor/chats/.../store.db` files.
- [x] **Implemented:** Populate `SessionMetadata.Live` as `interactive` after the open-file lane matches side-store membership.
- [x] **Partial:** Refine live IDE and CLI sessions to busy or idle through shared activity logic. Waiting and persisted terminal states remain deferred.
- [ ] **Unavailable:** Derive authoritative live state from transcripts alone.

### Repository and delivery activity

- [x] **Partial:** Let shared Git probes run after cwd inference.
- [x] **Implemented:** Probe the nearest existing ancestor for stale nested paths and reconcile unique local-only names to observed canonical repositories.
- [x] **Implemented:** Prefer the exact IDE side-store workspace path before Git probes.
- [x] **Implemented:** Parse commit subjects from Shell command attempts.
- [x] **Implemented:** Reconcile commit subjects against Git with collision safeguards.
- [x] **Implemented:** Read session-keyed IDE before/after Git checkpoint hashes from bubbles, retain changed full hashes, and verify them through shared repository reconciliation.
- [x] **Implemented:** Count unique PR URLs from completed IDE tool results.
- [x] **Implemented:** Count unique, syntactically valid GitHub PR URLs from assistant text.
- [ ] **Unavailable:** Recover authoritative commit SHAs from transcript commands.
- [ ] **Unavailable:** Recover historical branch and drift from current Git state.

See [`GitDrift`](#gitdrift) and [Fields that are not reliably
retrievable](#fields-that-are-not-reliably-retrievable).

### Synthesis and presentation

- [x] **Implemented:** Produce non-nil empty slices for collection fields.
- [x] **Implemented:** Leave unknown model, token, and cost fields empty.
- [x] **Implemented:** Supply transcript facts to the shared synthesis pipeline.
- [x] **Implemented:** Load cached synthesis for Cursor sessions in the temporary endpoint.
- [x] **Implemented:** Add Cursor to frontend vendor labels, filters, and session totals.
- [ ] **Remaining:** Add Cursor fixtures to preview and snapshot tests.
- [x] **Implemented:** Add Cursor to diagnostics and support-safe source summaries, probing `cursor` for the IDE and `agent` for the CLI separately.
- [x] **Implemented:** Keep dormant subagent task, result, and command fields outside the reviewed export census.
- [ ] **Unavailable:** Load a Cursor-native `SessionSynthesis` object.
- [ ] **Unavailable:** Load `DeclaredGoal` without a Cursor goal field.

### Reliability and operations

- [x] **Implemented:** Test canonical discovery and malformed JSON handling.
- [x] **Implemented:** Test commands, todos, edits, counts, and status hints.
- [x] **Implemented:** Test both observed timestamp formats.
- [x] **Implemented:** Test edit-based cwd inference before slug decoding.
- [x] **Implemented:** Test stored names for the IDE, CLI, and SDK lanes.
- [x] **Implemented:** Test complete families across incremental collection cutoffs.
- [x] **Implemented:** Cover family-safe candidate limits, live families, and pre-parse time-window selection.
- [ ] **Remaining:** Test missing roots, unreadable files, and skipped paths.
- [ ] **Remaining:** Run the parser against a sanitized real-transcript corpus.
- [ ] **Remaining:** Add schema-drift tests for missing and renamed JSON fields.
- [ ] **Remaining:** Add privacy tests for transcript and side-store content.
- [ ] **Deferred to a later PR:** Add remote parity tests after remote collection exists.
- [ ] **Remaining:** Record source quality when two sources disagree.

## Observed storage lanes

Cursor writes transcripts for three observed session lanes. The sampled corpus
did not contain ID overlap between the lanes. This separation is not a contract.

### Common transcript store

```text
~/.cursor/projects/<workspace-slug>/agent-transcripts/<uuid>/<uuid>.jsonl
~/.cursor/projects/<workspace-slug>/agent-transcripts/agent-<uuid>/agent-<uuid>.jsonl
~/.cursor/projects/<workspace-slug>/agent-transcripts/<parent-uuid>/subagents/<child-uuid>.jsonl
```

Each record has one of these observed forms:

```json
{"role":"user","message":{"content":[{"type":"text","text":"..."}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"..."},{"type":"tool_use","name":"Shell","input":{}}]}}
{"type":"turn_ended","status":"success"}
{"type":"turn_ended","status":"error","error":"User aborted request"}
```

Only `text` and `tool_use` content blocks were observed. Tool inputs record an
attempt. The transcript does not record a structured tool result, standard
output, exit status, duration, or per-tool timestamp.

The transcript filename without `.jsonl` is the session ID. For root
transcripts, the directory and filename IDs must agree. A nested transcript's
parent UUID comes from the directory above `subagents`; its filename identifies
the child. For SDK metadata joins, remove the `agent-` prefix from the transcript
ID and retain that prefix in the coSlash session ID.

### IDE Composer store

```text
~/Library/Application Support/Cursor/User/globalStorage/state.vscdb
~/Library/Application Support/Cursor/User/globalStorage/conversation-search.db
```

`state.vscdb` contains these relevant tables:

- `composerHeaders`
- `cursorDiskKV`

`composerHeaders.composerId` matched transcript UUIDs in the sampled corpus.
The table has these direct columns:

```text
composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent,
recency, checkpointAt, value, subagentTypeName
```

`composerHeaders.value` is JSON. Observed paths include:

```text
name
subtitle
unifiedMode
contextUsagePercent
filesChangedCount
totalLinesAdded
totalLinesRemoved
workspaceIdentifier.uri.fsPath
trackedGitRepos[].repoPath
trackedGitRepos[].branches[].branchName
subagentInfo.parentComposerId
subagentInfo.toolCallId
subagentInfo.subagentTypeName
```

`cursorDiskKV` is a key/value table. Relevant observed keys include:

```text
composerData:<composerId>
bubbleId:<composerId>:<bubbleId>
```

Observed `composerData` data includes `modelConfig.modelName`, `usageData`,
`subagentComposerIds`, status data, and todo data. Observed bubble data includes
`tokenCount`, `modelInfo.modelName`, timing data, tool results, commits, pull
requests, deleted files, and diff data. Coverage varies by session and version.

`conversation-search.db` contains `conversations.id`, `title`, `branches`,
`updated_at`, and archive data. Its full-text-search body is an implementation
detail. Do not depend on the full-text-search schema for collection.

### CLI agent store

```text
~/.cursor/chats/<workspace-hash>/<uuid>/store.db
```

The database contains `meta(key TEXT PRIMARY KEY, value TEXT)`. The observed
metadata row uses key `0`. Its value is hex-encoded JSON.

Observed JSON paths include:

```text
agentId
name
mode
createdAt
lastUsedModel
latestRootBlobId
subagentInfo.parentAgentId
subagentInfo.rootParentAgentId
subagentInfo.typeName
subagentInfo.toolCallId
```

`lastUsedModel` exists in some local metadata, but it is not present in every
store version. Blob data can be binary or encrypted. Treat the CLI model as
unknown when the metadata does not contain it.

### SDK agent store

```text
~/.cursor/projects/<workspace-slug>/sdk-agent-store/<store-id>/index.db
```

The database contains `agents`, `runs`, and `run_events`. In the sampled corpus,
`agents.agent_id` already used the `agent-<uuid>` transcript-folder ID. The
collector also accepts legacy bare UUID values without adding a second prefix.

Relevant `runs` columns include:

```text
agent_id, turn_number, status, model, usage_json, result, error_code,
created_at, updated_at, started_at, finished_at, cancelled_at, expired_at
```

Observed `runs.usage_json` paths include:

```text
inputTokens
outputTokens
cacheReadTokens
cacheWriteTokens
totalTokens
```

Aggregate SDK usage across the accepted runs for one agent. Define how
cancelled, failed, and repeated runs contribute before implementation.

### AI tracking store

```text
~/.cursor/ai-tracking/ai-code-tracking.db
```

The schema supports conversation summaries, model data, file attribution,
deleted files, and commit scoring. The sampled database did not contain enough
session-keyed data for reliable joins.

Use these tables only when a session-keyed row exists:

- `conversation_summaries`
- `ai_code_hashes`
- `tracked_file_content`
- `ai_deleted_files`
- `scored_commits`

Do not join `scored_commits` to a session by time alone.

## Lane detection and source precedence

Classify each transcript by exact UUID membership:

1. A matching `~/.cursor/chats/.../<uuid>/store.db` selects the CLI lane.
2. A matching SDK `agents.agent_id` selects the SDK lane.
3. A matching `composerHeaders.composerId` selects the IDE lane.
4. If no match exists, parse the transcript without side-store enrichment.

If more than one lane matches, retain the transcript and report ambiguous
metadata. Do not silently select one lane.

Use this precedence for common fields:

| Field | Preferred source order |
|---|---|
| Name | IDE header name → conversation title → CLI name → SDK agent name → first prompt. |
| Model | IDE bubble model or SDK run model → IDE composer model → CLI metadata model → unknown. |
| Tokens | SDK run usage → complete IDE bubble usage → unknown. |
| Working directory | IDE workspace path → Shell working directory → decoded workspace slug → edit paths. |
| Created time | Side-store created time → embedded prompt timestamp → filesystem time. |
| Updated time | Side-store updated time → embedded prompt timestamp → transcript mtime. |
| Parent session | Structured lane-specific parent ID → no parent. |

Do not treat `agent-<uuid>` SDK sessions as IDE or CLI `Task` children. IDE
Composer relations and CLI `subagentInfo` relations are separate mechanisms.

Cursor model labels are normalized before they enter the shared session model.
For Claude, GPT, and Grok families, Cursor reasoning modifiers (`none`, `low`,
`medium`, `high`, `xhigh`, and `thinking`) are removed while meaningful model
variants such as `fast`, `mini`, `pro`, and `codex` remain. Cursor's alternate
Claude version/family ordering is converted to the canonical ordering, and
Grok IDs receive the `xai/` provider prefix. This avoids a version-specific
alias list while leaving unrelated model names untouched; the shared matcher
remains strict. The routing label `default` maps to an empty value because it
does not identify the model Cursor selected.
The locally observed proprietary IDs `composer-2`, `composer-2-fast`, and
`composer-2.5` have explicit model-table entries. Their 200k default context
comes from Cursor metadata and documentation; their prices come from Cursor's
published model pricing. Bare `composer-2.5` uses Cursor's documented default
Fast tier. Legacy `composer-1` and `composer-1.5` remain unchanged and unpriced
because authoritative archived per-token prices are unavailable.

## `ModelTokens`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `InputTokens` | `S`: sum `runs.usage_json.inputTokens` by model. | `I`: `contextTokensUsed` assigned to the recognized current model as an experimental estimate. | High for SDK. The IDE fallback is current context occupancy, not cumulative or necessarily billable input, and exists to gather product feedback. CLI has no reliable source. |
| `OutputTokens` | `S`: sum `runs.usage_json.outputTokens` by model. | None. | High for SDK. IDE and CLI have no observed split that maps reliably. |
| `CacheCreationInputTokens` | `S`: sum `runs.usage_json.cacheWriteTokens` by model. | None. | High for SDK after the semantic mapping is accepted. IDE and CLI are unavailable. |
| `CacheCreation1hInputTokens` | None. | Zero. | No Cursor source distinguishes one-hour cache writes. |
| `CacheReadInputTokens` | `S`: sum `runs.usage_json.cacheReadTokens` by model. | None. | High for SDK. IDE and CLI are unavailable. |
| `Cost` | `I`: recorded cost in `usageData`, when present and defined. | `P`: `session.AttachCost` from trusted model-token data. | Recorded cost is rare. An estimated cost is not a Cursor-recorded fact. |

IDE `tokenCount` remains unused because its semantics and coverage are unknown.
The `contextTokensUsed` proxy provides no input/output/cache split and can
understate cumulative session usage; do not treat its calculated cost as an
authoritative Cursor charge.

## `SubagentCommand`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `Label` | `T`: the child Shell description. | The command string. | Medium. Cursor tool payloads can omit descriptions. |
| `Command` | `T`: the child Shell or `run_terminal_cmd` command input. | None. | High as an attempted command. Execution is not confirmed. |

## `Subagent`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `ID` | Validated child transcript filename, joined through an IDE header, CLI `subagentInfo`, or nested transcript path. | None. | High with a structured relation. Prompt or mtime matching is not sufficient. SDK child relationships remain deferred. |
| `ParentID` | Immediate parent from the combined structured path and metadata graph. | None. | Local JSON only. Cyclic edges are rejected. |
| `Name` | Child IDE header or CLI `meta.name`, including metadata for path-only children. | Child session ID. | High for a non-default stored name. `New Agent` is ignored. Task text and first prompts are not name fallbacks. |
| `Model` | IDE child bubble model. | IDE composer model or CLI metadata model. | High for a bubble model. CLI coverage varies. SDK sessions remain roots. |
| `Status` | Terminal child error → aborted; completed parent task or terminal child success → returned; active structured task → running. | Aborted when no active state is recorded. | New transcript activity clears prior terminal state. A running IDE bubble without a result ID requires an unambiguous header match on parent ID and tool-call ID. |
| `Task` | Structured relationship task text: IDE `task_v2.params.description` or CLI `subagentInfo.typeName`. | Child first prompt. | Parent Task text matches the first unclaimed exact description to recover the turn. Matching uses full internal text; only display text is truncated. |
| `Result` | `T`: child final assistant text. | None. | Low confidence because transcript JSONL has no structured tool result. SDK `runs.result` is a future source. Current SDK loading does not read it. |
| `DurationMs` | `T`: first embedded timestamp to transcript mtime. | Child side-store timestamp span or filesystem span. | Medium. SDK run timestamps are a future source. Current SDK loading does not read them. |
| `SpawnedAtTurn` | `T`: the parent Task position. | None. | High when a structured relation links the parent and child. |
| `ToolUses` | `T`: count child `tool_use` blocks. | None. | High. This counts requests, not successful operations. |
| `Commands` | `T`: child Shell commands through `session.CommandLog`. | Empty list. | High as attempted commands. |
| `Tokens` | Complete IDE child bubble usage, if its meaning is known. | Empty map. | Reliable IDE and CLI child token counts are not currently available. SDK usage applies to root sessions. |
| `Cost` | Recorded IDE usage cost, when present. | `P`: estimated cost from trusted child tokens. | Low for most sessions. |

IDE `subagentComposerIds` and header `subagentInfo` can describe Composer
relations. They do not prove that a transcript `Task` call created the child.

## `Session`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `Agent` | Constant `cursor`. | None. | High. |
| `ID` | Validated transcript filename ID; the containing directory must match for root transcripts. | None. | High. Keep the `agent-` prefix in the coSlash session ID. Nested children use their own filename ID. |
| `Name` | Lane-specific stored name or conversation title. | Truncated first prompt. | High for a non-default stored name. A generated fallback is low confidence. |
| `Summary` | Trimmed `conversation_summaries.tldr`, then `overview`. | Trimmed IDE `composerHeaders.value.subtitle`. | Conversation-search titles, FTS body, and coSlash synthesis are not used. |
| `Status` | `P`: open UUID-specific IDE AgentStore or CLI store, mapped to `interactive` and refined through shared busy/idle activity logic. | Trailing transcript `turn_ended` as `StatusHint`. | High for IDE/CLI process liveness after lane validation. It does not distinguish waiting, and SDK liveness remains unavailable. |
| `WorkingDirectory` | IDE `workspaceIdentifier.uri.fsPath`. | Shell working directory, decoded workspace slug, then edit-path inference. | High for the IDE path. Slug decoding and edit-path inference are heuristic. |
| `Branch` | `P`: Git symbolic-ref in the resolved working directory. | IDE tracked branch data, if it matches the worktree. | The Git probe is accurate for current state. It is not historical session state. |
| `Repository` | `P`: `CanonicalRepositoryName(cwd)`. | IDE tracked repository path. | High with a valid working directory. |
| `RepositoryLocalOnly` | `P`: repository canonicalization. | Unknown. | High with a successful probe. Do not infer this value from transcript text. |
| `EditedFileCount` | Count unique `FileEdits` paths. | IDE `filesChangedCount`. | Transcript paths count attempted edits. The IDE count can use different semantics. |
| `DurationMs` | SDK lifecycle timestamps. | Side-store time span, embedded timestamp span, then filesystem span. | High for SDK. Other lanes are estimates. |
| `Tokens` | SDK usage grouped by model. | Complete IDE bubble usage grouped by model. | High for SDK. IDE is conditional. CLI is unavailable. |
| `Cost` | Recorded IDE cost with known units. | `P`: `session.AttachCost` from trusted tokens and model. | Usually estimated or unavailable. |
| `UnpricedModels` | `P`: `session.UnpricedModels` after cost attachment. | Empty list when no model-token map exists. | Derived by coSlash. It is not stored by Cursor. |
| `Subagents` | Structured IDE and CLI child relations or nested paths, plus child transcripts. | Empty list. | All descendants appear in a flat list with local `parentId` context. SDK child relationships remain deferred. Prompt text and mtime never establish parentage. |
| `StartedAt` | Side-store created timestamp. | First embedded `<timestamp>`, then filesystem birth/mtime. | High for a typed side-store timestamp. Embedded text and filesystem values are fallbacks. |
| `LastActivityTime` | Side-store updated timestamp. | Latest embedded timestamp, then transcript mtime. | High for a typed side-store timestamp. Family promotion can make this newer than parent activity. |
| `Entrypoint` | Exact side-store membership: IDE `cursor-ide`, CLI `cursor-cli`, or SDK `cursor-sdk`. | `nil` for no match or multiple lane matches. | Conversation-search membership does not classify a lane. |
| `CommitLog` | Parsed commit subjects from Shell inputs, plus changed full hashes from IDE bubble `gitCheckpoint.commitHashesByGitWorkspace` → `afterGitCheckpoint.commitHashesByGitWorkspace`, for later reconciliation. | Empty list. | Internal evidence only. Checkpoint hashes are session-keyed and stronger than commands, but are still verified against the current repository. |

## `DigestEntry`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `Turn` | User-record index in transcript order. | Sequential message index. | High. Count user records as turns. Do not require an optional `<user_query>` wrapper. |
| `Category` | Record/tool type mapped to `first_prompt`, `user`, `question`, `todos`, or `subagent`. | `user`. | High for direct mappings. Cursor has no observed compaction or recap event. |
| `Description` | User text, todo text, or Task description. | Truncated tool input. | High for stored text. Strip known wrappers without deleting user content. |
| `Answer` | Adjacent assistant text for a question. | Empty string. | Medium. The answer can omit tool results or contain redaction markers. |
| `SubagentID` | Structured lane-specific child ID. | Empty string with an internal spawn key. | High with a structured relation. Do not use prompt similarity as an authoritative ID. |
| `Time` | Typed side-store or SDK event timestamp. | Embedded `<timestamp>`, then zero. | High for typed data. Do not assign transcript mtime to every event. |
| `SpawnKey` | Collector-generated key for the Task occurrence. | Empty string. | Internal correlation state. Cursor does not store this coSlash field. |

The collector must not create `compaction` or `recap` digest entries without a
specific persisted event.

## `FileEdit`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `Path` | `T`: `Write`, `StrReplace`, `Delete`, or `ApplyPatch` target path. | IDE diff or deleted-file metadata. | High as an attempted target. Normalize relative paths against the resolved working directory. |
| `Additions` | Parsed `ApplyPatch` hunk additions or `StrReplace` new text. | IDE aggregate line counts or current Git diff. | Medium. No tool result confirms that the edit succeeded. |
| `Deletions` | Parsed `ApplyPatch` hunk deletions or `StrReplace` old text. | IDE aggregate line counts or current Git diff. | Medium. Current Git state is not historical proof. |
| `Edits` | Count mutation-tool attempts for the normalized path. | Diff-block count. | Medium. This is an attempt count. |
| `IsNew` | Patch create-file syntax or side-store diff operation. | Filesystem and Git new-file state. | Medium with explicit create syntax. A `Write` call alone does not prove creation. |
| `changes` | Parsed patch, replacement, or write content passed to `FileEditSet`. | Empty internal list. | Medium as intended content. It is unexported and does not confirm final disk state. |

`Delete` has no direct representation in `FileEdit.IsNew`. Preserve its path and
change details, but do not mark it as a new file.

### Supplemental `FileChange`

`FileChange` is defined in `collector/internal/session/file_edits.go`. It supports
the internal inspector data behind each `FileEdit`.

| Field | Cursor source | Confidence and limits |
|---|---|---|
| `Kind` | `diff` for patch or replacement data. `content` for Write data. | High after payload parsing. |
| `Text` | Patch text, rendered old/new strings, or Write contents. | High as intended content. Sensitive source text must not enter logs. |
| `Operation` | Tool name mapped to `Add`, `Edit`, `Patch`, or `Write`. | Medium. Tool intent does not confirm success. |
| `Additions` | Parsed added-line count. | Medium. |
| `Deletions` | Parsed deleted-line count. | Medium. |

## `GitDrift`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `BaseBranch` | `P`: `BranchDrift` in the resolved working directory. | None. | High for current Git state. Not available from transcripts. |
| `Ahead` | `P`: `BranchDrift`. | None. | High for current Git state. |
| `Behind` | `P`: `BranchDrift`. | None. | High for current Git state. |

## `Todo`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `Text` | `T`: final `TodoWrite.todos[].content` value. | IDE composer todo data, then prompt checklist text. | High for `TodoWrite`. Prompt parsing is low confidence. |
| `Done` | `T`: map the final todo status to a boolean. | IDE composer todo status. | High when the status has known semantics. Unknown status values must remain incomplete. |

Use the last complete todo snapshot. Do not append every historical state as a
new todo.

## `SessionSynthesis`

Cursor does not store this coSlash object. The existing coSlash synthesizer can
create it after the collector populates the session.

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `Goals` | `Y`: synthesis from user prompts and todos. | Explicit first-prompt goals. | Generated data. It is not a Cursor fact. |
| `Outcome` | `Y`: synthesis from transcript and session evidence. | Final assistant text and terminal status. | Tool success is unknown, so the outcome can be incomplete. |
| `KeyDecisions` | `Y`: synthesis from explicit user and assistant statements. | Empty list. | Do not convert tool attempts into decisions. |
| `NextStep` | `Y`: synthesis from unresolved todos and final text. | Empty string. | Leave empty when the evidence is insufficient. |

## `SessionDetails`

| Field | Preferred source | Fallback | Confidence and limits |
|---|---|---|---|
| `Model` | Newest IDE bubble model or highest SDK turn model. | IDE composer model, CLI `lastUsedModel`, then `nil`. | High for per-run data. `Model` shows the last model; SDK token totals retain every normalized model. |
| `ContextTokens` | IDE composer `contextTokensUsed`, or input plus cache-read and cache-write tokens from the highest-turn valid SDK usage through `session.ContextTokens`. | Rare context-usage canvas `totalTokensUsed`, then percent-based estimate. | High for persisted IDE and SDK values. This is current context occupancy, not cumulative billable usage. Canvas coverage is rare and percent conversion is low confidence. CLI does not persist this value. |
| `ContextWindow` | IDE composer `contextTokenLimit`, then context-usage canvas `contextWindowSize`. | `P`: `session.ContextWindowFor(model)`. | Persisted session values are authoritative. Model-table data is an external estimate. CLI does not persist its status-line value. |
| `Turns` | Count transcript user records. | None. | High. Optional XML wrappers do not define turns. |
| `ToolUses` | Count transcript `tool_use` blocks. | None. | High as requested tool calls. |
| `Errors` | Count `turn_ended` records with `status=error`. | SDK failed-run count with a defined merge policy. | High for recorded turn errors. Tool-level errors are invisible in transcript JSONL. |
| `Compactions` | None. | Zero. | No compaction event was found in any observed local source. |
| `FirstPrompt` | First user record after known wrapper removal. | Raw first user text. | High. Preserve attached context separately when practical. |
| `Commands` | Shell and `run_terminal_cmd` command inputs. | Empty list. | High as attempted commands. No exit status or output exists in transcript JSONL. |
| `Commits` | IDE bubbles with changed before/after Git checkpoint hashes and commit subjects parsed from Shell commands, then `P`: `ReconcileCommits`. | Empty list. | IDE hashes are exact and repository-verified; command-only subjects remain heuristic. A checkpoint jump exposes only the final observed hash. |
| `PullRequests` | Successful PR metadata in IDE bubble data, when defined. | `gh pr create` attempts and PR URLs in assistant text. | Medium. A command records an attempt, not a created PR. |
| `Todos` | Final transcript `TodoWrite` snapshot. | IDE composer todo data. | High when the payload has known status values. |
| `Digest` | User prompts, questions, todos, and Task events in transcript order. | Empty list. | High for direct events. Compaction and recap categories are unavailable. |
| `FileEdits` | Mutation-tool inputs parsed through `FileEditSet`. | IDE diff metadata. | Paths are strong. Counts, success, and final content are not authoritative. |
| `Git` | `P`: `BranchDrift`. | `nil`. | High for current state after working-directory resolution. |
| `GitProbed` | Set by the shared probe path. | `false`. | coSlash bookkeeping, not Cursor data. |
| `LastEditAt` | `P`: `LatestFileModificationTime` for collected edit paths. | Latest typed IDE diff time, then `nil`. | A filesystem mtime is a heuristic and can include later edits. |
| `Synthesis` | `Y`: existing asynchronous synthesizer. | `nil`. | Generated by coSlash. |
| `SynthesisPending` | coSlash synthesis lifecycle. | `false`. | coSlash bookkeeping, not Cursor data. |
| `DeclaredGoal` | None. | `nil`. | No Cursor equivalent to the Claude `/goal` field was observed. Do not copy the first prompt into this field. |
| `CompactionSeed` | None. | Empty string. | Cursor has no observed compaction event or seed. |

## Fields that are not reliably retrievable

| Information | Availability | Reason |
|---|---|---|
| IDE and CLI input/output token split | Not reliable. | Transcripts omit usage. IDE bubble counts are sparse and do not always define token direction. CLI metadata has no reliable usage data. |
| IDE and CLI cache-token split | Not retrievable. | No complete observed source maps these categories. |
| One-hour cache creation tokens | Not retrievable. | Cursor does not distinguish this category in observed usage data. |
| IDE and CLI recorded cost | Usually not retrievable. | IDE `usageData` is sparse. CLI stores have no reliable cost field. An estimated cost is not recorded cost. |
| Tool result, output, exit code, or edit success | Not retrievable from transcripts. | JSONL stores tool inputs but no structured `tool_result`. A turn success applies to the whole turn. |
| Historical branch and Git drift | Not retrievable. | The shared Git probe reads current worktree state. Cursor transcripts do not store historical state. |
| Authoritative commit SHA from a command | Not retrievable from transcripts. | Shell output is absent. Subject reconciliation can select the wrong commit. |
| Authoritative PR number from a command | Not retrievable from transcripts. | A `gh pr create` input records only an attempt. Assistant text and IDE metadata can supply a number in some sessions. |
| Per-tool timestamp | Not retrievable from transcripts. | Tool blocks have no timestamp. Side stores can have partial bubble timing only. |
| Exact edit time | Not reliable. | File mtime can include edits after the session. Tool blocks have no timestamp. |
| Compaction count and seed | Not retrievable. | No observed transcript or side-store event identifies compaction. |
| Authoritative IDE or CLI live state | Not retrievable with current sources. | A missing `turn_ended` record does not prove activity. coSlash has no Cursor process, socket, or lock probe. |
| Complete IDE Task-to-child mapping | Not always retrievable. | Composer relations and transcript Task calls use separate identifiers and are not always linked. |
| CLI model for every session | Not always retrievable. | `lastUsedModel` is version-dependent. Other blobs can be binary or encrypted. |
| Cursor-native `SessionSynthesis` | Not retrievable. | Cursor does not store the coSlash synthesis schema. coSlash generates it. |
| `DeclaredGoal` | Not retrievable. | No explicit Cursor goal field was observed. The first prompt is not equivalent. |

## Operational and privacy rules

- Open live SQLite databases in read-only mode.
- Tolerate write-ahead-log files, locks, missing rows, and partial updates.
- Do not copy raw transcript, bubble, diff, blob, or file content into logs.
- Do not fail collection when an optional database is missing or unreadable.
- Treat all JSON paths and columns as optional at runtime.
- Record the selected source and confidence internally when practical.
- Reject malformed UUID joins instead of joining by approximate time.
- Do not decrypt opaque blobs as part of the initial integration.
- Apply normal coSlash privacy and remote-export filtering to Cursor data.

Ghost mode, privacy settings, cleanup, archive operations, and Cursor upgrades can
reduce stored data. The collector must still produce a transcript-only session
when all side stores are unavailable.
