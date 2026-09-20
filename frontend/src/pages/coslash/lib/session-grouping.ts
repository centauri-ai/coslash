import {
  boardStatusKey,
  getVendor,
  sessionReadiness,
  STATUS_ORDER,
  STATUSES,
  type Session,
} from '@/pages/coslash/lib/session';

export type BoardGroupBy = 'status' | 'readiness' | 'repo' | 'branch' | 'agent' | 'machine';
export type BoardRowGroupBy = BoardGroupBy | 'none';

export type BoardGroup = { key: string; label: string; title?: string; sessions: Session[] };

type Dimension = {
  label: string;
  of: (session: Session) => { key: string; label: string; title?: string };
  /** Fixed left-to-right order; dimensions without one follow the sorted session order. */
  order?: readonly string[];
};

const READINESS_ORDER = ['resume', 'review', 'fresh', 'unavailable'] as const;

function plain(value: string | null | undefined, fallback: string): { key: string; label: string } {
  const trimmed = value?.trim();
  return trimmed ? { key: trimmed, label: trimmed } : { key: fallback, label: fallback };
}

const DIMENSIONS: Record<BoardGroupBy, Dimension> = {
  status: {
    label: 'Status',
    order: STATUS_ORDER,
    of: (session) => {
      const key = boardStatusKey(session);
      return { key, label: STATUSES[key].label };
    },
  },
  readiness: {
    label: 'Readiness',
    order: READINESS_ORDER,
    of: (session) => {
      const readiness = sessionReadiness(session);
      return { key: readiness.key, label: readiness.label };
    },
  },
  // Keyed on the full path so two forks never merge; labelled by the segment that identifies it.
  repo: {
    label: 'Repository',
    of: (session) => {
      const repo = session.repo?.trim();
      if (!repo) return { key: 'No repository', label: 'No repository' };
      const label = repo.split('/').filter(Boolean).at(-1) ?? repo;
      return { key: repo, label, title: label === repo ? undefined : repo };
    },
  },
  branch: { label: 'Branch', of: (session) => plain(session.branch, 'No branch') },
  agent: { label: 'Agent', of: (session) => ({ key: session.agent, label: getVendor(session.agent).label }) },
  machine: { label: 'Machine', of: (session) => plain(session.sourceLabel, 'Unknown machine') },
};

export const BOARD_GROUP_BY_OPTIONS = (Object.keys(DIMENSIONS) as BoardGroupBy[]).map((value) => ({
  value,
  label: DIMENSIONS[value].label,
}));

export const BOARD_ROW_GROUP_BY_OPTIONS: { value: BoardRowGroupBy; label: string }[] = [
  { value: 'none', label: 'None' },
  ...BOARD_GROUP_BY_OPTIONS,
];

export function boardGroupByLabel(groupBy: BoardRowGroupBy): string {
  return groupBy === 'none' ? 'None' : DIMENSIONS[groupBy].label;
}

export const BOARD_GROUP_BYS: ReadonlySet<BoardGroupBy> = new Set(
  BOARD_GROUP_BY_OPTIONS.map((option) => option.value),
);

export const BOARD_ROW_GROUP_BYS: ReadonlySet<BoardRowGroupBy> = new Set(
  BOARD_ROW_GROUP_BY_OPTIONS.map((option) => option.value),
);

export function groupSessions(sessions: readonly Session[], groupBy: BoardGroupBy): BoardGroup[] {
  const dimension = DIMENSIONS[groupBy];
  const groups = new Map<string, BoardGroup>();
  for (const session of sessions) {
    const { key, label, title } = dimension.of(session);
    const group = groups.get(key);
    if (group) group.sessions.push(session);
    else groups.set(key, { key, label, title, sessions: [session] });
  }
  const ordered = [...groups.values()];
  const order = dimension.order;
  if (order == null) return ordered;
  return ordered.sort((a, b) => order.indexOf(a.key) - order.indexOf(b.key));
}

export function boardGroupKey(session: Session, groupBy: BoardGroupBy): string {
  return DIMENSIONS[groupBy].of(session).key;
}
