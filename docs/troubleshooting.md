# Troubleshooting

Start with `coslash doctor`. It checks session sources, agent CLIs, and storage. Use `coslash doctor --json` for a shareable report, and check the terminal where coSlash started for logs.

## Sessions are missing

Create at least one local Claude Code, Codex, Cursor, or OpenCode session, then reload. Run `coslash doctor` for unreadable or missing sources. In the UI, select **All** vendors and time windows and clear search.

For Cursor, `coslash doctor` reports the IDE (`cursor`) and CLI (`agent`) separately. coSlash reads only local Cursor IDE and CLI sessions: Cursor SDK sessions and remote Cursor collection are unsupported. Cursor CLI token and compaction data can be unavailable because Cursor does not store them reliably; Cursor IDE exposes current context occupancy separately, not cumulative token usage.

For a remote machine, choose **Settings → Machines → Add remote host**. coSlash
uses the system's existing OpenSSH configuration or a simple `user@host`
destination. If SSH needs authentication or host-key confirmation, choose
**Authenticate in Terminal** and complete the native prompt there; coSlash never
receives the credential. A changed host key requires verification outside the
authentication flow. The Linux SSH server must enable SFTP and the SSH user
must be able to read the Claude/Codex paths listed in [Data and
privacy](data-and-privacy.md). Setup checks the connection, installs the
digest-verified helper, and verifies it.

If setup fails, **Retry setup** tries again. A host that does not answer SSH is
shown offline after a short check and retried in the background. coSlash uses
SFTP when the helper is unavailable, blocked by a `noexec` mount, unsupported,
or fails verification. Future coSlash releases automatically update only helpers
that coSlash previously installed and verified. Never paste remote stderr into a
bug report; **Copy diagnostics** contains bounded structured transport, version,
timing, byte-count, and coverage facts.

`SFTP subsystem unavailable` means ordinary SSH may work while SFTP is disabled
by `sshd_config` or account policy. `Agent data is not readable` means the SSH
account connected but lacks file permissions. `No Claude or Codex data found`
means the allowed roots are absent or empty. A stale banner keeps the last-good
cards visible; **Retry** starts an immediate bounded refresh.

To remove a remote host, choose **Remove**. It stops local monitoring even if
the host is offline and leaves its optional helper installed on the host.

## coSlash will not start

A port conflict is reported in the terminal. Stop the other process or run:

```sh
coslash --port 8888
```

## Building from source fails

`make release` in `collector/` needs supported Go and Node versions. It checks them before building and prints install hints if either is missing or unsupported.

- **End users** should not build from source. On macOS, use the install script, Homebrew, or a release archive. On Windows, download the release executable (see the README Install section).
- **Developers** need Go 1.26+ (`brew install go` or https://go.dev/dl/) and Node 24+ (`brew install node` or https://nodejs.org/). Versions are pinned in `collector/go.mod` and `frontend/.nvmrc`.
- If the binary starts but the UI is missing, it was built with `make build` instead of `make release`. Rebuild with `make release` so the frontend is embedded.

## Synthesis does not appear

Confirm that synthesis is enabled in Settings and that the selected CLI is installed, authenticated, and supports the selected model. Short sessions may not be eligible. Storage errors, timeouts, and recent failures also prevent a result; opening the inspector surfaces the current error.

## Resume or Start fresh fails

Launching requires a recorded working directory, the agent CLI, and the terminal selected in Settings. On macOS, confirm Apple Terminal or iTerm2 is installed and allow automation under **System Settings → Privacy & Security → Automation**. On Windows, confirm Windows PowerShell 5.1 is available; coSlash uses Windows Terminal when it is installed and otherwise opens Windows PowerShell directly.

Cursor CLI **Resume** requires the `agent` command and restores the recorded session. **Open Cursor** for a Cursor IDE session requires the `cursor` command and only opens the recorded workspace; Cursor does not provide a way to restore that specific IDE chat.

For Cursor, **Start fresh with handoff** copies the brief to the clipboard. The IDE path opens the workspace; create a fresh chat before pasting the brief. The CLI path launches a new `agent` session; paste the brief there. Cursor does not provide a way for coSlash to inject it. Other supported agents receive the brief as background context and wait for your next message.

Remote Resume and Start fresh require a live SSH connection and a recorded
working directory. They are disabled while the host is offline; wait for it to
reconnect, then try again.

## Report a bug

[Open an issue](https://github.com/centauri-ai/coslash/issues) with:

- `coslash doctor --json` output or **Copy diagnostics** from the app.
- Minimal reproduction steps and the expected and actual result.
- Relevant logs with paths, prompts, tokens, secrets, and session IDs redacted.

Do not attach a transcript unless you have reviewed and redacted it.
