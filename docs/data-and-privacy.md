# Data and privacy

coSlash runs locally, but agent transcripts can contain prompts, source code, command output, credentials, and other secrets. Review these boundaries before use.

## Local access

coSlash reads, but does not modify:

- Claude Code, Codex, and OpenCode transcripts and their local session metadata.
- Codex rollouts under `~/.codex/sessions` and
  `~/.codex/archived_sessions`, plus matching names from
  `~/.codex/session_index.jsonl`. If a member exists in both rollout trees, the
  active copy is used.
- Cursor IDE and CLI transcripts plus their local read-only metadata stores:
  `~/.cursor/projects`, `~/.cursor/chats`, `~/.cursor/ai-tracking`, and
  the platform's Cursor global-storage directory (`~/Library/Application Support/Cursor/User/globalStorage`
  on macOS or `%APPDATA%\Cursor\User\globalStorage` on Windows).
- Local Pi schema-3 transcripts under `~/.pi/agent/sessions`, configured session roots, and exact paths retained by the managed runtime integration. Pi remote collection is unsupported.
- Recorded working directories and Git metadata used for branch and change summaries.
- Local process information used to identify live sessions.

On macOS, background enrichment does not open project files in Desktop, Documents, Downloads, iCloud Drive, or mounted volumes. Transcript discovery and sync continue, but repository and file timestamp details may use fallback values or be absent for those sessions.

`COSLASH_HOME` sets the storage root and defaults to `~/.coslash`:

| Path | Contents |
| --- | --- |
| `settings.json` | Synthesis, appearance, and terminal preferences. |
| `token` | Access token for the current server process. |
| `summaries/` | Cached synthesis results. |
| `synthesis/` | Temporary synthesis files and isolated CLI data. |
| `sys-prompts/` | Temporary handoffs for fresh sessions. |
| `pi-runtime/` and `pi-history/` | Private process identity, session IDs, exact transcript paths, runtime state, and retained discovery evidence. |
| `remotes/<source-id>/snapshot.json` | Legacy normalized remote session cards. |
| `remotes/<source-id>/snapshot-v2.json` and `snapshot-v2.previous.json` | Current and previous atomic remote generations. They contain normalized facts and complete supported Claude and Codex parsed records, including prompts, commands, working directories, subagent detail, edited-file paths, and file-change bodies. They do not contain raw transcript rows. |
| `session-backups/prepared/` | Verified, private session-family bundles retained locally while a V4 sync can resume. |
| `fingerprints/v1/<agent>/` | The local parse cache: one private file per local transcript, Cursor session or OpenCode family holding the parsed summary already produced for the board (prompts, commands, working directories, edited-file paths and file-change bodies), keyed by file size and modification time so an unchanged source is never parsed twice. It holds no raw transcript rows. Deleting the directory is always safe; `discovery-cursor.json` beside it records where a streamed discovery pass stopped. `COSLASH_SCALE_IMPORT=0` disables the cache. |
| `sync-v4/queue.json` | Private v4 installation ID, session metadata, content hashes, pending upload IDs and progress. This ID survives device re-pairing. |

coSlash restricts storage to the current account (`0700`/`0600` modes on
macOS and a protected current-user ACL on Windows). Programs running as that
same account can still read it.

The managed Pi extension is installed under `PI_CODING_AGENT_DIR/extensions` (default `~/.pi/agent/extensions`). Installation preserves settings and other extensions and refuses unmanaged replacement. Restart Pi to activate updates. Removing its runtime/history directories removes retained evidence; removing the managed extension disables reliable Pi live status. Transcript paths remain backend-only and are excluded from public session JSON. Pi portable sharing is unavailable until the formats can represent its branch context and accounting.

## Optional SSH/SFTP access and helper installation

When a remote machine is added, the system `ssh` client uses the saved
OpenSSH alias or simple `user@host` destination and requests SFTP. If SSH needs
interactive authentication, the user explicitly completes its native Terminal
prompt; credentials and prompt text never enter coSlash. The setup action
explicitly authorizes the first optional collector-helper installation. coSlash
verifies the embedded helper's digest and platform before uploading a versioned
executable to `~/.coslash/helpers/<version>/coslash-helper`, owned by the SSH
user with mode `0700`. Later coSlash releases may automatically replace only
that previously verified helper. The helper has no root or network access, reads
only the fixed allowlist below, and streams bounded normalized facts back to the
local machine. No path, command, prompt, transcript row, cache, or handoff supplied by
the local machine can choose files the helper opens.

