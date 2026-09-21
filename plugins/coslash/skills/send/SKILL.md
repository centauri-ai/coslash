---
name: send
description: Use when a user wants to start a new Claude Code or Codex session from a coSlash session.
---

# coSlash send

If a selector is not already available, run `coslash sessions [query] --json`, omitting the query to list every local session. Then run `coslash send <agent>:<session> --to claude|codex [message]` with the returned `selector`, the user's target, and optional message. Pass the message as one quoted argument.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not construct the handoff or launch the agent yourself.
