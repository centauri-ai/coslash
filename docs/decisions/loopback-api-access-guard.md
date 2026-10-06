# The loopback API trusts the per-run token, not the network location

- Status: Accepted
- Date: 2026-08-05
- Source: 70c8a1ec, 9d97ad20, 2b868d66

## Decision

- The collector listens only on IPv4 loopback.
- Every route, including the embedded frontend, goes through `internal/httpsec.Guard`. The guard accepts only the expected loopback host and the origin of the coSlash page.
- Every `/api/` route requires the access token. The collector creates a new token on each start and writes it to `COSLASH_HOME/token` with current-user permissions.
- No route sends CORS headers. Clients get fixed, non-sensitive error messages. The specific cause stays in the server log.
- The threat model includes web pages in the browser of the user and other devices on the network. It does not include other processes that run as the same OS user. These processes can read the token file, and coSlash accepts this.

## Context

The API exposes private transcript data and can start agent sessions (launch, resume, send, review). A web page in the browser of the user can send requests to `127.0.0.1` even though a remote host cannot. Thus a loopback bind alone is not an authorization boundary (comment in `internal/httpsec`). The requirement is that unrelated websites and unexpected hosts cannot read transcripts or trigger launches.

The token is per run so that a leaked value stops working after a restart. The host and origin checks block DNS-rebinding and cross-site requests that do not know the token.

If someone "simplifies" this design, these failures come back:

- A route outside the guard, or a CORS header, lets any open web page read transcripts or start an agent.
- A route without the token check lets a cross-site form post trigger a launch.
- A detailed error message can leak paths or transcript text to the caller.

## Consequences

- Users must not proxy or forward the port. [`docs/data-and-privacy.md`](../data-and-privacy.md) states this.
- Same-user malware can use the API. A stronger boundary needs OS-level isolation, which coSlash does not attempt.
- CLI subcommands and plugin skills find the running server through local discovery files and the token file. They must read the token the same way the browser does.
- On Windows, the token and credentials need a protected current-user ACL and a secured file handle (9d97ad20, 2b868d66). POSIX file modes alone are not sufficient there.
- Enforcement: `collector/internal/httpsec`, the token and runtime-lock code in `collector/cmd/coslash`, and `apiFetch` in the frontend. [`collector/AGENTS.md`](../../collector/AGENTS.md) holds the route rules under "HTTP security".
