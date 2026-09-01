# T05 — Manager, setup UI, diagnostics, and documentation

Status: not_started

Depends on: T02, T03, T04

## Objective

Integrate helper-first collection with explicit SFTP fallback and expose the full
setup, consent, compatibility, health, and lifecycle experience coherently from
backend through UI and documentation.

## Context

`remote.Manager` currently starts collection from `ApplySettings`, owns
freshness/backoff, and publishes composed snapshots. The Machines UI currently
states that Linux runs no coSlash code. The new flow must wait for the requested
window, select only a verified compatible helper, preserve cached cards during
refresh, and accurately explain optional installation and degraded SFTP.

Read:

- `collector/internal/remote/{manager,health}.go`
- `collector/cmd/coslash/api_remote.go`
- `frontend/src/pages/coslash/components/MachinesSettingsSection.tsx`
- `frontend/src/pages/coslash/lib/{machines,remote-api,host-strip}.ts`
- `settings.schema.json`, `README.md`, `docs/data-and-privacy.md`, and
  `docs/troubleshooting.md`

All new HTTP routes must remain behind `internal/httpsec.Guard`, and frontend API
calls must use `apiFetch`.

## Scope

### Manager and API

- Introduce one transport-independent result boundary for helper and SFTP.
- Use helper only when installed/verified/compatible; define which missing or
  lifecycle states permit SFTP fallback. Do not mask helper data/protocol errors
  with silent fallback.
- Wait for first `ListView` window before initial collection.
- Merge cache generations, then compose/filter for the UI.
- Keep last good cards visible; mark retained failed families stale.
- Preserve vendor isolation, retry/backoff, and manual retry semantics.
- Add stable helper/lifecycle/protocol/resource/partial-family reasons and safe
  metrics: transport, versions, timings, bytes, counts, and coverage.

### Setup UI and documentation

- Flow: test SSH, report platform/helper state, explain install, request consent,
  install/verify, then run a small collection test.
- Show active transport, helper version/compatibility, degraded SFTP limits, and
  partial/stale/truncated coverage.
- Add explicit install, upgrade/repair, retry, and exact uninstall actions.
- Disabling a host retains the helper and states that clearly. Removing a host
  first offers “remove and uninstall helper” and “remove host only”; uninstall
  completes before local alias/settings deletion. If it fails, retain enough
  local configuration to retry or let the user explicitly choose removal only.
- Prompt for deprecated helpers before their compatibility window expires. Never
  silently run incompatible/revoked helpers or silently hide the state behind
  SFTP fallback.
- Extend backend/frontend types and exhaustive decoders together.
- Update settings schema, README, privacy, and troubleshooting promises.
- Never display raw stderr; show bounded generic copy and safe structured facts.

## Acceptance criteria

- First collection uses the requested UI window, never accidental `since=0`.
- Helper, fallback, partial response, and stale-cache flows preserve valid cards.
- No incomplete scan removes a family or refresh loops indefinitely connecting.
- Users consent before Linux code is installed and can see reuse versus upgrade.
- Declined/blocked setup remains usable and honestly labeled.
- Disable, remove-only, successful remove-and-uninstall, and failed-uninstall
  flows leave no ambiguous remote-helper ownership state.
- Docs match actual installation, data flow, limits, verification, and removal.

## Focused tests

Run table-driven remote manager/API tests with fake transports, touched Vitest
files, and the TypeScript build only if shared types changed. Do not run complete
backend/frontend suites.

## Out of scope

No unrelated settings redesign, general multi-host expansion, or rollout enablement.
