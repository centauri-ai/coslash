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
| `settings.json` | Synthesis, appearance, terminal, and optional remote-host preferences. |
| `token` | Access token for the current server process. |
| `summaries/` | Cached synthesis results. |
| `synthesis/` | Temporary synthesis files and the OpenCode scratch database. |
| `sys-prompts/` | Temporary local handoffs for fresh sessions. |
| `remotes/<sourceId>/snapshot.json` | Last validated remote session snapshot cached on the Mac. |

coSlash creates the storage directory with mode `0700` and persistent files with mode `0600`. Programs running as your macOS user can still read them.

## Remote host cache and Linux handoffs

When you enable one SSH host, the Mac stores a secured last-good snapshot under `~/.coslash/remotes/<sourceId>/snapshot.json` (`0700` source directory, `0600` file). The cache holds bounded remote-view facts used by the board and inspector. It does not store transcript text, handoff text, credentials, SSH config, private keys, or resolved remote usernames/hostnames.

| Action | Cache behavior |
| --- | --- |
| Disable the host | Stops refresh work and removes its sessions from the current board; retains the secured cache so re-enabling can recover quickly. |
| Remove the host or change the SSH alias | Deletes that source cache. An alias change is treated as remove-plus-add with a new source ID. |

On the Linux host, Start Fresh stages a short-lived handoff under `~/.coslash/sys-prompts` (`0700` directory, `0600` files). Each record is bound to an agent and session, expires after one hour, and can be claimed once. An abandoned handoff becomes unusable after one hour, but physical deletion happens on the next `handoff` or `launch` invocation on that host.

Diagnostics and **Copy diagnostics** may include the user-entered SSH alias, collector/schema versions, capabilities, launchable agents, platform, coverage and retry timing, round-trip timing, and bounded redacted stderr. They never include resolved host/user, key paths, SSH config contents, private keys, credentials, transcript text, handoff text, or private remote working paths.

## Outbound data

The collector does not upload data itself. If you enable synthesis, it passes a bounded set of session facts—such as prompts or recaps, todos, filenames, commands, and commit text—to your selected local Claude Code, Codex, or OpenCode CLI. That CLI sends the request using its existing authentication, so the selected provider's settings and terms apply.

OpenCode has no ephemeral mode, so coSlash points each run at its own scratch database under `~/.coslash/synthesis`, discarded once the run ends. Synthesis runs never enter your own OpenCode history.

Resume and Start fresh launch your installed agent CLI. Its later network and data behavior is governed by that tool. Remote Resume and Start Fresh open an SSH terminal to the configured alias and run only fixed collector launch commands; they do not place remote paths or free-form handoff text in a shell string.

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

Disable or remove a remote host from Settings → Machines. Disabling retains `~/.coslash/remotes/<sourceId>/`; removing the host deletes it. To remove all coSlash data, quit coSlash and delete `~/.coslash` (or your `COSLASH_HOME`). On a Linux host used only as a remote collector, also delete `~/.coslash` there if you no longer want staged handoffs.

`coslash doctor --json` and **Copy diagnostics** exclude transcript contents, prompts, and session names.
