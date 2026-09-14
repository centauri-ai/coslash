# Cursor agent transcripts → coSlash session model

What Cursor stores in agent transcripts, what maps into
`collector/internal/session/session.go`, and how to recover fields that
transcripts omit (side stores, shared probes, synthesis).

Investigated against local Cursor data on macOS (~305 transcripts) and the
existing Claude / Codex / OpenCode collector paths. There is **no Cursor
vendor** in coSlash yet.

---

## 1. Transcript layout

```text
~/.cursor/projects/<workspace-slug>/agent-transcripts/<uuid>/<uuid>.jsonl
~/.cursor/projects/<workspace-slug>/agent-transcripts/agent-<uuid>/agent-<uuid>.jsonl
```

- Workspace slug encodes the project path
  (e.g. `Users-calvin-centauri-coslash` → `/Users/calvin/centauri/coslash`).
- Most sessions use a bare UUID directory.
- `agent-<uuid>/` folders are **SDK / Pi-style child agents**, not Task
  subagents from the IDE/CLI agent UI.

Sibling project files (`repo.json`, terminals, canvases, `agent-tools/`)
are not part of the transcript schema; some are useful as side channels
(see §4).

---

## 2. What a transcript line contains

Each JSONL line is almost always one of:

```json
{"role":"user"|"assistant","message":{"content":[{"type":"text","text":"..."},{"type":"tool_use","name":"Shell","input":{...}}]}}
{"type":"turn_ended","status":"success"|"error","error":"User aborted request"}
```

### Present

| Kind | Notes |
|------|--------|
| `role` + `message.content` | Only `text` and `tool_use` content blocks observed |
| Tool **inputs** | e.g. Shell `command`, Write `path`/`contents`, Task `prompt` |
| User XML wrappers | `<timestamp>`, `<user_query>`, `<attached_files>`, `<code_selection>`, … |
| `turn_ended` | `success` or `error` (e.g. user abort) |

### Absent from JSONL

- Token usage / cost / model id
- Message IDs, structured timestamps (only free-text `<timestamp>` in user text)
- `tool_result` / tool stdout (assistant mid-turn text often `[REDACTED]`)
- Compaction events
- Parent→child spawn UUIDs on `Task` tool calls

### Common tools (census)

`Read`, `ReadFile`, `Shell`, `Grep`, `rg`, `Glob`, `StrReplace`, `Write`,
`Delete`, `ApplyPatch`, `Task`, `TodoWrite`, `WebSearch`, `WebFetch`,
`CallMcpTool`, …

---

## 3. Transcript-only → `session.Session`

Relative to `collector/internal/session/session.go`.

### Solid from transcript (+ path / mtime)

| Field | Source |
|-------|--------|
| `Agent` | Constant `"cursor"` |
| `ID` | Directory / filename UUID |
| `FirstPrompt` / digest user turns | `<user_query>` text |
| `Turns` | Count of user_query (or user) rows |
| `ToolUses` | Count of `tool_use` blocks |
| `Commands` | Shell / `run_terminal_cmd` `input.command` |
| `Todos` | Last `TodoWrite` snapshot (`content` + `status`) |
| `EditedFileCount` / `FileEdits` (paths) | Write / StrReplace / Delete / ApplyPatch inputs; ApplyPatch hunks can yield real adds/dels |
| `WorkingDirectory` | Best-effort: decode workspace slug, Shell `working_directory`, or edit paths |
| `LastActivityTime` | Transcript file mtime |
| `StartedAt` / weak `DurationMs` | First `<timestamp>` + mtime (or timestamp span) |
| `Errors` | `turn_ended` with `status=error` |
| `Status` | Weak: trailing `turn_ended` → done/aborted; open without it → incomplete/live? |
| `Digest` | User prompts, todo updates, Task spawns (no compaction / recap events) |
| `Subagents` (spawn intent) | Task tool_use: `description`, `prompt`, `subagent_type` — **no child id / result / tokens** |
| `Entrypoint` | Not present; could hardcode e.g. `"cursor-agent"` |

### Missing from transcripts alone

| Field | Why |
|-------|-----|
| `Tokens`, `Cost`, `UnpricedModels`, `Model`, `ContextTokens`, `ContextWindow` | Never written |
| `Name`, `Summary`, `DeclaredGoal`, `Synthesis` | No title / summary rows (synthesis is coSlash-side) |
| `Branch`, `Repository`, `Git`, `repoLocalOnly` | Not in JSONL |
| `Commits`, `PullRequests`, `CommitLog` | Shell **commands** only; no stdout → no hashes |
| `Compactions` | No events |
| `LastEditAt` | No per-tool timestamps |
| Rich `Subagents[]` | Parent Task calls do not embed child UUID |
| Accurate `FileEdits` (`adds`/`dels`/`isNew`, success) | No `tool_result`; failed edits look like successful ones |

**Bottom line (transcript-only):** enough for a conversation / activity card
(prompts, turns, tools, commands, todos, edited paths, weak cwd / status /
timing). Not enough for metering, git card fidelity, live status, or linked
subagent trees the way Claude JSONL supports.

