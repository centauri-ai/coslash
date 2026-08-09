# Decision Log

Only the master agent edits this file. Add a dated entry whenever scope, contracts, ordering, or behavior changes.

## Accepted decisions

| ID    | Decision                                                                              | Reason                                                                |
| ----- | ------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| D-001 | Implement Canvas as a compile-time module, not a runtime loader or companion service. | Keeps the single binary and limits changes to coSlash core.           |
| D-002 | Use separate task branches/worktrees and master-owned integration files.              | Allows safe parallel work.                                            |
| D-003 | Use `{agent,id}` as session identity.                                                 | Prevents Claude/Codex ID collisions.                                  |
| D-004 | Replace ttyd with native PTY/WebSocket attached to tmux.                              | Fits coSlash authentication and removes random unauthenticated ports. |
| D-005 | Use coSlash synthesis CLIs for turn analysis; do not restore Azure credentials.       | Avoids browser secrets and aligns with main.                          |
| D-006 | Import old nonterminal runs as interrupted history.                                   | Prevents duplicate agent execution.                                   |
| D-007 | Keep `.fleetlog/run/**` inside run roots during the first port.                       | Avoids a high-risk cross-cutting protocol rename.                     |
| D-008 | Persist new Canvas workspace state server-side.                                       | Avoids future browser-origin migration problems.                      |
| D-009 | Keep Columbus Canvas, Daily Digest, and arbitrary Atlas execution out of scope.       | Maintains a bounded migration.                                        |
| D-010 | On 2026-08-09, pin `github.com/coder/websocket v1.8.15` and `github.com/creack/pty v1.1.24` for Task 04; add no npm dependency. | Supplies context-aware WebSockets and Unix PTY start/resize with the smallest backend-only dependency surface. |
| D-011 | On 2026-08-09, ratify `coslash.terminal.v1` as the echoed terminal WebSocket subprotocol and `coslash.token.` as the non-echoed token-bearing prefix. | Matches the guarded Task 02 implementation and prevents the credential-bearing protocol value from being echoed. |
| D-012 | On 2026-08-09, accept locally verified `review` results for Tasks 00–08 and 11 and mark them complete after integration into local `hlu/canvas-migration` SHA `01aa158`. | The operator explicitly chose forward progress; later findings remain fixable on owning branches before final release. |
