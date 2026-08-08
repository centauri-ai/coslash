# Issue and Risk Register

Only the master agent edits this file. Do not delete closed items; mark them resolved with evidence.

| ID    | Severity | Found by task | Area          | Description                                                                   | Owner  | Status     | Resolution/evidence                         |
| ----- | -------- | ------------- | ------------- | ----------------------------------------------------------------------------- | ------ | ---------- | ------------------------------------------- |
| I-001 | high     | evaluation    | Legacy source | Current legacy tip has 12 TypeScript build errors.                            | 00     | open       | —                                           |
| I-002 | high     | evaluation    | Lifecycle     | Atlas/DaGama tests emitted `EMFILE`; watcher ownership must be characterized. | 00/18  | open       | —                                           |
| I-003 | critical | evaluation    | Terminal      | Legacy ttyd iframe is outside coSlash API authentication.                     | 04     | open       | Replace with guarded native PTY/WebSocket.  |
| I-004 | high     | evaluation    | Compatibility | `.fleetlog/run/**` is embedded throughout prompts, policies, and tests.       | master | controlled | Rename deferred; retain protocol initially. |
