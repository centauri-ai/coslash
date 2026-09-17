---
name: review
description: Use when a user wants Claude Code, Codex, or OpenCode to review a local coSlash session.
---

# coSlash review

Run `coslash review <session> --with claude|codex|opencode` with the user's session and reviewer. If the user did not specify a reviewer, ask which one to use; do not choose or fall back automatically.

Return stdout unchanged. If the command fails, return stderr and the exit status, then suggest `coslash doctor --json`. Do not build or inspect the review prompt, diff, debrief, or session name, and do not launch an agent directly.
