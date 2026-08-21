# Mac-only SSH redesign — master plan

Status: approved for implementation on 2026-08-21. The product boundary and
measurement-driven guardrails are locked in
[`../03-mac-only-remote-design.md`](../03-mac-only-remote-design.md).

## Goal

Replace the completed Linux-collector implementation with Mac-side SFTP reads and
Mac-side Claude/Codex parsing. The Linux host must not install or run coSlash.

## Constraints

- Exactly one optional SSH alias and one remote source in v1.
- Remote v1 supports Claude Code and Codex; local OpenCode behavior is unchanged.
- Use system OpenSSH configuration and authentication; store no SSH credentials.
- No remote coSlash command, binary, daemon, cache, or release artifact.
- Raw remote files are streamed into bounded Mac memory and are never persisted.
- Missing liveness/Git facts remain unknown.
- Remote Resume and Start Fresh are out of scope unless the user changes the
  monitoring-first decision before implementation.

## Dependency flow

```text
P1 parser seam + read-only SFTP transport
  -> P2 collection manager + cache + API
    -> P3 frontend monitoring experience
      -> P4 removal, docs, and integration hardening
```

Four review PRs are the target. There is no separate umbrella PR unless review
coordination later requires one.

## Packets

| Packet | Plan | Status | Depends on | Review outcome |
|---|---|---|---|---|
| P1 | [`01-parser-and-sftp.md`](01-parser-and-sftp.md) | Done locally | — | A tested read-only SFTP source and shared Claude/Codex parser boundary |
| P2 | [`02-manager-cache-api.md`](02-manager-cache-api.md) | Done locally | P1 | Remote normalized facts flow through cache and source-aware API |
| P3 | [`03-frontend-monitoring.md`](03-frontend-monitoring.md) | Done locally | P2 | Honest monitoring UX with remote launch disabled |
| P4 | [`04-cleanup-docs-hardening.md`](04-cleanup-docs-hardening.md) | Done locally | P3 | Linux collector surfaces removed and end-to-end behavior verified |

## Reuse and replacement map

| Current feature area | Treatment |
|---|---|
| Remote settings and stable source identity | Reuse with revised compatibility fields |
| Manager one-flight lifecycle, backoff, last-good cache | Reuse structure; replace snapshot runner/decoder |
| Source-aware API keys and local/remote collision handling | Reuse |
| Machine badge, host strip, settings, diagnostics, stale state | Reuse and simplify |
| Linux snapshot/probe and `remoteview/v1` frame | Remove |
| Linux launch/handoff lifecycle | Remove |
| Linux builds, smoke job, installation guide | Revert/remove feature-specific additions |
| Vendor discovery/parsing tied to `os`/`exec` | Refactor behind a read-source seam |
| Remote Resume / Start Fresh | Hide; Copy handoff remains |

## Gates

| Gate | Condition |
|---|---|
| G0 — scope locked | Complete: monitoring-first and measurement-driven guardrails approved 2026-08-21 |
| G1 — safe data plane | Complete locally: P1 proves read-only SFTP, containment, bounded reads, and parser equivalence |
| G2 — backend complete | Complete locally: P2 proves last-good behavior, cancellation, API identity, and no raw cache |
| G3 — honest product | Complete locally: P3 never claims unsupported liveness/Git/launch behavior |
| G4 — replacement complete | Complete locally: no Linux install/release path remains; full local, fake-host, and real-host checks pass |

## Verification baseline

Each packet runs focused tests. P4 additionally runs:

```text
cd collector
gofmt -l ./cmd ./internal
go vet ./...
go test ./...
go test -race ./internal/remote/...

cd ../frontend
npm run lint
npm test -- --run
npm run format:check
npm run build
```

A final manual check used a configured proxy-backed OpenSSH alias.
The bounded preflight completed in about 6.0 seconds, and background refresh
returned one normalized Codex session in about 5.4 seconds without invoking a
Linux coSlash command. Automated coverage uses a fake SSH/SFTP server and an
injected process boundary for failure cases.

## Risks

| Risk | Mitigation |
|---|---|
| Large append-only transcripts consume bandwidth | Fingerprints, recent-first selection, hard byte/time caps, measurement gate |
| SFTP path traversal or symlink escape | Canonical allowlisted roots, reject symlinks, read-only interface |
| Liveness becomes misleading | Separate recency from liveness; unknown is a first-class state |
| Parser refactor regresses local sessions | Run identical fixtures through OS and in-memory/SFTP-like sources |
| SSH child leaks after cancellation | Own pipes/process in one session object; deadline and shutdown tests |
| Old Linux feature remains accidentally reachable | P4 deletion tests and release/docs assertions |
