import type { Session } from '@/pages/coslash/lib/session';
import type { CanvasNodeBox } from '@/plugins/canvas/shared';

export const SESSION_NODE_IDS = [
  'session',
  'goal',
  'plan',
  'timeline',
  'context',
  'changes',
  'terminal',
  'note',
  'turn',
] as const;

export type SessionNodeId = (typeof SESSION_NODE_IDS)[number];
export type SessionNodeLayout = CanvasNodeBox & Record<string, unknown>;
export type SessionCanvasLayout = Record<SessionNodeId, SessionNodeLayout>;

export type CanvasTurnDecision = { question: string; answer: string | null };
export type CanvasTurn = {
  index: number;
  prompt: string;
  planText: string | null;
  todos: { text: string; done: boolean }[];
  toolUses: number;
  errors: number;
  decisions: CanvasTurnDecision[];
  fileEdits: string[];
};

export type CanvasFileEdit = {
  path: string;
  adds: number;
  dels: number;
  edits: number;
  isNew: boolean;
  hunks: string[];
};

export type CanvasContextFile = {
  path: string;
  partial: boolean;
  totalLines: number | null;
  capturedContent: boolean;
  combinedReadId: string | null;
  segments: { startLine: number; content: string }[];
};

export type SessionCanvasDetail = Omit<Session, 'fileEdits'> & {
  fileEdits: CanvasFileEdit[];
  turnLog: CanvasTurn[];
  contextFiles: CanvasContextFile[];
  contextReadGroups: { id: string; command: string; output: string }[];
  deferredContext: { id: string; kind: string; name: string; content: string }[];
  triggeredContext: { kind: string; name: string; calls: number }[];
};

export type SessionCheckpointSnapshot = {
  turns: number;
  summary: string | null;
  openTasks: number;
  contextFiles: number;
  modifiedFiles: number;
  additions: number;
  deletions: number;
  branch: string | null;
  errors: number;
};

export type SessionExperiment = {
  id: string;
  prompt: string;
  createdAt: number;
  status: 'launching' | 'launched' | 'failed';
  error?: string;
  childSession?: { agent: string; id: string };
  terminalId?: string;
  promotedAt?: number;
  [key: string]: unknown;
};

export type SessionCheckpoint = {
  id: string;
  name: string;
  createdAt: number;
  snapshot: SessionCheckpointSnapshot;
  experiments: SessionExperiment[];
  [key: string]: unknown;
};

export type SessionCanvasWorkspace = {
  version: 1;
  layout: SessionCanvasLayout;
  checkpoints: SessionCheckpoint[];
  pinIds: string[];
  note: string;
  [key: string]: unknown;
};

export type SessionAttentionItem = {
  id: string;
  node: SessionNodeId;
  tone: 'warning' | 'error';
  title: string;
  detail: string;
};

export type SessionPinCandidate = {
  id: string;
  node: SessionNodeId;
  kind: 'Goal' | 'Outcome' | 'Task' | 'Decision' | 'Context' | 'Change';
  label: string;
  detail?: string;
};

export type TurnAnalysis = {
  intention: string;
  planSummary: string;
  status: string;
  findings: string[];
  issues: string[];
};
