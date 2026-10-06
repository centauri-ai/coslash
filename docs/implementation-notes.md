# Implementation notes

This file holds context that the source tree does not show: who owns what, which layer can do what, and which shortcuts are deliberate. For the decisions behind these rules, read [`docs/decisions/README.md`](decisions/README.md). Do not copy a compromise below as a pattern for new code.

## Source layout

Package and directory level only. Each line names what the unit owns.

### Collector commands (`collector/cmd`)

- `coslash` - the local binary: HTTP server, API routes, CLI subcommands (`sessions`, `send`, `review`, `doctor`, and more), startup, token, and runtime discovery.
- `coslash-helper` - the Linux SSH collection helper entry point. It is embedded in release builds.

### Collector internal packages (`collector/internal`)

- `agentexec` - child-process environment for launched agents, including removal of parent session markers and Windows shims.
- `collector` - local session collection across all vendor packages and composition with remote facts.
- `diagnostics` - content-free readiness checks for `coslash doctor` and the diagnostics dialog.
- `directedhandoff` - store and state tracking for a handoff addressed to a target session.
- `fullsessionexport` - client envelope for the additive full-session v2 upload.
- `fullsessionrecord` - adapter from the private parser model to the public `full-session-record/v1`.
- `grokcli` - Grok CLI location and private Grok home preparation.
- `handoff` - canonical Markdown context passed between agent sessions.
- `httpsec` - the loopback request guard and security headers.
- `hubclient` - Hub discovery, pairing, destination, upload, status, and credential storage.
- `launch` - terminal launches (resume, start fresh, send) and headless review processes per OS.
- `remote` - the Mac side of SSH collection: manager, helper lifecycle, SFTP fallback, cache, and health.
- `remotefacts` - transport-independent remote family facts used by composition.
- `remotehelper` - the Linux-side collector logic that the helper runs.
- `remoteinstall` - one-time helper activation on the remote host.
- `remoteprotocol` - the bounded NDJSON collection protocol and its validation.
- `review` - review requests, keys, and prompt construction for local and remote reviews.
- `session` - the private session model, exact detail, local revisions, and file-change IDs.
- `sessionbackupproducer` - frozen complete Codex backups and restart-safe spools.
- `sessionexport` - allow-listed mapping from the private model to `session-snapshot/v1`.
- `sessionpreview` - canonical snapshot bytes for preview and upload.
- `settings` - `settings.json` schema, validation, defaults, and SSH destination parsing.
- `synthesis` - synthesis scheduling, prompt construction, CLI runners, results, and cost accounting.
- `vendors` - shared source scanning, plus one subpackage per agent: `claude`, `codex`, `cursor`, `grok`, `opencode`, `pi`. Each subpackage owns read-only parsing, discovery, and liveness for that agent. Vendor behavior notes are in [`docs/vendors/`](vendors/).
- `web` - serves the frontend assets embedded in the binary.
- `windowsprivate`, `winfolders`, `winprocess` - Windows-only private file access, known folders, and process queries.
- `windowstest` - Windows ACL helpers for tests.

### Collector public contracts

- `collector/snapshot/v1` - `session-snapshot/v1`, the metadata-only portable record.
- `collector/fullsession/v1` - `full-session-record/v1`, one complete parsed revision.
- `collector/sessionbackup/v1` - `session-backup/v1`, one complete logical family backup.
- `collector/scripts` - smoke tests and the model price table filter.

### Frontend (`frontend/src`)

- `components/ui` - shared UI primitives (Radix and shadcn based).
- `lib` - app-wide helpers (theme, class names).
- `pages/coslash` - the coSlash app: `components` for views and dialogs, `features/sharing` and `features/full-sharing` for share flows, `hooks` for data loading, and `lib` for product logic (identity, grouping, search, API client).
- `styles` - base stylesheet and design tokens.

### Plugins (`plugins/coslash`)

- The Claude Code and Codex plugin manifests and the `skills` (`doctor`, `handoff`, `review`, `send`, `sessions`). The skills call the `coslash` CLI.

## Boundary rules

Area rules are in [`collector/AGENTS.md`](../collector/AGENTS.md) and [`frontend/AGENTS.md`](../frontend/AGENTS.md). The rules below cover the boundaries between layers.

