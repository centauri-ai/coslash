---
name: doctor
description: Use when a user wants structured coSlash diagnostics or help investigating a broken local setup.
---

# coSlash doctor

Always run `coslash doctor --json`; never omit `--json`. The coSlash app does not need to be running.

Return stdout unchanged. If the command fails, return stderr and the exit status. Do not start the app, invent remediation, or reshape the JSON.
