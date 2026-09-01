# Data and privacy

coSlash runs locally, but agent transcripts can contain prompts, source code, command output, credentials, and other secrets. Review these boundaries before use.

## Local access

coSlash reads, but does not modify:

- Claude Code and Codex transcripts and their local session metadata.
- Recorded working directories and Git metadata used for branch and change summaries.
- Local process information used to identify live sessions.

`COSLASH_HOME` sets the storage root and defaults to `~/.coslash`:

| Path | Contents |
| --- | --- |
| `settings.json` | Synthesis, appearance, and terminal preferences. |
| `token` | Access token for the current server process. |
| `summaries/` | Cached synthesis results. |
| `synthesis/` | Temporary synthesis files and the OpenCode scratch database. |
| `sys-prompts/` | Temporary handoffs for fresh sessions. |
| `remotes/<source-id>/snapshot.json` | Normalized remote session facts, opaque file fingerprints, coverage, and health. No raw transcript rows or remote absolute paths. |

coSlash creates the storage directory with mode `0700` and persistent files with mode `0600`. Programs running as your macOS user can still read them.

## Optional SSH/SFTP access and helper installation

When one remote machine is enabled, the Mac's system `ssh` client uses the saved
OpenSSH alias and requests SFTP. SFTP collection runs no coSlash code on Linux.
After a connection test, the user may explicitly consent to install the optional
collector helper. The Mac verifies signed release metadata and the exact artifact
digest before it uploads a versioned executable to
`~/.local/lib/coslash/helpers/<version>/coslash-helper`, owned by the SSH user
with mode `0700`. The helper has no root access or network access, reads only the
same fixed allowlist below, and streams bounded normalized facts back to the Mac.
No path, command, prompt, transcript row, cache, or handoff supplied by the Mac
can choose files the helper opens.

Builds without approved embedded release keys and metadata publication disable
the install action explicitly. They continue using SFTP and never upload an
unverified helper.

Both collection paths may read these paths beneath the SSH user's home:

- `.claude/projects`, `.claude/sessions`, and `.claude/jobs`;
- `.codex/sessions`, `.codex/archived_sessions`, and
  `.codex/session_index.jsonl`;
- a numeric `/proc/<pid>` entry only to validate a PID already present in Claude
  metadata.

The SFTP interface has no write, delete, rename, or chmod operation. It rejects
symlinks and canonical paths outside the allowlist. Current ceilings are 32 MiB
per file, 128 MiB per refresh, 2,000 candidate files per agent, 10,000 directory
entries, depth 16, and 90 seconds per refresh. Raw bytes stay in bounded Mac
memory only while parsing. The disk cache deliberately omits prompts, transcript
events, commands, tool output, edited-file paths, working directories, SSH
configuration, and credentials.

## Outbound data

The collector does not upload data itself. If you enable synthesis, it passes a bounded set of session facts—such as prompts or recaps, todos, filenames, commands, and commit text—to your selected local Claude Code, Codex, or OpenCode CLI. That CLI sends the request using its existing authentication, so the selected provider's settings and terms apply.

OpenCode has no ephemeral mode, so coSlash points each run at its own scratch database under `~/.coslash/synthesis`, discarded once the run ends. Synthesis runs never enter your own OpenCode history.

Resume and Start fresh launch your installed agent CLI. Its later network and data behavior is governed by that tool.

## Snapshot preview

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

The preview can include a bounded `firstPrompt` (up to 16 KiB). Known
credential patterns are redacted, but other sensitive text can remain, so the
exact bounded value must be reviewed. Redaction and truncation records identify
affected canonical paths. Raw transcripts, assistant reasoning, tool output,
file diffs, raw commands, environment variables, and unresolved local paths are
excluded.

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

## Local server

coSlash listens on IPv4 loopback and protects API requests with a new access token on every start. It rejects unexpected hosts, origins, and cross-site browser requests. The token is stored in `~/.coslash/token` with mode `0600`, so other processes running as your macOS user can still read it and access coSlash. Do not proxy or forward the port.

## Control and removal

Synthesis is off until you enable and save it in Settings. Disable it there to stop new requests, then delete `~/.coslash/summaries` to remove cached results.

Disabling a remote machine stops refreshes and hides its cards but retains its
normalized last-good cache and any optional helper; it does not change Linux.
**Remove host only** deletes the source's `~/.coslash/remotes/<source-id>`
directory and deliberately leaves a previously installed helper in place.
**Uninstall helper and remove** first removes only the exact verified helper
version, then deletes local host settings and cache. If the remote uninstall
fails, coSlash keeps the local host configuration so you can retry or explicitly
choose remove-only; it never silently forgets helper ownership.

To remove all coSlash data, quit coSlash and delete `~/.coslash` (or your `COSLASH_HOME`). `coslash doctor --json` and **Copy diagnostics** exclude transcript contents, prompts, and session names.
