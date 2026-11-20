import {
  boardStatusKey,
  currentLocation,
  getVendor,
  sessionKey,
  sessionReadiness,
  STATUS_ORDER,
  STATUSES,
  type Session,
} from '@/pages/coslash/lib/session';
import type { Group } from './detected-groups';

export type BoardGroupBy = 'status' | 'readiness' | 'repo' | 'worktree' | 'branch' | 'agent' | 'machine';
export type BoardRowGroupBy = BoardGroupBy | 'none';

export type BoardGroup = { key: string; label: string; title?: string; sessions: Session[] };

type Dimension = {
  label: string;
  of: (session: Session) => { key: string; label: string; title?: string };
  /** Fixed left-to-right order; dimensions without one follow the sorted session order. */
  order?: readonly string[];
};

const READINESS_ORDER = ['resume', 'inspect', 'fresh', 'unavailable'] as const;
const MISSING_REPOSITORY_KEY = '\0missing-repository';

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
      const repo =
        currentLocation(session)?.repo ?? (session.repoLocalOnly ? undefined : session.repo?.trim());
      if (!repo) return { key: MISSING_REPOSITORY_KEY, label: 'No repository' };
      const label = repo.split('/').filter(Boolean).at(-1) ?? repo;
      return { key: repo, label, title: label === repo ? undefined : repo };
    },
  },
  worktree: {
    label: 'Worktree',
    of: (session) => {
      const loc = currentLocation(session);
      return loc
        ? {
            key: JSON.stringify([session.sourceId, loc.id]),
            label: loc.main ? 'Main checkout' : loc.label,
            title: loc.root,
          }
        : { key: 'unverified', label: 'No verified checkout' };
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

export function groupSessions(
  sessions: readonly Session[],
  groupBy: BoardGroupBy,
  displayedGroups?: ReadonlyMap<string, Group>,
): BoardGroup[] {
  const dimension = DIMENSIONS[groupBy];
  const groups = new Map<string, BoardGroup>();
  for (const session of sessions) {
    const location = displayedGroups?.get(sessionKey(session));
    let bucket = dimension.of(session);
    if (location?.kind === 'Worktree') {
      if (groupBy === 'worktree')
        bucket = { key: location.id, label: location.label, title: location.context };
      if (groupBy === 'repo' && location.repositoryId && location.context)
        bucket = {
          key: location.context,
          label: location.context.split('/').at(-1) ?? location.context,
          title: location.context,
        };
    } else if (groupBy === 'repo' && location?.kind === 'Repository') {
      bucket = { key: location.basis, label: location.label, title: location.basis };
    }
    const { key, label, title } = bucket;
    const group = groups.get(key);
    if (group) group.sessions.push(session);
    else groups.set(key, { key, label, title, sessions: [session] });
  }
  const ordered = [...groups.values()];
  const order = dimension.order;
  if (order == null) return ordered;
  return ordered.sort((a, b) => order.indexOf(a.key) - order.indexOf(b.key));
}
