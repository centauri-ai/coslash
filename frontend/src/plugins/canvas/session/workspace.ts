import type { CanvasSessionIdentity } from '@/plugins/canvas/contracts';
import type {
  SessionAttentionItem,
  SessionCanvasDetail,
  SessionCanvasLayout,
  SessionCanvasWorkspace,
  SessionCheckpoint,
  SessionExperiment,
  SessionNodeId,
  SessionPinCandidate,
} from '@/plugins/canvas/session/types';
import { SESSION_NODE_IDS } from '@/plugins/canvas/session/types';

export const SESSION_CANVAS_WORLD = { width: 1680, height: 1120 } as const;
export const SESSION_NODE_MIN_WIDTH = 240;
export const SESSION_NODE_MIN_HEIGHT = 120;

export const DEFAULT_SESSION_LAYOUT: SessionCanvasLayout = {
  session: { x: 16, y: 54, width: 300, height: 260, collapsed: false, locked: false },
  goal: { x: 350, y: 54, width: 320, height: 220, collapsed: false, locked: false },
  plan: { x: 710, y: 38, width: 320, height: 242, collapsed: false, locked: false },
  timeline: { x: 16, y: 324, width: 430, height: 210, collapsed: false, locked: false },
  context: { x: 480, y: 300, width: 320, height: 260, collapsed: false, locked: false },
  changes: { x: 835, y: 330, width: 320, height: 230, collapsed: false, locked: false },
  terminal: { x: 360, y: 540, width: 820, height: 348, collapsed: false, locked: false },
  note: { x: 360, y: 900, width: 820, height: 176, collapsed: false, locked: false },
  turn: { x: 16, y: 824, width: 720, height: 280, collapsed: false, locked: false },
};

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function finite(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
}

function integer(value: unknown, fallback = 0): number {
  return Math.max(0, Math.round(finite(value, fallback)));
}

export function normalizeSessionLayout(value: unknown): SessionCanvasLayout {
  const source = record(value) ?? {};
  return {
    ...source,
    ...Object.fromEntries(
      SESSION_NODE_IDS.map((id) => {
        const fallback = DEFAULT_SESSION_LAYOUT[id];
        const candidate = record(source[id]) ?? {};
        const width = Math.max(
          SESSION_NODE_MIN_WIDTH,
          Math.min(SESSION_CANVAS_WORLD.width, finite(candidate.width, fallback.width)),
        );
        const height = Math.max(
          SESSION_NODE_MIN_HEIGHT,
          Math.min(SESSION_CANVAS_WORLD.height, finite(candidate.height, fallback.height)),
        );
        return [
          id,
          {
            ...candidate,
            x: Math.max(0, Math.min(SESSION_CANVAS_WORLD.width - width, finite(candidate.x, fallback.x))),
            y: Math.max(0, Math.min(SESSION_CANVAS_WORLD.height - height, finite(candidate.y, fallback.y))),
            width,
            height,
            collapsed: typeof candidate.collapsed === 'boolean' ? candidate.collapsed : fallback.collapsed,
            locked: typeof candidate.locked === 'boolean' ? candidate.locked : fallback.locked,
          },
        ];
      }),
    ),
  } as SessionCanvasLayout;
}

function normalizeExperiment(value: unknown, index: number): SessionExperiment | null {
  const candidate = record(value);
  if (candidate === null || typeof candidate.prompt !== 'string') return null;
  const createdAt = finite(candidate.createdAt, 0);
  const child = record(candidate.childSession);
  return {
    ...candidate,
    id:
      typeof candidate.id === 'string' && candidate.id !== ''
        ? candidate.id
        : `stored-experiment-${index}-${createdAt}`,
    prompt: candidate.prompt,
    createdAt,
    status: candidate.status === 'failed' || candidate.status === 'launching' ? candidate.status : 'launched',
    error: typeof candidate.error === 'string' ? candidate.error : undefined,
    childSession:
      child !== null && typeof child.agent === 'string' && typeof child.id === 'string'
        ? { agent: child.agent, id: child.id }
        : undefined,
    terminalId: typeof candidate.terminalId === 'string' ? candidate.terminalId : undefined,
    promotedAt: typeof candidate.promotedAt === 'number' ? candidate.promotedAt : undefined,
  };
}

