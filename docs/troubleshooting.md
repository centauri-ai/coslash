# Troubleshooting

Start with `coslash doctor`. It checks session sources, agent CLIs, and storage. Use `coslash doctor --json` for a shareable report, and check the terminal where coSlash started for logs.

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

## Report a bug

[Open an issue](https://github.com/centauri-ai/coslash/issues) with:

- `coslash doctor --json` output or **Copy diagnostics** from the app.
- Minimal reproduction steps and the expected and actual result.
- Relevant logs with paths, prompts, tokens, secrets, and session IDs redacted.

Do not attach a transcript unless you have reviewed and redacted it.
