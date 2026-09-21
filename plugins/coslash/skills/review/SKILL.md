---
name: review
description: Use when a user wants Claude Code, Codex, or OpenCode to review a local coSlash session.
---

# coSlash review

Run `coslash review <agent>:<session> --with claude|codex|opencode` with the `selector` returned by `coslash sessions [query] --json` and the user's reviewer. If the user did not specify a reviewer, ask which one to use; do not choose or fall back automatically.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not build or inspect the review prompt, diff, debrief, or session name, and do not launch an agent directly.