function normalizeCheckpoint(value: unknown, index: number): SessionCheckpoint | null {
  const candidate = record(value);
  const snapshot = record(candidate?.snapshot);
  if (candidate === null || snapshot === null) return null;
  const createdAt = finite(candidate.createdAt, 0);
  return {
    ...candidate,
    id:
      typeof candidate.id === 'string' && candidate.id !== ''
        ? candidate.id
        : `stored-checkpoint-${index}-${createdAt}`,
    name:
      typeof candidate.name === 'string' && candidate.name !== ''
        ? candidate.name
        : `Checkpoint ${index + 1}`,
    createdAt,
    snapshot: {
      ...snapshot,
      turns: integer(snapshot.turns),
      summary: typeof snapshot.summary === 'string' ? snapshot.summary : null,
      openTasks: integer(snapshot.openTasks),
      contextFiles: integer(snapshot.contextFiles),
      modifiedFiles: integer(snapshot.modifiedFiles),
      additions: integer(snapshot.additions),
      deletions: integer(snapshot.deletions),
      branch: typeof snapshot.branch === 'string' ? snapshot.branch : null,
      errors: integer(snapshot.errors),
    },
    experiments: Array.isArray(candidate.experiments)
      ? candidate.experiments
          .map(normalizeExperiment)
          .filter((experiment): experiment is SessionExperiment => experiment !== null)
      : [],
  };
}

export function defaultSessionWorkspace(): SessionCanvasWorkspace {
  return {
    version: 1,
    layout: normalizeSessionLayout(DEFAULT_SESSION_LAYOUT),
    checkpoints: [],
    pinIds: [],
    note: '',
  };
}

export function autoArrangeSessionLayout(layout: SessionCanvasLayout): SessionCanvasLayout {
  return {
    ...layout,
    ...Object.fromEntries(
      SESSION_NODE_IDS.map((id) => [id, { ...layout[id], ...DEFAULT_SESSION_LAYOUT[id] }]),
    ),
  } as SessionCanvasLayout;
}

export function normalizeSessionWorkspace(value: unknown): SessionCanvasWorkspace {
  const candidate = record(value);
  if (candidate?.version !== 1) return defaultSessionWorkspace();
  return {
    ...candidate,
    version: 1,
    layout: normalizeSessionLayout(candidate.layout),
    checkpoints: Array.isArray(candidate.checkpoints)
      ? candidate.checkpoints
          .map(normalizeCheckpoint)
          .filter((checkpoint): checkpoint is SessionCheckpoint => checkpoint !== null)
      : [],
    pinIds: Array.isArray(candidate.pinIds)
      ? [...new Set(candidate.pinIds.filter((id): id is string => typeof id === 'string'))]
      : [],
    note: typeof candidate.note === 'string' ? candidate.note : '',
  };
}

export function createCheckpoint(detail: SessionCanvasDetail, id: string, now: number): SessionCheckpoint {
  return {
    id,
    name: `Turn ${detail.turns} checkpoint`,
    createdAt: now,
    snapshot: {
      turns: detail.turns,
      summary: detail.summary,
      openTasks: detail.todos.filter((todo) => !todo.done).length,
      contextFiles: detail.contextFiles.length,
      modifiedFiles: detail.fileEdits.length,
      additions: detail.fileEdits.reduce((total, file) => total + file.adds, 0),
      deletions: detail.fileEdits.reduce((total, file) => total + file.dels, 0),
      branch: detail.branch,
      errors: detail.errors,
    },
    experiments: [],
  };
}

export type SessionWorkspaceAction =
  | { type: 'layout'; id: SessionNodeId; layout: SessionCanvasLayout[SessionNodeId] }
  | { type: 'toggle-collapse'; id: SessionNodeId }
  | { type: 'toggle-lock'; id: SessionNodeId }
  | { type: 'note'; value: string }
  | { type: 'toggle-pin'; id: string }
  | { type: 'add-checkpoint'; checkpoint: SessionCheckpoint }
  | { type: 'delete-checkpoint'; checkpointId: string }
  | { type: 'add-experiment'; checkpointId: string; experiment: SessionExperiment }
  | {
      type: 'finish-experiment';
      checkpointId: string;
      experimentId: string;
      patch: Partial<SessionExperiment>;
    }
  | { type: 'promote-experiment'; checkpointId: string; experimentId: string; promotedAt: number };

