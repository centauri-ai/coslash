import { sessionKey, type SessionIdentity, type VendorKey } from '@/pages/coslash/lib/session';

export type DirectedHandoff = {
  id: string;
  sourceId: string;
  sourceAgent: string;
  sourceSessionId: string;
  targetAgent: VendorKey;
  targetSessionId?: string;
  kind: 'review' | 'custom';
  status: 'running' | 'completed' | 'failed';
  activity?: 'starting' | 'working' | 'needs_input' | 'not_detected';
  createdAt: number;
  result?: string;
  error?: string;
};

export type HandoffTarget = { id: VendorKey; label: string };

export type HandoffSelection = {
  selectedSessionKey: string | null;
  pendingTargetKey: string | null;
};

type SelectionAction =
  | { type: 'select'; key: string | null }
  | { type: 'pending'; key: string }
  | { type: 'found'; key: string }
  | { type: 'clear-missing'; key: string };

export function handoffSelection(state: HandoffSelection, action: SelectionAction): HandoffSelection {
  if (action.type === 'select') return { selectedSessionKey: action.key, pendingTargetKey: null };
  if (action.type === 'pending') return { ...state, pendingTargetKey: action.key };
  if (action.type === 'found')
    return state.pendingTargetKey === action.key
      ? { selectedSessionKey: action.key, pendingTargetKey: null }
      : state;
  return state.selectedSessionKey === action.key ? { ...state, selectedSessionKey: null } : state;
}

export function handoffSourceKey(handoff: DirectedHandoff): string {
  return sessionKey({ sourceId: handoff.sourceId, agent: handoff.sourceAgent, id: handoff.sourceSessionId });
}

export function newestHandoffs(handoffs: readonly DirectedHandoff[]): Map<string, DirectedHandoff> {
  const newest = new Map<string, DirectedHandoff>();
  for (const handoff of handoffs) {
    const key = handoffSourceKey(handoff);
    if (!newest.has(key)) newest.set(key, handoff);
  }
  return newest;
}

export function handoffLabel(handoff: DirectedHandoff): string {
  const agent =
    (
      { claude: 'Claude Code', codex: 'Codex', opencode: 'OpenCode', cursor: 'Cursor CLI' } as Record<
        string,
        string
      >
    )[handoff.targetAgent] ?? handoff.targetAgent;
  if (handoff.status === 'failed') return `${agent} failed`;
  if (handoff.status === 'completed')
    return handoff.kind === 'review' ? `${agent} review complete` : `${agent} responded`;
  if (handoff.activity === 'needs_input') return `${agent} needs input`;
  if (handoff.activity === 'not_detected') return `${agent} not detected`;
  if (handoff.activity === 'working') return `${agent} working`;
  return `Starting ${agent}`;
}

export function handoffTargetsPath(source: SessionIdentity): string {
  return `/api/directed-handoffs/targets?${new URLSearchParams({ source: source.sourceId })}`;
}
