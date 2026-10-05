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
| `session-backups/prepared/` | Verified, private complete Codex, local Claude, OpenCode and Cursor bundles retained while an approved share or enabled v4 sync can resume. |
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
raw transcript rows do not. Exact-detail caching, synthesis, and complete backup
sharing are separate capabilities, and complete backup v1 remains Codex-only.
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

Outside an explicitly approved Hub share or explicitly enabled experimental
v4 sync, the collector does not upload session data itself. A
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
sessions is disabled by default. A development build with a paired Hub, a
supported semantic Local version, and `COSLASH_V4_SYNC_ENABLED=1` starts it,
right after pairing: a new owner's default Hub policy (version 0) is enough.
Each check-in reports which agents this install has sessions for. It sends recent session metadata before
content, then imports older history. The Hub check-in supplies the current
pause, device-off and leave-out policy; if check-in fails or the policy becomes
stale, upload stops. `COSLASH_SYNC_METERED=1` and
`COSLASH_SYNC_OFFLINE=1` stop uploads locally. On macOS, low battery uses
`pmset`; on Linux, metered NetworkManager connections and discharging battery
levels are checked when available. `COSLASH_SYNC_BATTERY_PERCENT` is a local
override for testing. Disable the v4 flag and restart to stop automatic
sync; the existing explicit Share to Hub flow remains available.

Hub-led device setup sends Local an opaque, expiring launch intent through the
`coslash:` app handoff. Local claims that intent with Hub, then uses the
existing device-authorization poll and saves the returned device credential in
the operating-system keychain. The browser does not receive the device code,
device credential, or Local API access token. The initial check-in contains
device version, operating system, and empty content-free queue state; it does
not upload a session or enable team sharing. Device and sync controls remain
in Hub.

## Share preview and approval

During an active Share to Hub flow, **See what gets shared** builds a local
`session-backup/v1` bundle for each selected local or SSH Codex session family.
The frozen bundle contains the raw attributable Codex rollouts and sidecar
bytes, canonical parsed records, exact file-change bodies, session enrichment,
and revision-matched persisted synthesis. Previewing does not upload or approve
anything. For a local Codex session, preview first starts or waits for its
current Local AI debrief, including for short sessions skipped by automatic
background synthesis. Upload rejects a reviewed local bundle without that
revision's debrief, including a request from an older open tab. The v3 Share
flow continues to block Claude, Cursor, and OpenCode.
The separate v4 local sync can prepare complete local Claude, OpenCode and
Cursor families when enabled; Claude SSH remains unsupported there. Neither flow falls
back to a metadata-only upload.

For opt-in user testing before the Team flow ships, append
`?team-preview=1` to the local coSlash URL. This reveals a clearly labeled
preview-only trigger in session details; it does not enable a Team workspace,
approval, or upload.

The review shows artifact counts by class, the exact total byte count and
complete-backup SHA-256, the Hub's advertised capacity, and the paired
destination and audience. Complete backups are not redacted. Raw prompts,
commands, tool output, paths, environment fragments, and file bodies may
contain credentials or other secrets and become visible to active members of
the destination workspace after acceptance.

The fixture-backed Share flow is available to source builds with
`?team-share=1`. It exercises eligibility, destination, selection, exact review,
partial retry, and Hub route states, but is labeled **NO UPLOAD** and never
contacts a cloud service. Use `&share-state=signed_out`, `pairing_required`,
`credential_dormant`, or `credential_revoked` to inspect eligibility states,
and `&share-result=partial` to inspect retry.

Approval binds the frozen source revision, complete hash and byte count,
destination workspace and name, audience version and member count, server
identity, and advertised per-backup, chunk, and workspace capacities. A changed
source, destination, audience, manifest, or capacity assertion requires a new
review. Uploads use bounded verified chunks and reconcile server status with
the same idempotency key. Frozen bundles and their upload identities survive a
dialog or app restart, so retry reads only the approved spool and never uses
changed source bytes as upload content. Accepted batch items remain accepted
while eligible failed items resume only missing chunks.

The older metadata snapshot and single-request full-v2 protocols remain in the
client for compatibility, but the normal **Share to Hub** action does not use
them. A Hub without v3 complete-backup support shows an update requirement and
receives no downgraded payload.

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