export function reduceSessionWorkspace(
  workspace: SessionCanvasWorkspace,
  action: SessionWorkspaceAction,
): SessionCanvasWorkspace {
  if (action.type === 'layout') {
    return { ...workspace, layout: { ...workspace.layout, [action.id]: action.layout } };
  }
  if (action.type === 'toggle-collapse' || action.type === 'toggle-lock') {
    const key = action.type === 'toggle-collapse' ? 'collapsed' : 'locked';
    const layout = workspace.layout[action.id];
    return {
      ...workspace,
      layout: { ...workspace.layout, [action.id]: { ...layout, [key]: !layout[key] } },
    };
  }
  if (action.type === 'note') return { ...workspace, note: action.value };
  if (action.type === 'toggle-pin') {
    return {
      ...workspace,
      pinIds: workspace.pinIds.includes(action.id)
        ? workspace.pinIds.filter((id) => id !== action.id)
        : [...workspace.pinIds, action.id],
    };
  }
  if (action.type === 'add-checkpoint') {
    return { ...workspace, checkpoints: [...workspace.checkpoints, action.checkpoint] };
  }
  if (action.type === 'delete-checkpoint') {
    return {
      ...workspace,
      checkpoints: workspace.checkpoints.filter((checkpoint) => checkpoint.id !== action.checkpointId),
    };
  }
  return {
    ...workspace,
    checkpoints: workspace.checkpoints.map((checkpoint) => {
      if (checkpoint.id !== action.checkpointId) return checkpoint;
      if (action.type === 'add-experiment') {
        return { ...checkpoint, experiments: [...checkpoint.experiments, action.experiment] };
      }
      return {
        ...checkpoint,
        experiments: checkpoint.experiments.map((experiment) => {
          if (experiment.id !== action.experimentId) return experiment;
          if (action.type === 'promote-experiment') {
            return { ...experiment, promotedAt: action.promotedAt };
          }
          return { ...experiment, ...action.patch };
        }),
      };
    }),
  };
}

export function reduceWorkspaceForIdentity(
  currentIdentityKey: string,
  expectedIdentityKey: string,
  workspace: SessionCanvasWorkspace,
  action: SessionWorkspaceAction,
): SessionCanvasWorkspace | null {
  return currentIdentityKey === expectedIdentityKey ? reduceSessionWorkspace(workspace, action) : null;
}

function excerpt(value: string, length = 100): string {
  const normalized = value.replace(/\s+/g, ' ').trim();
  return normalized.length > length ? `${normalized.slice(0, length - 1)}…` : normalized;
}

export function sessionAttention(detail: SessionCanvasDetail): SessionAttentionItem[] {
  const items: SessionAttentionItem[] = [];
  if (detail.status === 'waiting')
    items.push({
      id: 'waiting',
      node: 'session',
      tone: 'warning',
      title: 'Agent is waiting',
      detail: 'Review the latest turn before resuming.',
    });
  if (detail.errors > 0)
    items.push({
      id: 'errors',
      node: 'session',
      tone: 'error',
      title: `${detail.errors} logged ${detail.errors === 1 ? 'error' : 'errors'}`,
      detail: 'Inspect the failed turn before continuing.',
    });
  const openTasks = detail.todos.filter((todo) => !todo.done).length;
  if (openTasks > 0)
    items.push({
      id: 'tasks',
      node: 'plan',
      tone: 'warning',
      title: `${openTasks} open ${openTasks === 1 ? 'task' : 'tasks'}`,
      detail: 'The plan still has unfinished work.',
    });
  if (
    detail.contextTokens !== null &&
    detail.contextWindow !== null &&
    detail.contextWindow > 0 &&
    detail.contextTokens / detail.contextWindow >= 0.75
  )
    items.push({
      id: 'context',
      node: 'context',
      tone: 'warning',
      title: `Context is ${Math.round((detail.contextTokens / detail.contextWindow) * 100)}% full`,
      detail: 'Pin critical inputs before compaction.',
    });
  return items;
}

export function sessionPinCandidates(detail: SessionCanvasDetail): SessionPinCandidate[] {
  const candidates: SessionPinCandidate[] = [];
  if (detail.firstPrompt)
    candidates.push({ id: 'goal', node: 'goal', kind: 'Goal', label: excerpt(detail.firstPrompt) });
  if (detail.summary)
    candidates.push({ id: 'outcome', node: 'goal', kind: 'Outcome', label: excerpt(detail.summary) });
  detail.todos.forEach((todo, index) => {
    if (!todo.done)
      candidates.push({
        id: `task:${index}:${todo.text}`,
        node: 'plan',
        kind: 'Task',
        label: excerpt(todo.text),
      });
  });
  detail.contextFiles.forEach((file) =>
    candidates.push({ id: `context:${file.path}`, node: 'context', kind: 'Context', label: file.path }),
  );
  detail.fileEdits.forEach((file) =>
    candidates.push({
      id: `change:${file.path}`,
      node: 'changes',
      kind: 'Change',
      label: file.path,
      detail: `+${file.adds} −${file.dels}`,
    }),
  );
  detail.turnLog.flatMap((turn) =>
    turn.decisions.map((decision, index) =>
      candidates.push({
        id: `decision:${turn.index}:${index}`,
        node: 'timeline',
        kind: 'Decision',
        label: excerpt(decision.answer ?? decision.question),
        detail: `Turn ${turn.index}`,
      }),
    ),
  );
  return candidates;
}

export function sessionKey(identity: CanvasSessionIdentity): string {
  return `${identity.agent}\0${identity.id}`;
}
