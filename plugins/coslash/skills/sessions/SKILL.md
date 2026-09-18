---
name: sessions
description: Use when a user wants to list, find, search, or select local coSlash sessions.
---

# coSlash sessions

Run `coslash sessions [query] --json`, omitting the query to list every local session.

Use the returned `selector` field for handoff, send, and review commands. It preserves both the agent and session ID.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not invent flags, reshape the JSON, or reimplement filtering.