Builds without authenticated embedded helper assets disable the install action
explicitly. They continue using SFTP and never upload an unverified helper.

Both collection paths may read these paths beneath the SSH user's home:

- `.claude/projects`, `.claude/sessions`, and `.claude/jobs`;
- `.codex/sessions`, `.codex/archived_sessions`, and
  `.codex/session_index.jsonl`;
- for Claude liveness, a numeric `/proc/<pid>` entry only to validate a PID
  already present in Claude metadata;
- for Codex liveness, the list of rollout files that local Codex processes have
  open, from `lsof -a -c codex -Fn`. The SFTP fallback filters this list on the
  remote host so only Codex rollout file paths cross SSH. It does not contact the
  network;
- the Git configuration that `git remote get-url origin` reads, only in a
  working directory that a collected session recorded with a branch. The Mac
  runs this over SSH, not the helper, for at most 64 directories per refresh.
  The directory may be outside the home directory. The raw origin crosses SSH
  transiently; the Mac canonicalizes it before caching or exposure.

The SFTP interface has no write, delete, rename, or chmod operation. It rejects
symlinks and canonical paths outside the allowlist. Current ceilings are 32 MiB
per file, 128 MiB per refresh, 2,000 candidate files per agent, 10,000 directory
entries, depth 16, and three minutes per refresh. Raw transcript bytes stay in
bounded local memory only while parsing. For the supported Codex path, complete
parsed product data—including prompts, commands, edited-file paths, working
directories, subagent detail, and file-change bodies—is transferred and cached
locally so exact detail remains available after restart or an SSH outage. Claude
parsed exact details now cross SSH into the private local cache just like Codex;
raw transcript rows do not. Exact-detail caching, synthesis, and V4 sync are
separate capabilities. The versioned local session-family producer remains as
input to V4 sync; Local's manual V3 complete-backup share flow has been removed.
The cache excludes raw transcript rows, SSH configuration, coSlash credentials,
sockets, and environment values.

The local web app requests inspector data by opaque source, agent, session, and
revision identities. A supported remote revision is a SHA-256 fingerprint of
the complete parsed record. A local revision is a SHA-256 fingerprint of parsed
transcript content. It excludes separately refreshed process, repository, Git,
filesystem, review, synthesis, and subagent-status fields that the inspector
overlays or loads separately. It also excludes collection-time activity used
only when a source has no usable timestamp. File-change bodies are requested
only by opaque change IDs that the collector verifies belong to the selected
revision; display paths and change IDs are never treated as files to open.

## Outbound data

The collector does not upload session data just because it reads a local
transcript. When a paired device's Hub policy allows it, personal V4 sync sends
session data to Hub; the per-computer pause stops uploads and retries. Local no
longer offers the manual V3 complete-backup Share to Hub action. A
separately authorized remote MCP agent may read Hub sessions. If you enable
synthesis, it passes a bounded set of facts
to your selected local CLI. These facts can include prompts, recaps, todos,
filenames, commands, and commit text. Supported CLIs are Claude Code, Codex,
OpenCode, and Cursor. The CLI uses its existing authentication. The selected
provider's settings and terms apply.

OpenCode has no ephemeral mode, so coSlash points each run at its own scratch database under `~/.coslash/synthesis`, discarded once the run ends. Synthesis runs never enter your own OpenCode history.

Cursor synthesis uses read-only ask mode. coSlash also disables file, shell, write, web, and MCP tools for the run. Each run uses a temporary data directory under `~/.coslash/synthesis`. coSlash removes the directory after the run. At startup, coSlash removes abandoned synthesis directories that are more than one hour old. Synthesis chats do not enter your Cursor history.

Resume and Start fresh launch your installed agent CLI. Its later network and data behavior is governed by that tool.

Sending a session to Cursor CLI copies its handoff and optional task to the system clipboard before opening Cursor. Clipboard history tools and cross-device clipboard sync may retain that content. Paste it into the new Cursor session, then clear the clipboard if needed.

