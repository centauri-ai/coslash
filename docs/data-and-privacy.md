# Data and privacy

coSlash runs locally, but agent transcripts can contain prompts, source code, command output, credentials, and other secrets. Review these boundaries before use.

## Local access

coSlash reads, but does not modify:

- Claude Code, Codex, and OpenCode transcripts and their local session metadata.
- Cursor IDE and CLI transcripts plus their local read-only metadata stores:
  `~/.cursor/projects`, `~/.cursor/chats`, `~/.cursor/ai-tracking`, and
  `~/Library/Application Support/Cursor/User/globalStorage`.
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
| `remotes/<source-id>/snapshot.json` | Legacy normalized remote session cards. |
| `remotes/<source-id>/snapshot-v2.json` and `snapshot-v2.previous.json` | Current and previous atomic remote generations. They contain normalized facts and complete supported Codex parsed records, including prompts, commands, working directories, subagent detail, edited-file paths, and file-change bodies. They do not contain raw transcript rows. |

coSlash creates the storage directory with mode `0700` and persistent files with mode `0600`. Programs running as your macOS user can still read them.

## Optional SSH/SFTP access and helper installation

When a remote machine is added, the Mac's system `ssh` client uses the saved
OpenSSH alias or simple `user@host` destination and requests SFTP. If SSH needs
interactive authentication, the user explicitly completes its native Terminal
prompt; credentials and prompt text never enter coSlash. The setup action
explicitly authorizes the first optional collector-helper installation. coSlash
verifies the embedded helper's digest and platform before uploading a versioned
executable to `~/.coslash/helpers/<version>/coslash-helper`, owned by the SSH
user with mode `0700`. Later coSlash releases may automatically replace only
that previously verified helper. The helper has no root or network access, reads
only the fixed allowlist below, and streams bounded normalized facts back to the
Mac. No path, command, prompt, transcript row, cache, or handoff supplied by
the Mac can choose files the helper opens.

Builds without authenticated embedded helper assets disable the install action
explicitly. They continue using SFTP and never upload an unverified helper.

Both collection paths may read these paths beneath the SSH user's home:

- `.claude/projects`, `.claude/sessions`, and `.claude/jobs`;
- `.codex/sessions`, `.codex/archived_sessions`, and
  `.codex/session_index.jsonl`;
- a numeric `/proc/<pid>` entry only to validate a PID already present in Claude
  metadata;
- the Git configuration that `git remote get-url origin` reads, only in a
  working directory that a collected session recorded with a branch. The Mac
  runs this over SSH, not the helper, for at most 64 directories per refresh.
  The directory may be outside the home directory. The raw origin crosses SSH
  transiently; the Mac canonicalizes it before caching or exposure.

The SFTP interface has no write, delete, rename, or chmod operation. It rejects
symlinks and canonical paths outside the allowlist. Current ceilings are 32 MiB
per file, 128 MiB per refresh, 2,000 candidate files per agent, 10,000 directory
entries, depth 16, and three minutes per refresh. Raw transcript bytes stay in
bounded Mac memory only while parsing. For the supported Codex path, complete
parsed product data—including prompts, commands, edited-file paths, working
directories, subagent detail, and file-change bodies—is transferred and cached
locally so exact detail remains available after restart or an SSH outage. The
cache excludes raw transcript rows, SSH configuration, coSlash credentials,
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

Outside an explicitly approved Hub share, the collector does not upload
session data itself. If you enable synthesis, it passes a bounded set of facts
to your selected local CLI. These facts can include prompts, recaps, todos,
filenames, commands, and commit text. Supported CLIs are Claude Code, Codex,
OpenCode, and Cursor. The CLI uses its existing authentication. The selected
provider's settings and terms apply.

OpenCode has no ephemeral mode, so coSlash points each run at its own scratch database under `~/.coslash/synthesis`, discarded once the run ends. Synthesis runs never enter your own OpenCode history.

Cursor synthesis uses read-only ask mode. coSlash also disables file, shell, write, web, and MCP tools for the run. Each run uses a temporary data directory under `~/.coslash/synthesis`. coSlash removes the directory after the run. At startup, coSlash removes abandoned synthesis directories that are more than one hour old. Synthesis chats do not enter your Cursor history.

