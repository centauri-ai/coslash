# Troubleshooting

Start with `coslash doctor`. It checks session sources, agent CLIs, and storage. Use `coslash doctor --json` for a shareable report, and check the terminal where coSlash started for logs.

## Sessions are missing

Create at least one local Claude Code or Codex session, then reload. Run `coslash doctor` for unreadable or missing sources. In the UI, select **All** vendors and time windows and clear search.

For a remote machine, use **Settings → Machines → Test connection**. coSlash
uses the Mac's existing OpenSSH configuration, so first run `ssh <alias>` in
Terminal if the host key or authentication still needs confirmation. The Linux
SSH server must enable an SFTP subsystem and the SSH user must be able to read
the Claude/Codex paths listed in [Data and privacy](data-and-privacy.md). No
coSlash installation or agent CLI is required on Linux. The test performs a
bounded inspection of both agent roots, so a successful result also confirms
that supported data is readable.

`SFTP subsystem unavailable` means ordinary SSH may work while SFTP is disabled
by `sshd_config` or account policy. `Agent data is not readable` means the SSH
account connected but lacks file permissions. `No Claude or Codex data found`
means the allowed roots are absent or empty. A stale banner keeps the last-good
cards visible; **Retry** starts an immediate bounded refresh.

## coSlash will not start

A port conflict is reported in the terminal. Stop the other process or run:

```sh
coslash --port 8888
```

## Synthesis does not appear

Confirm that synthesis is enabled in Settings and that the selected CLI is installed, authenticated, and supports the selected model. Short sessions may not be eligible. Storage errors, timeouts, and recent failures also prevent a result; opening the inspector surfaces the current error.

## Resume or Start fresh fails

Launching requires macOS, a recorded working directory, the agent CLI, and the terminal selected in Settings. Confirm Apple Terminal or iTerm2 is installed and allow automation under **System Settings → Privacy & Security → Automation**.

A fresh agent waiting silently is expected: handoff context is marked as background, and the agent waits for your next message.

Remote sessions are monitoring-only, so Resume and Start Fresh are intentionally
hidden. Use **Copy handoff** and continue manually where appropriate.

## Report a bug

[Open an issue](https://github.com/centauri-ai/coslash/issues) with:

- `coslash doctor --json` output or **Copy diagnostics** from the app.
- Minimal reproduction steps and the expected and actual result.
- Relevant logs with paths, prompts, tokens, secrets, and session IDs redacted.

Do not attach a transcript unless you have reviewed and redacted it.