Remote Hub MCP is an optional agent connection. The agent sends tool questions
and arguments directly to the Hub MCP endpoint after browser OAuth approval;
coSlash Local does not copy the bearer token into its settings or process
arguments. Each agent manages its own credential storage. Disconnect or switch
the connection in that agent to clear its local OAuth credential; the Hub
checks access on each tool request and denies revoked access.

Experimental personal v4 sync of Codex, local Claude, OpenCode and Cursor
sessions starts by default when Local pairs with Hub or starts with a stored
credential. The first check-in advertises sync capabilities and reports the
normalized Local version, install channel, operating system, and content-free
queue state. A new owner's default Hub policy (version 0) is enough to start.
Each check-in reports which agents this install has sessions for. It sends
recent session metadata before content, then imports older history. The Hub
check-in supplies the current pause, device-off and leave-out policy; if
check-in fails or the policy becomes stale, upload stops. A Hub `device_revoked`
response removes the stored credential and marks this computer disconnected.
`COSLASH_V4_SYNC=0` disables Local auto-sync and stops advertising `sync-v4`;
`COSLASH_SYNC_POLICY=0` stops advertising `sync-policy/1`. Both default on.
`COSLASH_SYNC_METERED=1` and
`COSLASH_SYNC_OFFLINE=1` stop uploads locally. On macOS, low battery uses
`pmset`; on Linux, metered NetworkManager connections and discharging battery
levels are checked when available. `COSLASH_SYNC_BATTERY_PERCENT` is a local
override for testing. The per-computer pause setting also stops uploads and
retries until resumed. Local has no manual complete-backup action that bypasses
this pause; automatic personal sync does not enable team sharing.

Hub-led device setup sends Local an opaque, expiring launch intent through the
`coslash:` app handoff. Local claims that intent with Hub, then uses the
existing device-authorization poll and saves the returned device credential in
the operating-system keychain. The browser does not receive the device code,
device credential, or Local API access token. The initial check-in contains
device version, install channel, operating system, capabilities, and empty
content-free queue state; it does not upload a session or enable team sharing.
Hub manages device and sync policy; Local also offers a per-computer pause.

## Local preview and Hub sync

The `?team-preview=1` option exposes a clearly labeled, preview-only trigger in
session details. It shows a local preview and does not approve or upload a
session.

New session transfer from Local uses personal V4 sync under the paired device's
Hub policy and Local sync settings. Personal sync does not create a workspace
share link.

This client change removes Local's V3 complete-backup preview and upload path.
It does not change Hub V3 routes, stored backups, revisions, chunks, existing
workspace links, or their current URLs. No V3 data migration or deletion takes
place. The V3 server path remains a compatibility dependency until an owner
chooses a migration or sunset policy and the supported Local-client rollout
window has elapsed.

The local session-family producer and private spool remain in use by V4 sync.
This change does not redirect the V3 request or payload to a V4 endpoint and
does not change V4 upload selection. Issue 011's V4 artifact boundary is tracked
separately.

The library card for an SSH session carries the canonical origin remote when
`git remote get-url origin` succeeds on that host, and its live status from the
Claude or Codex liveness probe listed above. A missing or failed probe shows the
session as Inactive. The portable record itself still omits the origin. If no
bounded repository identity is available, the upload uses the disclosed
working-directory basename and marks the repository local-only; the review
shows that exact fallback before approval.
The source-aware session response may carry a recorded remote working-directory path when no repository identity is available; the library displays it relative to a recognizable home directory when possible, and it remains display metadata rather than raw transcript content.

## Local server

coSlash listens on IPv4 loopback and protects API requests with a new access
token on every start. It rejects unexpected hosts, origins, and cross-site
browser requests. The token is stored in `~/.coslash/token` with current-user
permissions, so other processes running as the same account can still read it
and access coSlash. Do not proxy or forward the port.

## Control and removal

Synthesis is off until you enable and save it in Settings. Disable it there to stop new requests, then delete `~/.coslash/summaries` to remove cached results.

Disabling a remote machine stops refreshes and hides its cards but retains its
normalized last-good cache and any optional helper; it does not change Linux.
**Remove** deletes the local host configuration and cache even if the host is
offline. It deliberately leaves any previously installed helper in place on the
remote machine.

To remove all coSlash data, quit coSlash and delete `~/.coslash` (or your `COSLASH_HOME`). `coslash doctor --json` and **Copy diagnostics** exclude transcript contents, prompts, and session names.
