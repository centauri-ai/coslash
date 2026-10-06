# Agent CLIs that coSlash launches get no inherited context and treat session text as data

- Status: Accepted
- Date: 2026-09-28
- Source: 27a5495d, ab8ead40, 4f4f8be3, 18b03dbd, 741b3a39

## Decision

- Automated agent runs that coSlash owns (synthesis and background review) start with project hooks, plugins, MCP configuration, and user rules disabled. They run read-only or without tools, as the vendor CLI allows. `collector/internal/synthesis/runner.go` and `collector/internal/launch` hold the per-vendor flags.
- Transcript text, repository content, synthesis output, remote host output, and Hub responses are untrusted. When they enter a prompt, they go inside an explicit `BEGIN UNTRUSTED ...` and `END UNTRUSTED ...` block with an instruction never to follow text inside it.
- Untrusted or unbounded content goes to the agent through stdin or a private temporary file. It never goes into argv. A positional user prompt comes after `--`, so message text cannot become a flag.
- A handoff goes to the new agent through a private file that coSlash deletes after the launch. The terminal command text contains only the file path. For a remote launch, the SSH command never contains the handoff. Local Codex launches are a known exception, listed in [`docs/implementation-notes.md`](../implementation-notes.md).
- A launched agent never inherits the session markers of the agent that started coSlash. coSlash removes an exact list of marker names from the environment of the child only. It never removes by prefix and never changes the environment of the app.
- A handoff has a fixed size limit. When a handoff is too large, coSlash first omits the timeline section. It does not raise the limit.

## Context

Argv is readable by other local processes and has an OS size limit. The first remote handoffs were in the SSH command line, which exposed them in process listings and failed for large handoffs.

The app, when started from inside a Claude Code session, kept `CLAUDE_CODE_CHILD_SESSION` and the parent session ID. A Claude session that `send` started then had transcript saving off, so coSlash never saw it. `CLAUDE_CODE_MESSAGING_TOKEN`, a token of the parent session, also reached every launched agent.

A Claude reviewer followed an instruction planted in the reviewed session, even inside the untrusted block. Prompt fencing reduces this risk but does not remove it. Thus coSlash also removes the capabilities that a planted instruction can use.

Long sessions produced handoffs over 64 KiB, and their launches failed with no output. The fix omits the timeline (741b3a39).

## Alternatives rejected

- Removing environment variables by prefix. `CLAUDE_CODE_USE_FOUNDRY`, `CODEX_HOME`, and credential variables share the `CLAUDE_CODE_` and `CODEX_` prefixes.
- Removing the markers from the app process at startup.
- Encoding the handoff into the SSH remote command.
- Raising the handoff limit as the fix for oversized handoffs. A multi-day session can exceed any fixed limit.

## Consequences

- Reviewers cannot use the installed review skills, plugins, or MCP servers of the user. Restoring them for review only is an open question in [`docs/ideas.md`](../ideas.md).
- Grok synthesis reads its prompt from a private file instead of stdin, because of the Grok CLI interface.
- On Windows, the Grok sandbox does not enforce a filesystem boundary for reviews. [`docs/data-and-privacy.md`](../data-and-privacy.md) warns the user.
- For remote reviews, the existing read access of the SSH account is the accepted boundary ([`collector/AGENTS.md`](../../collector/AGENTS.md)).
- Interactive Resume and Start fresh launch the normal CLI of the user with its normal configuration. This decision covers what coSlash passes to them, not their later behavior.
- When a vendor adds a new session marker, add the exact name to the list in `collector/internal/agentexec`. Record the observed vendor version in the matching file in `docs/vendors/`.
- Enforcement: `collector/internal/agentexec`, `collector/internal/launch`, `collector/internal/synthesis`, `collector/internal/review`, and `collector/internal/handoff`.
