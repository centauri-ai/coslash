# Frozen Cross-Task Contracts

Task 01 owns the initial version. After it merges, only the master agent may change this file.

## Backend plugin

```go
type Plugin interface {
    Register(*http.ServeMux)
    Start(context.Context) error
    Close(context.Context) error
}
```

The plugin is constructed once, registers only `/api/canvas`, `/api/dagama`, `/api/atlas`, and `/api/terminals` routes, and owns its background services.

## Frontend plugin

The Canvas frontend exports:

- `CanvasDestination = 'canvas' | 'dagama' | 'atlas'`.
- Destination navigation component.
- Destination renderer receiving sessions, freshness version, and current inspector callback.
- Session-card action receiving `{agent,id}` and current selection.
- Plugin settings/diagnostics/migration entry point.

## Session identity

Every API, storage key, UI key, and session link uses both `agent` and `id`. Never key Canvas state by `id` alone.

## Canvas APIs

- `GET /api/canvas/sessions/{agent}/{id}`
- `PUT /api/canvas/sessions/{agent}/{id}/name`
- `POST /api/canvas/sessions/{agent}/{id}/fork`
- `POST /api/canvas/sessions/{agent}/{id}/turns/{turn}/analysis`
- `GET /api/canvas/sessions/{agent}/{id}/files?path=...`
- `GET|PUT /api/canvas/workspaces/{agent}/{id}`
- `POST /api/canvas/sessions/{agent}/{id}/terminal`

## Terminal APIs

- `GET /api/terminals/{terminalId}`
- `POST /api/terminals/{terminalId}/input`
- `POST /api/terminals/{terminalId}/stop`
- `GET /api/terminals/{terminalId}/ws`

WebSocket client frames are bounded JSON messages:

```json
{"type":"input","data":"..."}
{"type":"resize","cols":120,"rows":40}
```

Server frames are terminal byte payloads. Authentication uses a dedicated WebSocket subprotocol carrying the current coSlash token; the server echoes only the static protocol name.

## Errors

```json
{
  "ok": false,
  "code": "STABLE_CODE",
  "error": "safe message",
  "field": "optional",
  "actualRevision": 2
}
```

Never return raw command output, absolute private paths not already user-visible, stack traces, or internal errors.

## Storage

- Canvas: `~/.coslash/canvas`.
- DaGama private: `~/.coslash/dagama/projects`.
- Atlas private: `~/.coslash/atlas/projects`.
- Project boards: `<project>/.coslash/{dagama,atlas}/boards`.
- Run exchange protocol: `<runRoot>/.fleetlog/run/**` until a later migration.

All writes are atomic and revisioned where concurrent browser saves are possible. Paths are canonicalized, scoped, and symlink checked.

## Workflow API compatibility

Preserve the legacy client shapes for project open, board CRUD, run preview/start/list/read, artifacts, prompts, attempt outputs, retry/trigger, terminal reconnect, cancel, takeover/handback, publish preflight, and gate decisions.
