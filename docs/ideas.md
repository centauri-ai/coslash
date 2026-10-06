# Ideas

This file lists open design questions that are not decided. When an idea is decided, move it to a file in [`docs/decisions/`](decisions/README.md) and remove it here. When an idea is rejected, keep it here with the reason, so that nobody proposes it again without new facts.

## Open

- **Full reviewer capability versus prompt injection.** A Claude reviewer followed an instruction planted in the reviewed session. One option gives reviewers back their tools, skills, plugins, hooks, and MCP for review quality, and makes the data fence stronger. This conflicts with [hardened-agent-launches](decisions/hardened-agent-launches.md). Open: is a best-effort fence acceptable, and does the result show a notice when session data looks like instructions?
- **Fork history treatment.** Forked Codex sessions inherit parent turns, tool calls, and commands. Open: do counters mean fork-owned activity, inherited context, or both, and how does the timeline mark the fork boundary?
