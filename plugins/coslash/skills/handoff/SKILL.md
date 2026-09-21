---
name: handoff
description: Use when a user wants the canonical coSlash handoff or context for a specific session.
---

# coSlash handoff

Run `coslash handoff <agent>:<session>` with the `selector` returned by `coslash sessions [query] --json`.

Use the `coslash` executable found on `PATH` and preserve the inherited `COSLASH_HOME`. Do not substitute a repository-relative binary or `go run` unless the user explicitly asks to test a source build.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not generate, summarize, or reformat the handoff.
