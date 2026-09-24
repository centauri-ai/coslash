---
name: doctor
description: Use when a user wants structured coSlash diagnostics or help investigating a broken local setup.
---

# coSlash doctor

Always run `coslash doctor --json`; never omit `--json`. The coSlash app does not need to be running.

In Codex, if the command's JSON reports `operation not permitted` for coSlash storage, request escalated execution and retry `coslash doctor --json` outside the sandbox. If permission is denied, stop and report that coSlash needs access to its local files.

Return stdout unchanged. If the command fails, return stderr and the exit status. Do not start the app, invent remediation, or reshape the JSON.
