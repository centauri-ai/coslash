# Issue and Risk Register

Only the master agent edits this file. Do not delete closed items; mark them resolved with evidence.

| ID    | Severity | Found by task | Area          | Description                                                                   | Owner  | Status     | Resolution/evidence                         |
| ----- | -------- | ------------- | ------------- | ----------------------------------------------------------------------------- | ------ | ---------- | ------------------------------------------- |
| I-001 | high     | evaluation    | Legacy source | Current legacy tip has 12 TypeScript build errors.                            | 00     | open       | —                                           |
| I-002 | high     | evaluation    | Lifecycle     | Atlas/DaGama tests emitted `EMFILE`; watcher ownership must be characterized. | 00/18  | open       | —                                           |
| I-003 | critical | evaluation    | Terminal      | Legacy ttyd iframe is outside coSlash API authentication.                     | 04/18  | in_review  | Task 04 result `fc9c2be` supplies guarded native PTY/WebSocket with no ttyd or random ports; Task 18 retains live verification. |
| I-004 | high     | evaluation    | Compatibility | `.fleetlog/run/**` is embedded throughout prompts, policies, and tests.       | master | controlled | Rename deferred; retain protocol initially. |
| I-005 | high     | 02            | Scheduling    | Task 04 waited for versions requested by Task 04 but could not start without versions supplied through Task 02. | master | resolved | Follow-up `d7b1278` pins `coder/websocket v1.8.15` and `creack/pty v1.1.24`; no npm dependency is required. |