- Vendor packages only read agent data. They never repair, migrate, or write vendor files. The Pi parser avoids the Pi loader for this reason. The exceptions are the managed OpenCode plugin and Pi extension, which coSlash installs into vendor configuration directories.
- `session.Session` is not a wire type. Data that leaves the process for Hub or for a portable record goes through `sessionexport`, `fullsessionrecord`, or `sessionbackupproducer`. The Hub client never parses transcripts and never re-serializes reviewed bytes.
- The collector enforces privacy, eligibility, and consent. The frontend shows API-owned labels and states. It never decides what is safe to share.
- The Mac owns settings, cache, composition, and health for remote sources. The helper keeps nothing between runs. `remotefacts` stays separate from the SSH protocol so composition does not depend on the transport.
- `cmd/coslash` is the only place that registers HTTP routes. Every route goes through `httpsec`.
- Windows-only system calls stay in the `win*` and `windowsprivate` packages or in `_windows.go` files.
- Plugin skills parse the output of the `coslash` CLI. A change to CLI output or flags is a contract change for the skills.

## Intentional compromises

Each item names the trigger that justifies a real fix.

- **Oversized handoffs drop only the timeline.** When a handoff exceeds 64 KiB, coSlash omits the timeline. Other oversized sections still fail at launch (`ponytail:` comment in `collector/internal/handoff` and `frontend/src/pages/coslash/lib/handoff.ts`, 741b3a39). Upgrade trigger: a report of a handoff that still fails after timeline omission.
- **Local Codex handoffs reach Codex through argv.** The local launch expands the handoff file into `-c developer_instructions=...` on macOS and Windows. The remote fix (27a5495d) moved remote launches to a private profile file and did not change local launches. The JSON-encoded handoff can be several times the 64 KiB input limit. Upgrade trigger: a failed large local Codex handoff, or a need to hide handoffs from other local users. The remote profile-file approach in `collector/internal/launch` is the reference fix.
- **One SSH source.** The first release supports one remote host. Upgrade trigger: a confirmed need for more than one remote host. [`docs/current-product-baseline.md`](current-product-baseline.md) lists this as deferred.
- **SSH destination is an alias or `user@host` only.** Ports, IPv6, and ProxyJump stay in the SSH configuration of the user (comment in `collector/internal/settings`). Upgrade trigger: users who cannot express a host in their SSH configuration.
- **Portable snapshot v1 is metadata-only.** Titles, summaries, digests, todos, and commit subjects stay on the device until a separately reviewed product contract adds safe derived fields (comment in `collector/internal/sessionexport`, eb6e2ca5).
- **Complete backup v1 is Codex-only.** Other agents return `complete_backup_unsupported` (`collector/internal/sessionbackupproducer/README.md`). Pi portable sharing waits until the formats can represent its branch context and accounting ([`docs/data-and-privacy.md`](data-and-privacy.md)).
- **Legacy routes and caches remain.** `GET /api/hub/full-session-preview`, `POST /api/hub/full-session-shares`, the path-based `/api/diff` form, and `remotes/<source-id>/snapshot.json` stay for older clients ([`docs/current-product-baseline.md`](current-product-baseline.md)). The normal UI does not use them.
- **Claude fork usage ties keep usage on both sides.** When ownership of forked rows cannot be resolved, coSlash counts the usage twice instead of dropping tokens (comment in `collector/internal/vendors/claude`). Upgrade trigger: Claude Code records fork ownership.
- **Untested Pi releases stay enabled.** Diagnostics warn for a stable Pi release that is not in the tested list, but runtime integration and launch continue (`collector/internal/diagnostics`). Add a release to the list after a regression pass on that release.
- **Remove leaves the remote helper installed.** Remove deletes local configuration and cache even when the host is offline ([`docs/data-and-privacy.md`](data-and-privacy.md)).
- **Grok reviews on Windows have no filesystem boundary.** The Grok sandbox does not enforce one on Windows. coSlash restricts tools and warns the user ([`docs/data-and-privacy.md`](data-and-privacy.md)). Upgrade trigger: a Grok release that enforces the sandbox on Windows.
- **Accounting loss is not always detectable.** An existing zero-byte accounting database initializes as a new store without an unavailable response ([`docs/current-product-baseline.md`](current-product-baseline.md)).
