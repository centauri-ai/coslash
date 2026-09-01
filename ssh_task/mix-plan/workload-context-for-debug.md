# Workload context for SSH debugging

Internal notes for debugging remote Machines / SFTP refresh and designing the next
parity steps vs local collection. Captured from coworker agent-box incidents and
follow-up measurement (2026-08-31). Not user-facing product docs.

Related PRs: [#128](https://github.com/centauri-ai/coslash/pull/128) (→ `main`),
[#129](https://github.com/centauri-ai/coslash/pull/129) (→ `test/observability`).

---

## Symptom summary

- SSH / SFTP itself worked (`control_master` hit/started, `sftp_open` ok in ~0.6–3s).
- UI often empty or stuck on **connecting**; host strip later **limited** /
  `partial_agent_data`.
- Logs (pre-fix): Claude sometimes produced `sessions=11` (or ~40 after fixes) with
  `claude:cand=…,sel=…`, while Codex stayed `cand=0,sel=0,err=true`.
- Round trips sat near the session deadline (~85–92s when deadline was 90s).
- Separate UI bug: empty time window showed
  “CoSlash couldn’t load sessions from the API” instead of empty-state copy.

---

## Why local does not show the same timeout

| | Local | Remote (SSH/SFTP) |
|---|---|---|
| I/O | Direct disk | Every readdir / lstat / realpath / open is a network RTT |
| Deadline | No single shared SFTP session deadline for the whole collect | One `DefaultDeadline` for the SSH/SFTP child |
| Vendor failure | Soft-fail per vendor; others still serve | Historically: one agent failure → limited and **discard** all sessions |
| Scale on this host | Same trees, but GB-scale Codex finishes in seconds–tens of seconds | Same work can take minutes or hit byte limits first |

Timeouts are primarily **SSH latency + bandwidth + hard remote limits**, not
“Codex only broken over SSH.”

---

## Remote safety limits (code)

From `collector/internal/remote/limits.go` (after partial-result PR):

| Limit | Value | Role |
|---|---|---|
| `DefaultConnectTimeout` | 60s | ControlMaster / connect |
| `DefaultDeadline` | **3m** (was **90s**) | Entire SFTP session for collect |
| `DefaultMaxFileBytes` | **32 MiB** | Single remote file open |
| `DefaultMaxTotalBytes` | **128 MiB** | Aggregate bytes read per refresh |
| `DefaultMaxEntries` | 10_000 | Directory entries |
| `DefaultMaxDepth` | 16 | Allowlisted tree depth |
| `FreshnessInterval` | 3m | When to consider cache stale for auto refresh |
| `InitialRetryBackoff` | 3m | Backoff after limited / hard failure |

Allowlist (remote home): `.claude/projects|sessions|jobs`,
`.codex/sessions|archived_sessions|session_index.jsonl`
(`collector/internal/remote/source.go`).

Parse window: `parseSince = max(0, since - 24h)` then `FilesSince` / list filter
at UI `since` (`refreshSFTPWithOpen` + `collector.ListRemote`).

---

## Workload: Claude session families (agent-box)

Claude stores roots and subagents separately; family-level stats matter for
selection limits and SFTP cost.

| Metric | Value |
|---|---|
| Session families | 41 |
| Files | 131 |
| Root files | 41 |
| Subagent files | 90 |
| Total size | **92.57 MiB** |
| Mean / median family | 2.26 MiB / 1.18 MiB |
| P90 / P95 family | 6.47 MiB / 8.04 MiB |
| Max family | 8.64 MiB |
| Families with subagents | 23 |
| Largest family file count | 11 |
| Subagent bytes | 21.50 MiB |
| Largest individual file | 7.79 MiB |
| Largest 10 families | **66.3%** of Claude bytes |

### Family size histogram

| Size | Count |
|---|---|
| &lt;256 KiB | 7 |
| 256 KiB–1 MiB | 11 |
| 1–4 MiB | 14 |
| 4–16 MiB | 9 |
| ≥16 MiB | 0 |

### By recent activity

| Age | Families | Size |
|---|---|---|
| &lt;1 day | 3 | 4.09 MiB |
| 1–7 days | 4 | 11.91 MiB |
| 7–30 days | 34 | 76.57 MiB |
| ≥30 days | 0 | 0 |

**Implication:** Entire Claude tree fits under 32 MiB/file and under 128 MiB total.
Over SSH it is **slow but finishable** (coworker logs ~70s Claude-heavy reads when
deadline was 90s). No Claude file exceeds the per-file cap.

---

## Workload: active Codex transcripts (agent-box)

| Metric | Value |
|---|---|
| Files | **296** |
| Total size | **1,127.3 MiB (~1.18 GiB)** |
| Mean / median | 3.81 MiB / 1.07 MiB |
| P90 / P95 / P99 | 8.87 / 14.51 / 32.93 MiB |
| **Maximum file** | **135.73 MiB** |

### File size histogram

| Size | Count |
|---|---|
| &lt;256 KiB | 56 |
| 256 KiB–1 MiB | 88 |
| 1–4 MiB | 77 |
| 4–16 MiB | 60 |
| 16–32 MiB | 11 |
| 32–64 MiB | 3 |
| 64–128 MiB | 0 |
| ≥128 MiB | 1 |

### Skew

- Largest file ≈ **12.0%** of all Codex bytes.
- Largest 10 files ≈ **34.2%**.
- Largest 25 files ≈ **52.4%**.
- **Four files exceed the 32 MiB per-file limit.**

### By age

| Age | Files | Size |
|---|---|---|
| &lt;1 day | 9 | 60.8 MiB |
| 1–7 days | 12 | 198.4 MiB |
| 7–30 days | 140 | 712.0 MiB |
| 30–90 days | 135 | 156.2 MiB |
| ≥90 days | 0 | 0 |

**Implication:** Codex cannot complete a full (or even naïve “week”) remote refresh
under current caps. Example from diagnosis: week-oriented selection still chose
**12 files ≈ 220 MiB**, including one **~142 MiB** file — above both 32 MiB/file and
128 MiB/refresh. Deadline often expires first; error was sometimes mislabeled
(`connection_failed` / historically `invalid_remote_data` via `"parse …"` wraps).

---

## Failure mechanics (pre- and post-partial fix)

### Historical loop (main bug #1)

1. `ApplySettings` / initial refresh often ran with **`since_ms=0`** before the UI
   sent its time window → widest possible collect.
2. Claude scan read tens of MB over SFTP (~70s observed).
3. Shared deadline killed or starved Codex.
4. Result marked `limited` / `partial_agent_data`.
5. Sessions **not cached** → `cached == nil`.
6. Next `/api/sessions` → `trigger=initial` again → **connecting** thrash.

### After partial-result fix (verified on coworker binary)

- State **`limited`**, not stuck **connecting**.
- **`cached=true`**; remote Claude sessions published (e.g. **40** saved).
- Automatic retry backed off (~**3 minutes**).
- Codex still fails as a **separate** limit/deadline problem.
- If the browser tab still said “connecting,” hard-refresh the tab after relaunch;
  backend had already transitioned.

### Empty window API bug (main bug #2)

- Source-aware `/api/sessions` left `Sessions` as Go `nil` when nothing matched.
- JSON: `"sessions": null`.
- UI decode required an array → “couldn’t load sessions from the API.”
- Fix: encode `sessions: []`; client treats null/missing as empty →
  “No sessions in this window.”

---

## Observability signals (useful log lines)

On `test/observability` (and PR #129), look for:

```text
remote.refresh phase=start … since_ms=…
remote.collect agent=claude|codex outcome=ok|error reason=… cand=… sel=… sessions=… bytes=… duration_ms=…
remote.refresh phase=complete … outcome=limited|ok cached=true|false by_agent=… coverage=…
```

Interpretation cheat sheet:

| Signal | Meaning |
|---|---|
| `cached=false` + `sessions=N>0` + `limited` | Old bug: collected but not published |
| `cached=true` + `limited` + `by_agent=claude=N` | Partial success working |
| `codex:…reason=refresh_timeout` | Deadline |
| `codex` fail with huge `bytes` / oversize | Hit file or total byte limit |
| `since_ms=0` on first refresh | Collect started before UI window |
| `sessions: null` (wire) | Empty-window encode bug (fixed) |

Coworker log sample patterns (older build):
`coverage=claude:cand=12,sel=12|0;codex:cand=0,sel=0,err=true`,
`round_trip_ms≈85000–92000`, `trigger=initial` every ~2 minutes.

---

## Fixes already landed (PRs #128 / #129)

1. **Publish partial agent success** — cache/show Claude (or Codex) when the other fails; state `limited`.
2. **Backoff on limited** — set `nextRetryAt`; stop immediate rescan loop.
3. **Parallel Claude ∥ Codex** collect on one SFTP client.
4. **Deadline 90s → 3m**; improve timeout classification (`Canceled` / deadline strings);
   avoid `"parse …"` wraps that mislabeled timeouts as invalid data.
5. **Limited sessions not `displayStale`** (still ineligible for aggregates).
6. **Empty `sessions: []`** encode + tolerant UI decode.

---

## Next design steps (priority)

Ordered for this workload (Claude ~93 MiB finishable; Codex ~1.1 GiB not):

### A. Stop impossible / wasteful work

1. **Don’t start remote collection in `ApplySettings`** — wait for first `ListView`
   so `since_ms` matches the UI window (e.g. This week), not `0`.
2. **Skip oversized transcripts; keep valid Codex sessions** — one 136 MiB file must
   not fail the whole agent; soft-skip over `MaxFileBytes` like local skip-and-continue.
3. **Preserve deadline errors as `refresh_timeout`** end-to-end (still seeing
   `connection_failed` in some post-deadline EOF paths).
4. **Reduce repeated SFTP lstat/realpath** — validate/allowlist path is RTT-heavy
   on hundreds of files.

### B. Local-like ongoing UX

5. **Incremental remote refresh** — use fingerprints/mtimes; only re-fetch changed
   files after first snapshot. Re-reading ~1 GB over SFTP will never feel local.
6. **Soft-fail missing/empty Codex tree** — empty coverage, not partial failure, on
   Claude-only hosts.
7. **Keep showing last good view** while refreshing (“updating…”), avoid connecting
   flash when cache exists.

### C. Product policy (optional)

8. Default remote window tighter until incremental exists.
9. Metadata-first / lazy full transcript for huge rollouts.
10. Per-file best-effort parse for corrupt JSONL (helps bad files; does **not** fix
    oversize/`cand=0` discovery failures).

### Explicitly lower priority

- Dual SFTP channels / raw bandwidth tricks before skip-oversize + window-first +
  incremental.
- Raising `MaxTotalBytes` enough to “fit” 220 MiB weeks without skip — still blocked
  by 32 MiB/file and one 142 MiB transcript.

---

## Design north star

Remote should match **local failure semantics** more than local wall-clock:

- Soft-fail per agent and per file where possible.
- Always leave the user with a usable board when any agent succeeded.
- First paint may be slow over SSH; **repeat visits** should be incremental.

For agent-box-class hosts: **smart degrade** (window + skip giants + cache +
incremental), not “download the full Codex corpus every refresh.”

---

## Quick reference paths

| Area | Path |
|---|---|
| Deadline / byte caps | `collector/internal/remote/limits.go` |
| Refresh / limited publish / parallel collect | `collector/internal/remote/manager.go` |
| SFTP allowlist + per-op validate | `collector/internal/remote/source.go` |
| Session open / deadline context | `collector/internal/remote/sftp.go` |
| Empty sessions encode | `collector/cmd/coslash/api.go` (`handleList`) |
| Empty sessions decode | `frontend/src/pages/coslash/hooks/use-sessions.ts` |
| Host strip copy | `frontend/src/pages/coslash/lib/host-strip.ts` |
| Debug logging notes | `docs/debugging.md` |
