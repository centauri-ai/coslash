# Linux runs the full Local in the person's account with a file credential

- Status: Accepted
- Date: 2026-10-08
- Source: `collector/cmd/coslash/background_login_linux.go`, `collector/internal/hubclient/credential_file.go`, `TestSystemdUnitQuotesExecutableAndCarriesDataLocations`, `TestBackgroundPersistenceNeedsLingerForSystemd`, `TestFileCredentialStoreRoundTripIsPrivate`

## Decision

- A Linux host can run the full coSlash Local and pair directly with Hub, the same as macOS and Windows. Hub's install command carries only a single-use connect code; on a server the person runs it in their own SSH session, so Hub never receives or stores SSH credentials. Pairing still completes only after the person approves the named host in Hub.
- On Linux the device credential is a `0600` file at `$COSLASH_HOME/hub-credentials/<hub host>`, written atomically, never a Secret Service keyring.
- Local uses a `systemctl --user` unit (`coslash.service`, `Restart=always`, `KillMode=process`) only when the account lingers or can enable lingering. Without lingering the user manager stops when the last SSH session ends and would take Local with it, and some systems refuse `loginctl enable-linger` to a remote session without `sudo` (Ubuntu 24.04 in a container did; the Google Cloud Ubuntu 24.04 image allows it). Local then runs as a detached process and adds a `crontab` `@reboot` entry. Without either it keeps running and tells the person to start it from their init system.
- Each account on a shared server runs its own Local. Port 8787 is only a default: when another process holds it and `--port` was not given, Local binds a free loopback port, and the CLI finds it through `runtime.json`.
- `coslash connect` starts Local through the unit when it can, so supervision applies from the first launch, and enables boot start only after pairing.
- The unit carries the data-location variables of the shell that connected (`PATH`, `COSLASH_HOME`, XDG and agent overrides) because the user manager does not read shell profiles.

## Context

Most Linux users reach Linux over SSH. The [bounded SSH helper](bounded-ssh-helper-collection.md) collects from a host through a Mac or PC that must stay online and covers Claude Code and Codex only. A host that runs Local itself collects every supported agent, keeps liveness and Git state where the sessions run, and syncs on its own.

Headless hosts have no Secret Service. `secret-tool store` failed after Hub had already exchanged the authorization, which left a device on Hub that Local could never use. Even on a desktop, a keyring that is locked after an unattended reboot reads as "not paired".

Under systemd the automatic-update helper is a child in the service's cgroup. The default `KillMode=control-group` would kill it when the old process exits, and its readiness wait is 30 seconds, so the restart delay is 5 seconds.

## Alternatives rejected

- Hub stores SSH credentials and installs remotely: the server would hold keys to customer machines.
- Keyring with a file fallback: a locked keyring after reboot is indistinguishable from "not paired", so the fallback would hide the failure instead of avoiding it.
- A system-wide service installed with `sudo`: the install would need root, and the service would run outside the person's home and agent data.

## Consequences

- Anyone who can read the person's home on that host can read the device credential, as with SSH keys. Revoking the device in Hub invalidates it.
- Changing an agent data variable after connecting needs a reconnect so the unit picks it up.
- Enforcement: `collector/cmd/coslash/background_login_linux.go`, `collector/cmd/coslash/hub_api.go` (`deviceCredentialStore`), `collector/internal/hubclient/credential_file.go`.
