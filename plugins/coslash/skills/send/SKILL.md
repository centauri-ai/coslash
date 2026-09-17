---
name: send
description: Use when a user wants to start a new Claude Code or Codex session from a coSlash session.
---

# coSlash send

Run `coslash send <session> --to claude|codex [message]` with the user's session, target, and optional message. Pass the message as one quoted argument.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not construct the handoff or launch the agent yourself.
