---
name: handoff
description: Use when a user wants the canonical coSlash handoff or context for a specific session.
---

# coSlash handoff

If the user means the current session, build the selector from the environment and do not list sessions: `claude:$CLAUDE_CODE_SESSION_ID` in Claude Code, `codex:$CODEX_SESSION_ID` in Codex. If that variable is empty, or if the user names another session, use the `selector` returned by `coslash sessions [query] --json`. Then run `coslash handoff <agent>:<session>`.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

If the command reports `coSlash app is not running`, resolve the `coslash` executable from `PATH`, launch that executable directly as `coslash --no-open --port 0` in the background with output redirected to a named temporary log, and retain its PID. Do not launch for other errors. Every two seconds, retry the exact original command while it reports `coSlash app is not running`, until three minutes pass. The command reports that error before it contacts the app, so a retry has no side effect. If the retained process exits because another coSlash app is already running, keep retrying for that app; for any other early exit, return the startup log.

When the command reports anything else, stop retrying and use that result. Keep the temporary log while the retained process runs, and arrange its removal after that process exits; if it already exited as a startup contender, remove the log when the retries end. On startup failure or timeout, send SIGTERM to the retained PID if it is still running, wait for it to exit, return the startup log, then remove it. Otherwise leave the instance running by default. If the user asks to close it after the command, arrange cleanup before launching so interruption also sends SIGTERM to the retained PID, then wait for it to exit and remove the log. Report cleanup failures on stderr without changing command stdout. Never stop an instance that was already running or a process found by name, port, or a PID file.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not generate, summarize, or reformat the handoff.
