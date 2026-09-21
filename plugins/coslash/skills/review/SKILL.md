---
name: review
description: Use when a user wants Claude Code, Codex, or OpenCode to review a local coSlash session.
---

# coSlash review

Run `coslash review <agent>:<session> --with claude|codex|opencode` with the `selector` returned by `coslash sessions [query] --json` and the user's reviewer. If the user did not specify a reviewer, ask which one to use; do not choose or fall back automatically.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

If the command reports `coSlash app is not running`, resolve the `coslash` executable from `PATH`, launch that executable directly as `coslash --no-open --port 0` in the background with output redirected to a temporary log, and retain its PID. Do not launch for other errors. Poll `coslash sessions --json` until it succeeds, the process exits, or three minutes pass.

On startup failure or timeout, send SIGTERM to the retained PID if it is still running, wait for it to exit, and return the startup log. Otherwise remove the temporary log and leave the instance running. Server shutdown cancels active reviews, so do not close it after starting a review; tell the user it must remain running until the review ends. Report cleanup failures on stderr without changing command stdout. Never stop an instance that was already running or a process found by name, port, or a PID file.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not build or inspect the review prompt, diff, debrief, or session name, and do not launch an agent directly.