---

## 4. Alternative sources for missing fields

### 4.1 Cursor on-disk lanes (join by transcript UUID)

Cursor sessions are **three disjoint ID spaces**, all writing agent-transcripts:

| Lane | Approx. share (this Mac) | Primary store |
|------|--------------------------|---------------|
| IDE Composer | ~229 / 305 | `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb` (`composerHeaders`, `cursorDiskKV`) + `conversation-search.db` |
| CLI agent | ~62 / 305 | `~/.cursor/chats/<workspace-hash>/<uuid>/store.db` |
| SDK (`agent-*`) | ~13 / 305 | `projects/*/sdk-agent-store/*/index.db` |

`composer ∩ chats = ∅`. Join is exact UUID (strip `agent-` for SDK).

#### IDE — `state.vscdb` / `conversation-search.db`

- **`composerHeaders`**: `name`, `subtitle`, `unifiedMode`, `contextUsagePercent`, line-change counts, workspace `fsPath`, timestamps; `composerId` == transcript UUID.
- **`cursorDiskKV`**:
  - `composerData:<id>` → `modelConfig.modelName`, sparse `usageData` (cost rare), `subagentComposerIds`
  - `bubbleId:<id>:<bubble>` → per-bubble `tokenCount`, `modelInfo.modelName` (often zero for transcript-linked composers on this disk)
- **`conversation-search`**: `title` (+ FTS body) for IDE chats.

#### CLI — `~/.cursor/chats/.../store.db`

- `meta` (hex JSON): `name`, `mode`, `lastUsedModel`, **`subagentInfo`** (`parentAgentId`, `typeName`, `toolCallId`), `createdAt`.
- Blobs may hold finer `modelName`; many are binary / encrypted.
- **No reliable token / cost** found.

#### SDK — `sdk-agent-store/*/index.db`

- `runs.usage_json`: `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`, `totalTokens`.
- `model`, timestamps.
- Join: folder `agent-<uuid>` ↔ `agents.agent_id` (full coverage of local `agent-*` transcripts).
- No cost field.

#### `~/.cursor/ai-tracking/ai-code-tracking.db`

| Table | Capability | This machine |
|-------|------------|--------------|
| `conversation_summaries` | title, tldr, overview, model, mode | **0 rows** |
| `ai_code_hashes` | conversationId, model, provenance | conversationId / model **NULL** |
| `scored_commits` | commit AI line attribution, branch | Filled; **not session-keyed** |
| `tracked_file_content` / `ai_deleted_files` | file ↔ conversation / composer | Empty |

Right schema for summaries / model if Cursor starts populating it; not usable for joins today.

#### Sparse extras

- Canvas `canvases/context-usage-<uuid>.canvas.data.json`: `composerId`, `totalTokensUsed`, `contextWindowSize`, chat name — **accurate but rare**.
- `cli-config.json` / `ide_state.json` / workspace `state.vscdb`: not per-session metering.
- `agent-tools/`: opaque tool dumps; **unreliable** for parent/child linking.

### 4.2 Shared coSlash probes (reuse after parse)

Pipeline today: vendor `Collect` → `finalizeSessions` → List-only
`probeLastEdits` + `probeGitEnvironment`. Remote list skips FS probes.

| Field(s) | Mechanism | Needs | Cursor reuse |
|----------|-----------|-------|--------------|
| `Repository`, `RepositoryLocalOnly` | `CanonicalRepositoryName(cwd)` | Real cwd | Yes if cwd resolved |
| `Branch` (if missing) | `git symbolic-ref` | cwd | Yes (OpenCode is probe-only today) |
| `Git` / `GitProbed` | `BranchDrift` | cwd + branch | Yes |
| `Commits` (final) | `ReconcileCommits` vs `git log` | cwd + CommitLog subjects | Yes (hashes from git, not transcript) |
| `LastEditAt` | `LatestFileModificationTime` | cwd + FileEdits | Yes (mtime heuristic) |
| `LastActivityTime` / `StartedAt` | Event times or log mtime | transcript path | Yes |
| `Cost`, `ContextWindow` | `AttachCost` / `ContextWindowFor(model)` | tokens + model | Estimate when no recorded cost |
| `Status` refinement | `LiveStatus(InTurn, …)` | **Live map** | Refinement reusable; **live discovery is greenfield** |
| `Synthesis` | Existing async synthesizer | Filled Session | Fully reusable |
| `DeclaredGoal` | Claude `/goal` only | — | Leave nil unless Cursor gains equivalent |

Live probes today are vendor-specific (Claude pid files, Codex `lsof`, OpenCode `ps`+cwd). Cursor needs its own live signal if busy/idle should match other agents.

### 4.3 Synthesis from transcript intent

