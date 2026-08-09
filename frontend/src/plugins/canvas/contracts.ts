export type CanvasDestination = 'canvas' | 'dagama' | 'atlas';

export type CanvasSessionIdentity = {
  agent: string;
  id: string;
};

export type CanvasApiFailure = {
  ok: false;
  code: string;
  error: string;
  field?: string;
  actualRevision?: number;
};

export type TerminalInputFrame = { type: 'input'; data: string };
export type TerminalResizeFrame = { type: 'resize'; cols: number; rows: number };
export type TerminalClientFrame = TerminalInputFrame | TerminalResizeFrame;

export type TerminalStatus = {
  terminalId: string;
  state: string;
  writable: boolean;
};

export type CanvasWorkspaceDocument<State = unknown> = {
  schemaVersion: number;
  revision: number;
  session: CanvasSessionIdentity;
  state: State;
  updatedAt: string;
};

export type CanvasWorkspaceWrite<State = unknown> = {
  schemaVersion: number;
  expectedRevision: number;
  state: State;
};

export type CanvasBoardDocument<Board = unknown> = {
  schemaVersion: number;
  id: string;
  name: string;
  revision: number;
  createdAt: string;
  updatedAt: string;
  board: Board;
};

export type CanvasRunDocument = {
  schemaVersion: number;
  runId: string;
  projectId: string;
  boardId: string;
  boardRevision: number;
  title: string;
  status: string;
  sessions?: CanvasSessionIdentity[];
  createdAt: string;
  updatedAt: string;
  finishedAt: string | null;
};
