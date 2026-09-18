---
name: handoff
description: Use when a user wants the canonical coSlash handoff or context for a specific session.
---

# coSlash handoff

Run `coslash handoff <agent>:<session>` with the `selector` returned by `coslash sessions`.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not generate, summarize, or reformat the handoff.
