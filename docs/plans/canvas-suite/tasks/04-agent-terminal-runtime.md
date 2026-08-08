# Task 04 — Agent Execution and Native Terminal Runtime

## Objective

Implement explicit Claude/Codex execution plus an authenticated native PTY/WebSocket terminal attached to persistent tmux sessions.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/04.js`](../task-status/04.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

No updates yet.

## Dependencies

- Tasks 01 and 02 merged into the assigned base SHA.
- Approved terminal dependency versions selected, documented, and pinned by the master in Task 02.

## Owned paths

- `collector/internal/plugins/canvas/agentexec/`.
- `collector/internal/plugins/canvas/terminal/`.
- New plugin-local tests and fakes.
- Do not edit `httpsec`, `main.go`, `go.mod`, or frontend packages.

## Required behavior

- Explicit argv and environment allowlists; no user text interpolated into shell commands.
- Claude/Codex start, resume, same-vendor fork, bounded headless execution, and session/thread ID capture.
- Deterministic validated tmux names.
- PTY attach, input, resize, disconnect, reconnect, status, and stop.
- Writable/read-only policy enforced server-side.
- Bracketed paste for note delivery.
- Bounded registries and explicit close ownership for sockets, PTYs, timers, processes, and watchers.
- Collector shutdown detaches clients while preserving only tmux sessions allowed by policy.

## Tests

```sh
cd collector
go test -race ./internal/plugins/canvas/agentexec/... ./internal/plugins/canvas/terminal/...
```

Use fake executables and isolated tmux names. Test missing binaries, bad IDs, command injection, resize limits, large frames, wrong origins/tokens through a test wrapper, reconnect, repeated lifecycle cleanup, and process exit races.

## Exit gate

- No ttyd or random port allocation remains.
- Repeated start/attach/stop cycles return resource counts to baseline.
- Task 02 can integrate the WebSocket handler without changing its semantics.

## Report back

```markdown
Task: 04 Agent/terminal runtime
Status: complete | partial | blocked
Branch/base/result SHA:
Execution adapters delivered:
Terminal lifecycle delivered:
Pinned dependencies consumed:
Tests/race/leak results:
Security findings:
Contract deviations:
New issues/risks:
Recommended tasks now unblocked:
```
