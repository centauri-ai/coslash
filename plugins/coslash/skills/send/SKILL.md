---
name: send
description: Use when a user wants to start a new Claude Code or Codex session from a coSlash session.
---

# coSlash send

If the user means the current session, build the selector from the environment and do not list sessions: `claude:$CLAUDE_CODE_SESSION_ID` in Claude Code, `codex:$CODEX_SESSION_ID` in Codex. If that variable is empty, or if the user names another session, and a selector is not already available, run `coslash sessions [query] --json`, omitting the query to list every local session. Proceed only when exactly one session matches; report no matches or ask the user to choose a returned `selector` when multiple sessions match. Then run `coslash send <agent>:<session> --to claude|codex [message]` with the selected `selector`, the user's target, and optional message. The message is the task that the user gives to the target agent, for example "do an analysis". Pass the message as one quoted argument.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

In Codex, if the command reports `coSlash app is not running`, `coSlash app is not reachable`, or `operation not permitted` for coSlash storage, request escalated execution and retry the exact command outside the sandbox. Use the same elevated execution for any startup and retries below. If permission is denied, stop and report that coSlash needs access to its local files and loopback address.

If the command reports `coSlash app is not running` after the Codex retry, when applicable, resolve the `coslash` executable from `PATH`, launch that executable directly as `coslash --no-open --port 0` in the background with output redirected to a temporary log created with `mktemp -t coslash.XXXXXX`, and retain its PID. Do not launch for other errors. Every two seconds, retry the exact original command while it reports `coSlash app is not running`, until three minutes pass. The command reports that error before it contacts the app, so a retry has no side effect. If the retained process exits because another coSlash app is already running, keep retrying for that app; for any other early exit, return the startup log.

When the command reports anything else, stop retrying and use that result. Keep the temporary log while the retained process runs, and arrange its removal after that process exits; if it already exited as a startup contender, remove the log when the retries end. On startup failure or timeout, send SIGTERM to the retained PID if it is still running, wait for it to exit, return the startup log, then remove it. Otherwise leave the instance running by default. If the user asks to close it after the command, arrange cleanup before launching so interruption also sends SIGTERM to the retained PID, then wait for it to exit and remove the log. Report cleanup failures on stderr without changing command stdout. Never stop an instance that was already running or a process found by name, port, or a PID file.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not construct the handoff or launch the agent yourself.
