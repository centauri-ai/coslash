# Troubleshooting

Start with `coslash doctor`. It checks session sources, agent CLIs, and storage. Use `coslash doctor --json` for a shareable report, and check the terminal where coSlash started for logs.

## Sessions are missing

Create at least one local Claude Code or Codex session, then reload. Run `coslash doctor` for unreadable or missing sources. In the UI, select **All** vendors and time windows and clear search.

For a remote machine, choose **Settings → Machines → Add remote host**. coSlash
uses the Mac's existing OpenSSH configuration, so first run `ssh <alias>` in
Terminal if the host key or authentication still needs confirmation. The Linux
SSH server must enable SFTP and the SSH user must be able to read the
Claude/Codex paths listed in [Data and privacy](data-and-privacy.md). Setup
checks the connection, installs the digest-verified helper, and verifies it.

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

- **End users** should not build from source. Install a prebuilt binary with `curl -fsSL https://coslash.io/install.sh | bash`, Homebrew, or a release archive (see the README Install section).
- **Developers** need Go 1.26+ (`brew install go` or https://go.dev/dl/) and Node 24+ (`brew install node` or https://nodejs.org/). Versions are pinned in `collector/go.mod` and `frontend/.nvmrc`.
- If the binary starts but the UI is missing, it was built with `make build` instead of `make release`. Rebuild with `make release` so the frontend is embedded.

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
