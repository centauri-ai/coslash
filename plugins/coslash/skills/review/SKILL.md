---
name: review
description: Use when a user wants Claude Code, Codex, or OpenCode to review a local coSlash session.
---

# coSlash review

If the user means the current session, build the selector from the environment and do not list sessions: `claude:$CLAUDE_CODE_SESSION_ID` in Claude Code, `codex:$CODEX_SESSION_ID` in Codex. If that variable is empty, or if the user names another session, use the `selector` returned by `coslash sessions [query] --json`. Then run `coslash review <agent>:<session> --with claude|codex|opencode` with that selector and the user's reviewer. If the user did not specify a reviewer, ask which one to use; do not choose or fall back automatically.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

If the command reports `coSlash app is not running`, resolve the `coslash` executable from `PATH`, launch that executable directly as `coslash --no-open --port 0` in the background with output redirected to a named temporary log, and retain its PID. Do not launch for other errors. Every two seconds, retry the exact original command while it reports `coSlash app is not running`, until three minutes pass. The command reports that error before it contacts the app, so a retry has no side effect. If the retained process exits because another coSlash app is already running, keep retrying for that app; for any other early exit, return the startup log.

When the command reports anything else, stop retrying and use that result. Keep the temporary log while the retained process runs, and arrange its removal after that process exits; if it already exited as a startup contender, remove the log when the retries end. On startup failure or timeout, send SIGTERM to the retained PID if it is still running, wait for it to exit, return the startup log, then remove it. Otherwise leave the instance running. Server shutdown cancels active reviews, so do not close it after starting a review; tell the user it must remain running until the review ends. Report cleanup failures on stderr without changing command stdout. Never stop an instance that was already running or a process found by name, port, or a PID file.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not build or inspect the review prompt, diff, debrief, or session name, and do not launch an agent directly.
