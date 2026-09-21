---
name: handoff
description: Use when a user wants the canonical coSlash handoff or context for a specific session.
---

# coSlash handoff

Run `coslash handoff <agent>:<session>` with the `selector` returned by `coslash sessions [query] --json`.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

If the command reports `coSlash app is not running`, resolve the `coslash` executable from `PATH`, launch that executable directly as `coslash --no-open --port 0` in the background with output redirected to a temporary log, and retain its PID. Do not launch for other errors. Poll `coslash sessions --json` until it succeeds, the process exits, or three minutes pass.

On startup failure or timeout, send SIGTERM to the retained PID if it is still running, wait for it to exit, and return the startup log. Otherwise leave the instance running by default. If the user asks to close it after the command, arrange cleanup before launching so interruption also sends SIGTERM to the retained PID, then wait for it to exit. Remove the temporary log after reading it or once readiness succeeds. Report cleanup failures on stderr without changing command stdout. Never stop an instance that was already running or a process found by name, port, or a PID file.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not generate, summarize, or reformat the handoff.
