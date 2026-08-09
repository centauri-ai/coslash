import type {
  CanvasApiFailure,
  CanvasBoardDocument,
  CanvasRunDocument,
  CanvasWorkspaceDocument,
  TerminalInputFrame,
  TerminalResizeFrame,
} from '@/plugins/canvas/contracts';

export const FROZEN_CANVAS_CONTRACT_FIXTURES = {
  error: {
    ok: false,
    code: 'REVISION_CONFLICT',
    error: 'the workspace changed in another client',
    field: 'expectedRevision',
    actualRevision: 2,
  } satisfies CanvasApiFailure,
  terminalInput: { type: 'input', data: "printf 'ready\\n'" } satisfies TerminalInputFrame,
  terminalResize: { type: 'resize', cols: 120, rows: 40 } satisfies TerminalResizeFrame,
  workspace: {
    schemaVersion: 1,
    revision: 2,
    session: { agent: 'codex', id: 'session-1' },
    state: { layout: {}, pins: [] },
    updatedAt: '2026-08-08T20:00:00Z',
  } satisfies CanvasWorkspaceDocument,
  board: {
    schemaVersion: 1,
    id: 'board-1',
    name: 'Migration board',
    revision: 3,
    createdAt: '2026-08-08T19:00:00Z',
    updatedAt: '2026-08-08T20:00:00Z',
    board: { stages: [] },
  } satisfies CanvasBoardDocument,
  run: {
    schemaVersion: 1,
    runId: 'run-1',
    projectId: 'project-1',
    boardId: 'board-1',
    boardRevision: 4,
    title: 'Migration run',
    status: 'running',
    sessions: [
      { agent: 'claude', id: 'shared-id' },
      { agent: 'codex', id: 'shared-id' },
    ],
    createdAt: '2026-08-08T19:00:00Z',
    updatedAt: '2026-08-08T20:00:00Z',
    finishedAt: null,
  } satisfies CanvasRunDocument,
} as const;
