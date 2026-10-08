# Remote collection uses a bounded, stateless helper over the user's own SSH

- Status: Accepted
- Date: 2026-09-01
- Source: e55901e9, d033bdb8, e4c9a882, 4ed47ce0, 27a5495d

## Decision

- The Mac is the only UI and the only owner of settings, cache, composition, and health. The Linux host runs `coslash-helper` only for the duration of one request. The helper has no daemon, no listener, no inbound port, no root access, no network access, and no state between runs.
- coSlash connects through the system `ssh` client and the SSH configuration of the user. It never stores SSH passwords or private keys.
- The helper reads only a fixed allowlist of paths. No path, command, prompt, or other input from the Mac can choose which files the helper opens.
- With the helper, transcript parsing, liveness checks, and Git inspection run on the Linux host. Raw transcript rows never leave the host. Bounded parsed records cross SSH into the private local cache.
- Without an installed helper, coSlash uses the read-only SFTP fallback. Transcript files then cross SSH, and the Mac parses them. A helper that fails verification or refresh does not fall back to SFTP.
- The first helper installation needs an explicit user action. Later releases can replace only a helper that coSlash installed and verified before. Builds without authenticated embedded helper assets never upload a helper.
- coSlash never interpolates transcript content, remote paths, or handoff text into an SSH shell command.
- A refresh that is interrupted, has malformed protocol output, cannot enumerate families, or exceeds its bounds keeps the last good generation and marks it stale. With helper collection, invalid transcript data in an attributed family retains that family's last good records as stale while healthy families refresh; the helper response must still complete with an authoritative inventory. An absent session counts as deleted only after the authoritative completion evidence of the protocol.
- One unreachable or misconfigured host never blocks local sessions.

## Context

Many users run their main agent sessions on Linux servers but work from a Mac. Process liveness, working directories, and Git state are only correct on the machine where the session runs.

The helper is the only code that coSlash runs on a machine it does not own. A narrow, read-only, stateless helper keeps that footprint small and easy to verify. If a partial refresh replaces the cache, sessions disappear and then come back. Thus the cache publishes only complete generations.

The first remote handoff put the handoff text, base64-encoded, into the SSH command. This tied reliability to command-length limits and exposed the content in process listings.

## Alternatives rejected

We rejected these designs:

- SSHFS or a mount of the remote home. It loses reliable process and Git state.
- Copying transcript files to the Mac and parsing them locally as the primary design. Same reason, plus raw transcripts leave the host. This path remains only as the fallback for hosts without a helper.
- A full HTTP server on the remote host. It adds ports, authentication, and lifecycle management.
- Credential fields in coSlash settings.

We also rejected handoff payloads in the SSH command line (27a5495d).

## Consequences

- Only one SSH source is supported. The first release supports one host, not many.
- Remote collection supports Claude Code and Codex on Linux amd64 and arm64 only. The other vendors have no remote collection.
- The user sees stale data during an outage instead of an empty list.
- Disable keeps the last good cache. Remove deletes local configuration and cache but leaves the helper on the host.
- Contracts: [`remotehelper`](../../collector/internal/remotehelper/README.md), [`remoteprotocol`](../../collector/internal/remoteprotocol/README.md), and [`remotefacts`](../../collector/internal/remotefacts/README.md) READMEs. The path allowlist and ceilings are in [`docs/data-and-privacy.md`](../data-and-privacy.md). Do not copy them here.
- Enforcement: `collector/internal/remote`, `collector/internal/remotehelper`, `collector/internal/remoteprotocol`, `collector/internal/remotefacts`, `collector/internal/remoteinstall`, and `collector/cmd/coslash-helper`.