Resume and Start fresh launch your installed agent CLI. Its later network and data behavior is governed by that tool.

## Share preview and approval

During an active Share to Hub flow, **See what gets shared** builds a local
`session-snapshot/v1` preview through the same canonical serializer whose bytes
a future opt-in upload will use. Local-only session details do not show this
action. Previewing does not upload or approve anything. If the source revision
changes, validation fails, or the mandatory snapshot exceeds 256 KiB, approval
remains blocked.

For opt-in user testing before the Team flow ships, append
`?team-preview=1` to the local coSlash URL. This reveals a clearly labeled
preview-only trigger in session details; it does not enable a Team workspace,
approval, or upload.

The preview contains metadata and repository-relative file-change statistics.
Session titles, summaries, declared goals, prompts, digests, todos, commit
subjects, subagent details, commands, transcripts, assistant reasoning, tool
output, file diffs, environment variables, and unresolved local paths stay on
the device. Redaction and truncation records identify affected canonical paths.

The fixture-backed Share flow is available to source builds with
`?team-share=1`. It exercises eligibility, destination, selection, exact review,
partial retry, and Hub route states, but is labeled **NO UPLOAD** and never
contacts a cloud service. Use `&share-state=signed_out`, `pairing_required`,
`credential_dormant`, or `credential_revoked` to inspect eligibility states,
and `&share-result=partial` to inspect retry.

The production flow will bind approval to the source revision, canonical hash
and byte count, and displayed destination. Failures that require renewed review
refresh the preview or destination and require explicit approval. Unchanged
retries retain their idempotency key and canonical bytes; accepted items are
not sent again. The server derives workspace authority from authenticated state
or a workspace-bound device credential and never trusts the client assertion to
select or retarget a workspace.

The additive **Share full revision** flow is available only for an eligible
SSH Codex session with a validated `full-session-record/v1` in the last-good
cache. It does not change the v1 snapshot preview or upload. Before loading the
record, coSlash verifies the authenticated paired destination and that the Hub
advertises the v2 protocol, record version, and bounded record limit. Network
and temporary discovery failures remain retryable; only a valid discovery
response without full-v2 support is incompatible. Pairing, discovery, and
oversize errors do not include session content.

The full-v2 review displays the destination, active-member audience, canonical
record size, total envelope size, revision identity, record SHA-256, repository,
and the entire canonical envelope, including all prompt text, commands, todos,
digest entries, subagent fields, paths, and ordered file-change bodies present
in the record. Full records are not redacted or truncated. The review therefore
shows an embedded-secret warning and requires a separate checkbox approval.

Approval binds the exact source, agent, session, full-record revision, record
hash and size, envelope size, repository, destination workspace and name, and
active-member count. Immediately before one bounded gzip upload, coSlash
re-loads the immutable record, re-fetches the destination and audience, and
rebuilds the canonical envelope. Any binding change requires a new review. An
ambiguous timeout is reconciled with the same idempotency key; a retry cannot
create a second revision.

The library card for an SSH session carries the canonical origin remote when
`git remote get-url origin` succeeds on that host. The portable record itself
still omits that field. If no bounded repository identity is available, the
upload uses the disclosed working-directory basename and marks the repository
local-only; the review shows that exact fallback before approval.

## Local server

coSlash listens on IPv4 loopback and protects API requests with a new access token on every start. It rejects unexpected hosts, origins, and cross-site browser requests. The token is stored in `~/.coslash/token` with mode `0600`, so other processes running as your macOS user can still read it and access coSlash. Do not proxy or forward the port.

## Control and removal

Synthesis is off until you enable and save it in Settings. Disable it there to stop new requests, then delete `~/.coslash/summaries` to remove cached results.

Disabling a remote machine stops refreshes and hides its cards but retains its
normalized last-good cache and any optional helper; it does not change Linux.
**Remove** deletes the local host configuration and cache even if the host is
offline. It deliberately leaves any previously installed helper in place on the
remote machine.

To remove all coSlash data, quit coSlash and delete `~/.coslash` (or your `COSLASH_HOME`). `coslash doctor --json` and **Copy diagnostics** exclude transcript contents, prompts, and session names.
