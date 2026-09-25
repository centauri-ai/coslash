---
name: sessions
description: Use when a user wants to list, find, search, or select local coSlash sessions.
---

# coSlash sessions

For a broad list request, run `coslash sessions [query] --recent 20 --json`, omitting the query to list local sessions. Present this as a preview limited to 20 most recently active sessions. Use `coslash sessions [query] --json` for a specific search or when the user explicitly requests the full machine-readable list. Do not fetch the unrestricted list for a broad chat request.

For an exact agent filter, use `--agent claude|codex|cursor|opencode`. To get the most recently active matches, add `--recent N`; it sorts by activity time before limiting. For example, `coslash sessions --agent codex --recent 1 --json` finds the latest Codex session. A text query still searches session names, repositories, branches, and agents.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

If a command reports `Error: usage:`, compare the usage it prints with the command you ran. If that usage does not list a subcommand or flag that you used, the installed coSlash is older than this skill. Run `coslash --version`, tell the user to update coSlash the same way they installed it, for example `brew upgrade coslash`, and stop. Otherwise, the arguments are not valid: return stderr and the exit status. Do not retry with other flags.

In Codex, if the command reports `coSlash app is not running`, `coSlash app is not reachable`, or `operation not permitted` for coSlash storage, request escalated execution and retry the exact command outside the sandbox. Use the same elevated execution for any startup and retries below. If permission is denied, stop and report that coSlash needs access to its local files and loopback address.

If the command reports `coSlash app is not running` after the Codex retry, when applicable, resolve the `coslash` executable from `PATH`, launch that executable directly as `coslash --no-open --port 0` in the background with output redirected to a temporary log created with `mktemp -t coslash.XXXXXX`, and retain its PID. Do not launch for other errors. Every two seconds, retry the exact original command while it reports `coSlash app is not running`, until three minutes pass. The command reports that error before it contacts the app, so a retry has no side effect. If the retained process exits because another coSlash app is already running, keep retrying for that app; for any other early exit, return the startup log.

When the command reports anything else, stop retrying and use that result. Keep the temporary log while the retained process runs, and arrange its removal after that process exits; if it already exited as a startup contender, remove the log when the retries end. On startup failure or timeout, send SIGTERM to the retained PID if it is still running, wait for it to exit, return the startup log, then remove it. Otherwise leave the instance running by default. If the user asks to close it after the command, arrange cleanup before launching so interruption also sends SIGTERM to the retained PID, then wait for it to exit and remove the log. Report cleanup failures on stderr without changing command stdout. Never stop an instance that was already running or a process found by name, port, or a PID file.

Use the returned `selector` field for handoff, send, and review commands. It preserves both the agent and session ID.

For specific searches or full machine-readable requests, return stdout unchanged when it fits in chat. For a broad list preview, show each complete session's `selector` and `name` in CLI order, then suggest `--agent`, `--recent`, or a narrower query. Do not sort the preview or save output to a file. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not invent flags or reimplement filtering.
