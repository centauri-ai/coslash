# coSlash local product baseline — 2026-09-11

This is the implementation inventory for the local coSlash application at the
start of the next product iteration. It describes shipped code, not future
intent. The accepted live-beta source was
`df985b42beb441ab615b312842b6e3dee51a4b5f` (`v0.0.3-rc.6`). The new baseline
also contains the two maintenance commits that were already on `origin/main`.

## Product capabilities

- Discover and normalize local Claude Code, Codex, and OpenCode sessions.
- Present list and board views with repository, branch, source, status, time,
  token, cost, context, compaction, working-tree, and resume-readiness facts.
- Inspect a session timeline, artifacts, commits, commands, todos, model usage,
  subagents, and deterministic debrief.
- Resume the original session, start a fresh agent with a generated handoff, or
  copy the handoff.
- Optionally improve local debriefs through the installed Claude Code, Codex,
  or OpenCode CLI. Synthesis is off by default and uses bounded derived facts.
- Configure theme, terminal, synthesis provider/model, and one SSH source.
- Install and manage a bounded Linux helper for an SSH source; report honest
  health, staleness, retry, and incomplete-coverage states.
- Discover local and SSH sessions in one source-aware library while keeping
  host names, user names, absolute paths, prompts, commands, and transcripts
  out of the browser source summary.
- Pair with a configured coSlash Hub, freeze the complete local/SSH Codex
  session-family backup, review its inventory/hash/bytes/capacity and the
  destination audience, and explicitly share one session or a bounded batch
  through the v3 Share flow. The separate personal v4 sync follows Hub device
  policy and does not enable team sharing or standing team auto-share rules.
- Open the server-confirmed Hub route after an accepted upload and preserve
  idempotent retry behavior.

## Local architecture and storage

| Area | Implementation |
| --- | --- |
| Collector/API | Go module in `collector`; loopback HTTP server with a startup access-token guard |
| UI | React, TypeScript, Vite, Tailwind, and Radix primitives in `frontend` |
| Session sources | Read-only parsers for Claude Code, Codex, and OpenCode local data |
| Remote source | SSH manager plus versioned `coslash-helper` for Linux amd64/arm64 |
| Local state | `~/.coslash/settings.json`, cached derived summaries, temporary handoffs, normalized remote facts, and private v4 sync queue state |
| Hub credential | OS keychain entry scoped to the configured Hub host |
| Raw source policy | Local and remote transcripts are read-only; an explicitly prepared complete backup persists exact attributable bytes in a private retry spool until discarded |

## Local HTTP API

All routes are loopback-only and protected by the process access-token guard.

| Method and route | Function |
| --- | --- |
| `GET /api/sessions` | Local sessions; `sourceAware=1` adds safe local/SSH source facts |
| `GET /api/synthesis` | Read or request the selected local session synthesis |
| `POST /api/hub/share-synthesis` | Check or start a selected Local session's revision-bound debrief before complete-backup review; report pending, consent, eligibility, and failure states |
| `GET /api/session-detail?source=…&agent=…&session=…&revision=…` | Read an exact local or cached SSH session revision |
| `GET /api/diff?id=…&path=…` | Read one local session file diff (legacy path-based form) |
| `GET /api/diff?source=…&agent=…&session=…&revision=…&change=…` | Read selected changes from an exact local or cached SSH session revision; repeat `change` to select multiple changes |
| `GET /api/share-preview` | Build the exact privacy-bounded Hub preview for a local or SSH revision |
| `GET /api/settings` | Read validated settings and backend/model options |
| `PUT /api/settings` | Save settings and optional remote-helper ownership action |
| `POST /api/launch` | Resume or start fresh with a handoff in the configured terminal/agent |
| `POST /api/reviews` | Start a local session review with a selected installed agent |
| `GET /api/handoff` | Render canonical Markdown for one local session |
| `POST /api/send` | Start Claude Code or Codex with a local session handoff and optional initial task |
| `POST /api/remote/test` | Validate the configured SSH source |
| `POST /api/remote/retry` | Retry remote collection |
| `GET /api/remote/status` | Read sanitized remote/helper health |
| `GET /api/sync/status` | Read content-free Hub sync state and per-session membership, protected by the Local access token |
| `POST /api/remote/helper/setup` | Install or update the managed remote helper |
| `GET /api/diagnostics` | Read content-free build/source/CLI/storage diagnostics |
| `GET /api/hub/destination` | Read pairing and destination readiness |
| `POST /api/hub/pairings` | Start device pairing |
| `POST /api/hub/pairings/{id}/poll` | Complete pairing and store the device credential |
| `POST /api/hub/onboarding/activate` | Consume a Hub launch intent through the token-guarded Local API and start pairing |
| `POST /api/hub/onboarding/check-in` | Retry a content-free Hub device check-in from a user-clicked Hub recovery link |
| `POST /api/hub/backup-previews` | Freeze and review a complete `session-backup/v1` bundle |
| `POST /api/hub/shares` | Resumably upload an explicitly approved complete backup through v3 |

