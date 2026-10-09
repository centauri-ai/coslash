# Decisions

This folder records the load-bearing decisions of coSlash that the code cannot express on its own: the invariant, the incident or requirement that forced it, the alternatives that we rejected, and the costs that we accept. Each file tells a coding agent what must stay true and why, so that a later "simplification" does not remove a guard that exists for a reason.

## Rules

- When the details of a decision change, update its file in the same pull request as the code.
- For a new significant decision, add a new file and an index line below.
- When a decision is replaced, set `Status: Superseded by [<title>](<slug>.md)` and keep the file.
- Write each decision forward-looking. Do not add removal logs or change history.
- The slug is a permanent citation key. Never rename a merged slug.
- Cite a decision as `docs/decisions/<slug>.md` in prose and in code comments.
- Every claim needs a source: a commit short SHA, a test name, or a code path.

## Index

- [Synthesis is opt-in and reads only bounded derived facts](synthesis-opt-in-bounded-facts.md) - Read if: you change synthesis defaults, settings, prompt construction, input budgets, or synthesis accounting.
- [The loopback API trusts the per-run token, not the network location](loopback-api-access-guard.md) - Read if: you add or change an HTTP route, server startup, the token file, runtime discovery, CORS, or error messages returned to clients.
- [Hub sharing is explicit and consent binds to the exact reviewed audience](explicit-hub-sharing-consent.md) - Read if: you touch share preview, upload, retry, Hub pairing or destination, export serializers, or anything that sends session data off the machine.
- [Remote collection uses a bounded, stateless helper over the user's own SSH](bounded-ssh-helper-collection.md) - Read if: you change SSH collection, the Linux helper, its install or update, the remote cache, or remote launches.
- [Agent CLIs that coSlash launches get no inherited context and treat session text as data](hardened-agent-launches.md) - Read if: you change how coSlash starts any agent CLI (synthesis, review, send, resume, start fresh), its arguments, environment, or prompt content.
- [A session is source plus agent plus session ID, and its revision covers parsed content only](session-identity-and-revisions.md) - Read if: you add a session field, a cache, an API route, or a frontend store that keys or compares sessions, or you change revision computation.
- [Linux runs the full Local in the person's account with a file credential](linux-local-runtime.md) - Read if: you change Linux startup, the systemd unit or crontab fallback, Hub credential storage, or the Linux install command.

## Related documents

- [`docs/current-product-baseline.md`](../current-product-baseline.md) - inventory of what exists today.
- [`docs/implementation-notes.md`](../implementation-notes.md) - source layout, boundary rules, and intentional compromises.
- [`docs/vendors/`](../vendors/) - observed vendor behavior, one file per package under `collector/internal/vendors`. The `claude` package is documented in `claude-code.md`, because macOS reads `claude.md` as a `CLAUDE.md` instruction file.
- [`docs/ideas.md`](../ideas.md) - open design questions that are not decided.
