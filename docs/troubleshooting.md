# Troubleshooting

Start with `coslash doctor`. It checks session sources, agent CLIs, storage, and an optional remote SSH host. Use `coslash doctor --json` for a shareable report, and check the terminal where coSlash started for logs.

## Sessions are missing

Create at least one local Claude Code or Codex session, then reload. Run `coslash doctor` for unreadable or missing sources. In the UI, select **All** vendors and time windows and clear search.

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

## Remote SSH host

### Collector missing or outdated

Probe states `setup_required` or `upgrade_required` mean the Mac cannot find a compatible collector at `~/.local/bin/coslash` on the Linux host. Install or replace it using [`docs/remote-host-installation.md`](remote-host-installation.md), then use **Test connection** or **Retry**. The app does not download or upgrade the remote binary.

### Noisy shell startup output

Background SSH commands tolerate bounded shell startup noise before one `COSLASH-REMOTE/1` frame. If login scripts print large or malformed output, probe and snapshot fail with an invalid-transport style error. Keep machine-readable stdout clean for non-interactive SSH, or move interactive-only printing behind an interactive-shell guard.

### Agent CLI missing on the remote PATH

Monitoring can succeed while Resume or Start Fresh is unavailable when `launchableAgents` omits an agent. Non-login SSH sessions often miss PATH entries that only interactive shells load. Confirm `ssh <alias> 'command -v claude'` and `command -v codex` in the same non-interactive environment coSlash uses, then adjust the remote shell profile accordingly.

### BatchMode authentication or host-key failures

Probe, snapshot, and handoff staging use `BatchMode=yes` and must not prompt. Run `ssh <alias>` once interactively so host keys and authentication succeed, then retry from coSlash. Diagnostics may show a bounded redacted error such as `connection failed`; they do not expose credentials, key paths, or SSH config contents, and they do not guess from locale-specific stderr phrases.

### Retry backoff

Automatic refresh failures back off from three minutes exponentially to a 30-minute cap. Manual **Retry** bypasses the delay but does not start a second refresh while one is already running. Diagnostics can include `nextRetryAtMs` when a backoff is active.

### Limited history

A `limited` host means discovery or payload limits kept the newest whole sessions and declared truncation. Widen the displayed time window only if you need older remote cards; Hub's local all-history setting does not widen remote collection.

### Clock skew

Diagnostics may include `clockOffsetMs` and `roundTripMs`. Coverage comparisons stay in the Mac request clock. Large skew can move edge-of-window sessions; use the reported offset as a diagnostic, not as sub-second truth.

### Stale cache behavior

When a refresh fails after a previous success, the board keeps the last good remote snapshot and marks the host stale or upgrade-required. Disable removes cards but retains the secured Mac cache; remove deletes `~/.coslash/remotes/<sourceId>/`.

### Interactive resume errors

Remote Resume and Start Fresh open an interactive SSH terminal. Authentication prompts, missing agent CLIs, or a missing session can fail inside that terminal without a precise Mac-side translation of remote stderr. Prefer the installation guide, PATH checks, and `snapshot --probe` over interpreting locale-specific error text.

## Report a bug

[Open an issue](https://github.com/centauri-ai/coslash/issues) with:

- `coslash doctor --json` output or **Copy diagnostics** from the app.
- Minimal reproduction steps and the expected and actual result.
- Relevant logs with paths, prompts, tokens, secrets, and session IDs redacted.

Do not attach a transcript unless you have reviewed and redacted it.
