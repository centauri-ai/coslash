# P3 — Mac-only remote monitoring experience

Status: done locally on 2026-08-21.

## Purpose

Adapt the existing remote UI to the SFTP-backed contract and present only facts
the Mac-side parser can prove.

## Required outcome

- Keep source-aware identity, machine badges, one-host settings, host strip,
  retry, stale cards, diagnostics, and removal confirmation.
- Replace collector install/upgrade/agent-CLI states with SFTP connection,
  permission, missing-data, partial-agent, limited, stale, and ready states.
- Show transcript recency separately from liveness; render unknown liveness without
  implying stopped, idle, waiting, or active.
- Keep remote Copy handoff.
- Hide remote Resume and Start Fresh, Commands, diff, synthesis, preview, and share
  actions. Local actions remain unchanged.
- Update settings help to say that Linux needs SSH/SFTP and readable agent data,
  with no coSlash installation.

## Acceptance

- Local and remote sessions with the same agent ID remain independently selectable,
  sorted, filtered, inspected, and copied.
- No remote UI text mentions installing/upgrading a collector or missing a remote
  coSlash binary.
- Unknown liveness and transcript recency have distinct fixtures and accessible
  copy; stale host state remains distinct from either.
- Remote action controls cannot issue launch/diff/synthesis/preview/share requests.
- Disable, retry, edit-alias replacement, and confirmed remove preserve the P2
  lifecycle contract.
- Frontend unit tests cover every machine state and action gate.

## Verification

From `frontend/`:

```text
npm run lint
npm test -- --run
npm run format:check
npm run build
```

## Handoff to P4

List every remaining Linux-collector string, route, source file, build target, and
document that P4 must remove.

## Implemented locally

- Session selection, sorting, cards, inspector state, file selection, and handoff
  copy use the complete source-aware identity, so local and remote IDs cannot
  collide.
- Machine badges, SFTP health strip, retry, diagnostics, one-host settings,
  connection test, disable, and confirmed removal use the P2 contract.
- Remote null process status is labeled `Liveness unknown`; transcript recency
  and stale machine state remain separate labels.
- Remote Resume, Start Fresh, Commands, diff, synthesis, preview, and Hub sharing
  controls are hidden or locally gated. Request builders also reject remote
  launch/diff/synthesis requests. Copy handoff remains available.
- Settings say Linux needs only SSH/SFTP and readable Claude/Codex files and must
  not install coSlash.

Frontend lint, all 61 tests, format verification, and the production TypeScript
build pass locally. The only lint output is the pre-existing Fast Refresh warning
for `SessionSortDropdownMenu.tsx` exports.

P4 must still search historical/release/docs surfaces for `remoteview`, framed
snapshot/probe commands, remote launch/handoff storage, Linux artifacts, and old
remote installation guidance, then remove anything still reachable.