| Concern | Grade | Notes |
|---------|-------|-------|
| FileEdit **path** | Accurate | From tool inputs |
| FileEdit **adds/dels** / **IsNew** | Heuristic | Patch math / Write≠create; over-counts failures |
| Edit **success** | Impossible (JSONL) | No tool_result; `turn_ended` is turn-level only |
| Commit **subject** | Accurate | From `git commit -m` / HEREDOC in Shell input |
| Commit **hash** | Impossible in JSONL | Never in stdout; assistant text rarely repeats new SHAs |
| Commit display list | Heuristic | Subject + `ReconcileCommits` (subject collisions possible) |
| PR **create attempts** | Accurate | `gh pr create` in command text |
| PR **URL / number** | Often accurate | Frequently appears in later assistant text |
| Task → child link via chat `subagentInfo` | Accurate | CLI store; Task children are normal UUID dirs |
| Task → `agent-*` as children | Wrong | SDK boundary prompts; do not link as Task kids |
| Prompt-overlap parent↔child | Heuristic | Works when unique; collapses under naive prefixes; misses deleted children |
| mtime-only subagent link | Unreliable | Parallel Tasks share create times |
| Compactions | Impossible | No events in any local store found |

---

## 5. Consolidated field recovery map

| Field | Best recovery | Accuracy |
|-------|---------------|----------|
| **Name** | IDE headers / conversation-search; CLI `meta.name`; else truncated `user_query` | Accurate when non-default; CLI often `"New Agent"` |
| **Summary** | `conversation_summaries` (empty today); else subtitle / FTS | Unavailable now; heuristic only |
| **Model** | Bubble `modelInfo` / SDK `runs.model`; else composerData / `lastUsedModel` | Accurate from bubbles/runs; config can be stale |
| **Tokens** | SDK `usage_json`; IDE bubble counts (often 0); CLI none | SDK accurate; IDE unreliable; CLI impossible locally |
| **Cost** | Rare IDE `usageData`; else `AttachCost` estimate | Estimate for most sessions |
| **ContextTokens / ContextWindow** | Rare context-usage canvas; else `%` × inferred window | Canvas accurate but rare; else heuristic |
| **Branch / Repository / Git** | Shared git probes after cwd resolve | Accurate if cwd is a real worktree |
| **Commits** | Shell subjects + `ReconcileCommits` | Heuristic |
| **PullRequests** | `gh pr create` + assistant URLs | Attempt accurate; URL often accurate |
| **FileEdits** | Tool inputs + optional patch math + `probeLastEdits` | Path accurate; stats/success weak |
| **LastEditAt** | File mtimes of edit paths | Heuristic |
| **Subagents** | CLI `subagentInfo` + child JSONL; Task intent from parent | Link accurate via meta; Result/Duration heuristic; Tokens impossible in JSONL |
| **Status / InTurn** | StatusHint from transcript; live probe TBD | Live = greenfield |
| **DeclaredGoal** | — | Leave nil |
| **Synthesis** | Existing synthesizer | Reusable |
| **Compactions** | — | Impossible |
| **DurationMs** | Timestamp span / mtime | Heuristic |

---

## 6. Recommendations for a Cursor vendor

### Worth wiring

1. **Lane detect** by UUID presence: `chats/` → CLI; `composerHeaders` → IDE; `agent-*` + sdk-store → SDK.
2. **Name + Model** from the matching side store (prefer per-bubble / run model over single config field).
3. **Parent↔child** via CLI `subagentInfo` (never treat `agent-*` transcripts as Task children).
4. Shared **git / last-edit / cost-estimate / synthesis** once cwd is inferred (slug, Shell cwd, or composer workspace URI).
5. **SDK `usage_json`** when present.

### Accept as best-effort

- FileEdit line stats, commit subject→hash reconcile, PR URLs from assistant text, context usage percent.

### Do not expect from local disk today

- Reliable IDE/CLI token + cost parity with Claude.
- Compaction counts.
- Per-tool edit success.
- Populated `conversation_summaries` / session-keyed `ai_code_hashes`.
- Live busy/idle without a new Cursor process / socket / lock probe.

### Privacy / ops caveats

- Global `state.vscdb` is large and live while Cursor is open (full chat / bubble / diff payloads).
- CLI blobs may be encrypted.
- Ghost mode / privacy settings may thin what lands on disk.
- Dual ID spaces: do not assume one store covers all transcripts.

---

## 7. Practical coverage summary

| Surface | Outlook |
|---------|---------|
| Activity card (prompts, turns, tools, todos, paths) | Strong from JSONL alone |
| Naming + model | Strong with side stores |
| Git card | Strong via shared probes + inferred cwd |
| Subagent tree (CLI) | Strong via `subagentInfo` |
| Metering (tokens / cost) | Strong for SDK only; IDE/CLI thin → estimates |
| Live busy/idle | Needs new probe |
| Compactions / edit success / commit hashes from transcript | Not available |

From Cursor transcripts alone you get a conversation/activity card. With side stores + existing coSlash probes you can reach a useful unified Session for most board fields; metering and success-backed edits will lag Claude/Codex until Cursor persists usage and tool results more consistently.