The legacy `GET /api/hub/full-session-preview` and
`POST /api/hub/full-session-shares` compatibility routes remain available to
older clients, but there is no separate full-revision action in the normal UI.

## Contracts and privacy boundary

- `collector/snapshot/v1`: canonical `session-snapshot/v1` schema and fixtures.
- `collector/internal/sessionpreview`: exact review document presented before
  an upload.
- `collector/internal/sessionexport`: allow-listed metadata-only serialization.
- `collector/sessionbackup/v1`: canonical complete logical-family manifest,
  artifact taxonomy, hashing, fixtures, and verifier.
- `collector/internal/sessionbackupproducer`: frozen local/SSH Codex bundle
  production and restart-safe spools.
- `collector/internal/hubclient`: discovery, manual and Hub-led pairing,
  content-free device check-in, destination, upload, status, and canonical Hub
  handoff client.
- `collector/cmd/coslash/protocol_*.go`: per-user macOS Launch Services and
  Windows `HKCU` registration for the validated `coslash:` activation URL.
- At the 2026-09-11 baseline, local-only categories included raw
  prompts/transcripts, summaries/goals, commands, todos, detailed subagent
  content, commit subjects, diffs, and absolute paths. C03 preserves that
  boundary for v1 sharing, but a separately approved full-v2 SSH Codex revision
  includes the parsed prompts, commands, todos, subagent content, paths, and
  file-change bodies shown in its complete review. Raw transcript rows remain
  local. Previewing either flow does not upload anything.

## Settings and supported agent skills

The product has no independently installed "skill registry." Its reusable
agent-facing capabilities are session discovery, debrief synthesis, resume,
fresh-start handoff, copy handoff, and local/SSH collection for Claude Code,
Codex, and OpenCode. Backend/model options are defined in
`collector/internal/settings/settings.go`; arbitrary models are accepted only
when the selected local CLI can resolve them.

## Build and verification

Prerequisites are Go 1.26+ and Node 24+.

```sh
cd collector
make release       # staged frontend + embedded remote helpers + local binary
make test          # Go, OpenCode plugin, and embedded-helper tests
make check         # formatting and go vet
make dist          # reproducible darwin/arm64 and darwin/amd64 archives
./bin/coslash
```

Frontend-only development uses `npm --prefix frontend ci`, then `npm --prefix
frontend run dev`, `test`, or `build`. Release packaging is owned by
`collector/Makefile`; generated staged assets and helper binaries are removed
after their consumers finish.

Transition verification passed on 2026-09-11: the collector command, Hub
client, export, remote, settings, and vendor packages; 27 focused session
library/preview/sharing UI tests; and the production frontend build. The
frontend package audit reported zero vulnerabilities.

## Deferred, not implemented

- Automatic/continuous sharing and standing repository rules.
- More than one configured SSH source.
- A server-side product skill/plugin marketplace.
- General-availability compatibility or support promises.
- LB-09 production promotion; the accepted source is a non-production beta
  candidate until a later release decision is made.

## Cursor support addendum - 2026-09-17

Cursor support is local-only and covers Cursor IDE and Cursor CLI (`agent`)
sessions. coSlash reads Cursor transcripts and local metadata stores without
modifying them, exposes IDE and CLI installation health separately, and keeps
session families and subagents together. Cursor SDK sessions are excluded.

Cursor IDE exposes its current context occupancy separately; cumulative token
usage remains unavailable. Cursor CLI token and compaction data remain
unavailable when Cursor does not persist them reliably. Completed Cursor
assistant replies appear as timeline recaps, and the latest IDE conversation
summary can inform local synthesis.

## Hub sync runtime addendum - 2026-10-05

Local v4 personal sync starts by default after pairing and at startup when a
stored Hub credential exists. The Hub's device policy controls automatic
uploads; the Local settings pause stops uploads and retry work on this
computer. Local reports a content-free `/api/sync/status` response and
per-session `in_hub`, `syncing`, `not_in_hub`, or `left_out` states for the UI.
Hub device revocation deletes the stored credential and changes the Local
status to disconnected. `COSLASH_V4_SYNC=0` disables local v4 sync;
`COSLASH_SYNC_POLICY=0` suppresses the `sync-policy/1` capability.

## V3 client retirement addendum - 2026-10-09

The Local V3 complete-backup preview and Share to Hub action, its Local upload
routes, and the V3 upload caller have been removed. The local session-family
producer and spool remain in use by V4 sync; V4 upload selection is unchanged.

This client change does not remove Hub V3 endpoints, historical backup data, or
workspace links. No migration or purge has been applied. Server route removal
must follow an approved migrate-or-sunset policy and the owner-defined Local
client adoption window. A migration must preserve exact artifacts and existing
link resolution; a sunset must define notice, end date, old-link behavior, and
data-deletion timing.
