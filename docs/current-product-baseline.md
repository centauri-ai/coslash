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
- Pair with a configured coSlash Hub, preview the exact bounded outgoing
  snapshot, review destination/audience/revision/exclusions, and explicitly
  share one session or a bounded batch. There is no standing auto-share rule.
- Open the server-confirmed Hub route after an accepted upload and preserve
  idempotent retry behavior.

## Local architecture and storage

| Area | Implementation |
| --- | --- |
| Collector/API | Go module in `collector`; loopback HTTP server with a startup access-token guard |
| UI | React, TypeScript, Vite, Tailwind, and Radix primitives in `frontend` |
| Session sources | Read-only parsers for Claude Code, Codex, and OpenCode local data |
| Remote source | SSH manager plus versioned `coslash-helper` for Linux amd64/arm64 |
| Local state | `~/.coslash/settings.json`, cached derived summaries, temporary handoffs, and normalized remote facts |
| Hub credential | OS keychain entry scoped to the configured Hub host |
| Raw source policy | Local and remote transcripts are read-only; raw remote transcript bytes are not persisted |

## Local HTTP API

All routes are loopback-only and protected by the process access-token guard.

| Method and route | Function |
| --- | --- |
| `GET /api/sessions` | Local sessions; `sourceAware=1` adds safe local/SSH source facts |
| `GET /api/synthesis` | Read or request the selected local session synthesis |
| `GET /api/diff` | Read one local session file diff |
| `GET /api/share-preview` | Build the exact privacy-bounded Hub preview for a local or SSH revision |
| `GET /api/settings` | Read validated settings and backend/model options |
| `PUT /api/settings` | Save settings and optional remote-helper ownership action |
| `POST /api/launch` | Resume or start fresh with a handoff in the configured terminal/agent |
| `POST /api/remote/test` | Validate the configured SSH source |
| `POST /api/remote/retry` | Retry remote collection |
| `GET /api/remote/status` | Read sanitized remote/helper health |
| `POST /api/remote/helper/setup` | Install or update the managed remote helper |
| `GET /api/diagnostics` | Read content-free build/source/CLI/storage diagnostics |
| `GET /api/hub/destination` | Read pairing and destination readiness |
| `POST /api/hub/pairings` | Start device pairing |
| `POST /api/hub/pairings/{id}/poll` | Complete pairing and store the device credential |
| `POST /api/hub/shares` | Submit an explicitly approved `hub-share/v1` request |

## Contracts and privacy boundary

- `collector/snapshot/v1`: canonical `session-snapshot/v1` schema and fixtures.
- `collector/internal/sessionpreview`: exact review document presented before
  an upload.
- `collector/internal/sessionexport`: allow-listed metadata-only serialization.
- `collector/internal/hubclient`: discovery, pairing, destination, upload,
  status, and canonical Hub handoff client.
- Local-only categories include raw prompts/transcripts, summaries/goals,
  commands, todos, detailed subagent content, commit subjects, diffs, and
  absolute paths. The UI must not imply that previewing uploads anything.

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
make test          # Go tests plus embedded-helper check
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
