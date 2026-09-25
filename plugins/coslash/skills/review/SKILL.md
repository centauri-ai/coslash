---
name: review
description: Use when a user wants Claude Code, Codex, or OpenCode to review a local coSlash session.
---

# coSlash review

If the user means the current session, build the selector from the environment and do not list sessions: `claude:$CLAUDE_CODE_SESSION_ID` in Claude Code, `codex:$CODEX_SESSION_ID` in Codex. If that variable is empty, or if the user names another session, use the `selector` returned by `coslash sessions [query] --json`. Then run `coslash review <agent>:<session> --with claude|codex|opencode` with that selector and the user's reviewer. If the user did not specify a reviewer, ask which one to use; do not choose or fall back automatically.

After starting a review, run `coslash review status <agent>:<session> --json` with the same selector to check its progress. While the status is `pending`, retry at reasonable intervals if the user wants the outcome. Report the `result` when it is `completed` or the `error` when it is `failed`. If the app restarts or its 100-result history evicts an old review, status is lost; a `review not found` response does not prove that the review failed.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

If a command in the form that this skill gives reports `Error: usage:`, the installed coSlash is older than this skill. Run `coslash --version`, tell the user to update coSlash the same way they installed it, for example `brew upgrade coslash`, and stop. Do not retry with other flags.

In Codex, if the command reports `coSlash app is not running`, `coSlash app is not reachable`, or `operation not permitted` for coSlash storage, request escalated execution and retry the exact command outside the sandbox. Use the same elevated execution for any startup and retries below. If permission is denied, stop and report that coSlash needs access to its local files and loopback address.

If the command reports `coSlash app is not running` after the Codex retry, when applicable, resolve the `coslash` executable from `PATH`, launch that executable directly as `coslash --no-open --port 0` in the background with output redirected to a temporary log created with `mktemp -t coslash.XXXXXX`, and retain its PID. Do not launch for other errors. Every two seconds, retry the exact original command while it reports `coSlash app is not running`, until three minutes pass. The command reports that error before it contacts the app, so a retry has no side effect. If the retained process exits because another coSlash app is already running, keep retrying for that app; for any other early exit, return the startup log.

When the command reports anything else, stop retrying and use that result. Keep the temporary log while the retained process runs, and arrange its removal after that process exits; if it already exited as a startup contender, remove the log when the retries end. On startup failure or timeout, send SIGTERM to the retained PID if it is still running, wait for it to exit, return the startup log, then remove it. Otherwise leave the instance running. Server shutdown cancels active reviews, so do not close it after starting a review; tell the user it must remain running until the review ends. Report cleanup failures on stderr without changing command stdout. Never stop an instance that was already running or a process found by name, port, or a PID file.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not build or inspect the review prompt, diff, debrief, or session name, and do not launch an agent directly.
