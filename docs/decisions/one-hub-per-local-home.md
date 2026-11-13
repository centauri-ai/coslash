# A Local data home pairs with one Hub; a second Hub uses a second home

- Status: Accepted
- Date: 2026-11-13
- Source: `collector/cmd/coslash/hub_api.go` (`deviceCredentialStore`), `collector/cmd/coslash/connect.go` (`StartConnectCode`), `collector/cmd/coslash/background_login_darwin.go`, `collector/cmd/coslash/background_login_linux.go` (`backgroundProfileSuffix`), `collector/cmd/coslash/background_login_windows.go`, `collector/cmd/coslash/onboarding.go` (`ensureSync`), `TestDeviceCredentialsAreIsolatedByHome`, `TestBackgroundProfilesHaveIndependentStartup`

## Decision

- A coSlash Local process and its `COSLASH_HOME` hold one Hub binding: one saved Hub address, one device credential, and one sync queue.
- To sync the same sessions to a second Hub or account, the person runs a second Local with its own `COSLASH_HOME`. Device credentials are keyed by data home (a file under the home on Linux, a keychain account that includes a hash of the home elsewhere), and on Linux each home gets its own service or `crontab` entry. The steps are in [Installation](../install.md#sync-to-two-hubs).
- Local does not hold several Hub connections in one process.

## Context

Sending session data to two Hub accounts is a rare need. Supporting it inside one Local (centauri-ai/coslash#608, then a reduced centauri-ai/coslash#609) took about 5,000 added lines: a connection registry, per-Hub workers, per-Hub consent, and a guard that stops the primary connection from rebinding its queue. The guard and the rewired primary sync loop changed behavior for every single-Hub user. A second Local with its own data home already isolated credentials, queues, and startup, and paired with a second Hub on Linux on 2026-11-13 with no code change.

## Alternatives rejected

- Several Hub connections in one Local: the cost above, carried by every user for a feature few use.
- Hub-side fan-out, where the Hub forwards a device's uploads to another account: it only covers accounts on the same Hub deployment and moves consent for the second account into Hub. Not built.

## Consequences

- Both copies scan the same agent sessions, and each runs synthesis if enabled in its own settings.
- Hub-initiated `coslash:` links, **Open coSlash Local** and **Update now** reach only the copy that owns the link handler; custom homes never register one.
- On macOS a custom home gets no login entry. On Windows pairing writes the executable to the current user's Run key without `COSLASH_HOME`, so sign-in starts the default-home copy; the second copy must be started with its custom home after sign-in.
- `StartConnectCode` saves the new Hub address before the claim, so a connect command run without the second `COSLASH_HOME` repoints the existing copy.
