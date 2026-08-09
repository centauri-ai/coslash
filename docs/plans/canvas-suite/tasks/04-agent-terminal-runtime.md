# Task 04 — Agent Execution and Native Terminal Runtime

## Objective

Implement explicit Claude/Codex execution plus an authenticated native PTY/WebSocket terminal attached to persistent tmux sessions.

## Local review outcome

Complete at 2026-08-09T02:19:04Z. Accepted and locally merged into `hlu/canvas-migration` at `01aa158ecc322b3dcf4b71e46d278944147ca7b6`; the live-agent matrix remains assigned to Task 18.

## Automatic status and reporting

Canonical dashboard record: [`../task-status/04.js`](../task-status/04.js). The assigned agent must follow [`../AUTOMATION.md`](../AUTOMATION.md) and update the sidecar plus this brief at claim, start, every material checkpoint/test, block, review handoff, and review outcome. Do not ask the human to re-enter status.

### Automation progress log

#### Live status record

```yaml
state: review
agent: codex-root-task-04
branch: codex/canvas-task-04-agent-terminal
worktree: /Users/helu/code/product/coslash-task-04-unblock
base_sha: d7b12784da5bd4a8953b59858ce072d178bacff0
result_sha: fc9c2be8bbc599c7a3b558c54a397a0985f3d997
claimed_at: 2026-08-09T01:10:37Z
started_at: 2026-08-09T01:10:37Z
updated_at: 2026-08-09T01:21:25Z
current_focus: master review and integration handoff
```

- 2026-08-09T01:10:37Z — **claimed**. Re-read the central status, Task 02/04 sidecars, this brief, and Git/worktree evidence. Base `d7b1278` is the verified Task 02 dependency follow-up containing Task 01 contracts, Task 02 guarded WebSocket helpers, `coder/websocket v1.8.15`, and `creack/pty v1.1.24`. Active Tasks 11 and 14 own non-overlapping `dagama/` and `atlas/` paths. The operator explicitly authorized starting Task 04 from reviewed dependency results.
- 2026-08-09T01:10:37Z — **in_progress**. Started implementation in the existing isolated worktree. Next: inspect contracts and launch conventions, then implement only `agentexec/`, `terminal/`, and plugin-local tests/fakes.
- 2026-08-09T01:14:26Z — **in_progress**. Implemented explicit allowlisted Claude/Codex start, resume, same-vendor fork, and bounded headless execution. Prompts use stdin for headless runs; environments, cwd, output, time, profiles, and session IDs are bounded/validated. `go test -race ./internal/plugins/canvas/agentexec/...` passes. Next: implement tmux, PTY, lifecycle, bracketed paste, and guarded WebSocket behavior.
- 2026-08-09T01:21:25Z — **review** at `fc9c2be`. Delivered the native tmux/PTY/WebSocket runtime, server-side write policy, bracketed-paste notes, bounded registry/frame/lifecycle ownership, and full execution adapters. Targeted repeated race, uncached full collector race, full vet, coverage, ownership, and forbidden-runtime audits pass. Next: master review and merge; live disposable agents remain Task 18.

## Dependencies

- Task 01 contracts.
- Dependency versions supplied by the master through task 02.

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
Dependencies requested:
Tests/race/leak results:
Security findings:
Contract deviations:
New issues/risks:
Recommended tasks now unblocked:
```

## Worker report — review — updated 2026-08-09T01:21:25Z

```yaml
task: "04 Agent/terminal runtime"
status: review
agent: codex-root-task-04
branch: codex/canvas-task-04-agent-terminal
worktree: /Users/helu/code/product/coslash-task-04-unblock
base_sha: d7b12784da5bd4a8953b59858ce072d178bacff0
result_sha: fc9c2be8bbc599c7a3b558c54a397a0985f3d997
summary: >-
  Added explicit allowlisted Claude/Codex execution and a guarded native
  tmux/PTY/WebSocket terminal runtime with bounded frames, registries, output,
  time, prompts, environments, and lifecycle ownership.
execution_adapters_delivered:
  - "Interactive start/resume/same-vendor fork for Claude and Codex using direct argv."
  - "Bounded headless execution with stdin prompts and safe errors."
  - "Claude session-id and Codex thread-id capture from bounded JSON streams."
terminal_lifecycle_delivered:
  - "Deterministic collision-resistant validated tmux names and direct tmux argv."
  - "Create/adopt/status/attach/reconnect/input/resize/disconnect/stop."
  - "Native PTY clients and authenticated static-subprotocol WebSocket bridging."
  - "Server-side writable/read-only policy and bracketed-paste note delivery."
  - "Bounded registry and explicit socket/goroutine/PTY/process/shutdown ownership."
dependencies_requested:
  - "github.com/coder/websocket v1.8.15 (already pinned in base d7b1278)"
  - "github.com/creack/pty v1.1.24 (already pinned in base d7b1278)"
  - "No npm dependency"
changed_files:
  - collector/internal/plugins/canvas/agentexec/agentexec.go
  - collector/internal/plugins/canvas/agentexec/agentexec_test.go
  - collector/internal/plugins/canvas/terminal/handler.go
  - collector/internal/plugins/canvas/terminal/handler_test.go
  - collector/internal/plugins/canvas/terminal/manager.go
  - collector/internal/plugins/canvas/terminal/manager_test.go
tests:
  - { command: "go test -race -count=3 ./internal/plugins/canvas/agentexec/... ./internal/plugins/canvas/terminal/...", result: passed }
  - { command: "go test -race -count=1 ./...", result: passed, evidence: "uncached full collector suite" }
  - { command: "go vet ./...", result: passed }
  - { command: "go test -cover ./internal/plugins/canvas/agentexec/... ./internal/plugins/canvas/terminal/...", result: passed, evidence: "78.2% agentexec; 53.4% terminal" }
  - { command: "git diff --check plus owned-path and ttyd/listener audits", result: passed }
security_findings:
  - "No shell command interpolation, ttyd, listener, or random port allocation in Task 04 code."
  - "Wrong/missing tokens, foreign origins, missing static subprotocol, unknown/oversized frames, bad ids, unsafe profiles/env, and read-only writes fail closed."
contract_deviations: []
issues:
  - { severity: P3, status: deferred, summary: "Live disposable Claude/Codex/tmux matrix not run in ordinary worker tests", owner: "Task 18" }
remaining_work:
  - "Master review/merge and handler mounting by the integration consumer."
improvements:
  - "Task 18 can add OS-level FD/goroutine snapshots around the live matrix."
known_issues:
  - "No real agent was launched; tests deliberately use fakes and guarded loopback sockets."
follow_ups:
  - "Task 09: session terminal creation and routes."
  - "Tasks 12/15: bounded controller execution and terminal takeover."
  - "Task 18: live disposable agent/tmux and leak verification."
recommended_tasks_now_unblocked: ["09 (after 02/06/08)", "12 (after 05/11)", "15 (after 05/14)"]
central_updates_requested: { status: true, reports: true, issues: true, decisions: false }
```
