# Personal Hub sync uses paired-device consent

- Status: Accepted
- Date: 2026-10-19
- Source: `collector/cmd/coslash/main.go`, `collector/internal/syncv4/runner.go`, `collector/internal/syncv4/plan.go`, `collector/internal/syncv4/curated_test.go`
- Supersedes: [Hub sharing is explicit and consent binds to the exact reviewed audience](explicit-hub-sharing-consent.md)

## Decision

- Local collection works without a Hub account. Pairing a device authorizes personal background sync under the Hub policy and Local settings.
- The sync worker starts by default, but uploads require a paired credential and a compatible Hub import plan. Each upload needs no separate preview approval.
- The default import covers recent activity from three days, capped at 30 sessions. Older history requires an explicit per-device backfill request.
- A one-time backfill covers the selected window. Completion returns the device to live activity without another automatic history import.
- Local pause, Hub pause, device-off policy, leave-out rules, and network or battery conditions restrict uploads. Failed check-ins or stale consent stop uploads.
- Device revocation removes the stored credential and stops sync. Pairing or changing the Hub resets upload state for the new binding.
- V4 transfers only parsed session-record artifacts for supported local Codex, Claude, OpenCode, and Cursor families. Other artifact kinds remain in the private local spool.
- Parsed records can contain prompts, commands, source snippets, working directories, and derived synthesis. File-edit path sanitization does not redact secrets from record contents.
- Personal sync does not authorize workspace sharing or create a workspace share link. Workspace sharing requires separate authorization for its destination audience.
- Local no longer offers the V3 complete-backup preview and upload flow. This client change does not delete existing Hub backups or workspace links.

## Context

The previous decision required a preview and approval for each Local upload.
Personal sync instead keeps a paired device current under a bounded import plan.
Its authorization belongs to device setup, Hub policy, and Local pause controls.
The privacy disclosure must explain that future eligible activity can upload
without another preview.

Selecting parsed records limits the artifact types that leave the machine.
It does not make their contents free of secrets.
Local collection, personal sync, workspace sharing, synthesis, and remote MCP
have separate authorization boundaries.

## Alternatives rejected

- Per-upload approval for personal sync: it prevents unattended updates from a paired device.
- Pairing as workspace-sharing consent: it does not identify or authorize a workspace audience.
- Uploading every spool artifact: complete capture retains source material that the personal sync contract does not require.

## Consequences

- The user-facing disclosure lives in [data and privacy](../data-and-privacy.md). It must describe automatic uploads after pairing and the available pause controls.
- The import-plan and retry contract lives in [`collector/internal/syncv4/README.md`](../../collector/internal/syncv4/README.md).
- `TestDefaultPlanDoesNotUploadOlderHistory` and `TestCompletedBackfillReturnsToLiveOnlyActivity` enforce the import boundary.
- `TestScaleHubWithoutPlanCreatesOrListsNothing`, `TestLocalPauseWinsOverFreshServerPolicy`, and `TestPausedOrOfflineCheckInSendsNoUpload` enforce upload gates.
- `TestCuratedArtifactsSelectsOnlyParsedRecordsForSupportedAgents` enforces artifact selection. `TestSanitizeRecordFilePathsForSupportedReaders` covers file-edit path sanitization.
- `TestRevokedCredentialIsDeletedAndCallsStop` covers revocation. Queue binding and startup behavior live in `collector/internal/syncv4` and `collector/cmd/coslash`.
- Hub V3 routes and stored data remain a server compatibility dependency until a separate migration or sunset decision.
