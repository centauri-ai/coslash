import {
  BOARD_GROUP_BYS,
  BOARD_ROW_GROUP_BYS,
  type BoardGroupBy,
  type BoardRowGroupBy,
} from '@/pages/coslash/lib/session-grouping';

export type SessionRange = 'today' | 'this-week' | 'week' | 'month' | 'all';
export type SessionStatusGroup = 'needs' | 'running' | 'idle';
export type SessionListDensity = 'comfortable' | 'compact';
export type SessionView = 'list' | 'board';
export type SessionSort = {
  key: 'title' | 'recent' | 'cost';
  dir: 'asc' | 'desc';
};

export type SessionViewPreferences = {
  query: string;
  range: SessionRange;
  statusFilters: SessionStatusGroup[];
  groupFilters: string[];
  machineFilters: string[];
  agentFilters: string[];
  view: SessionView;
  density: SessionListDensity;
  boardColumns: BoardGroupBy;
  boardRows: BoardRowGroupBy;
  sort: SessionSort;
};

const STORAGE_KEY = 'coslash.session-view-preferences.v1';
const RANGES = new Set<SessionRange>(['today', 'this-week', 'week', 'month', 'all']);
const STATUSES = new Set<SessionStatusGroup>(['needs', 'running', 'idle']);
const VIEWS = new Set<SessionView>(['list', 'board']);
const DENSITIES = new Set<SessionListDensity>(['comfortable', 'compact']);
const SORT_KEYS = new Set<SessionSort['key']>(['title', 'recent', 'cost']);
const SORT_DIRECTIONS = new Set<SessionSort['dir']>(['asc', 'desc']);

export const DEFAULT_SESSION_VIEW_PREFERENCES: SessionViewPreferences = {
  query: '',
  range: 'this-week',
  statusFilters: [],
  groupFilters: [],
  machineFilters: [],
  agentFilters: [],
  view: 'list',
  density: 'comfortable',
  boardColumns: 'status',
  boardRows: 'repo',
  sort: { key: 'recent', dir: 'desc' },
};

/** Persisted preferences are untrusted input, so an unknown value falls back rather than throwing. */
function oneOf<T extends string>(value: unknown, allowed: ReadonlySet<T>, fallback: T): T {
  return typeof value === 'string' && (allowed as ReadonlySet<string>).has(value) ? (value as T) : fallback;
}

function stringOrNull(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value)
    ? [...new Set(value.filter((item): item is string => typeof item === 'string'))]
    : [];
}

function stringArrayOrLegacy(array: unknown, single: unknown): string[] {
  const values = stringArray(array);
  const legacy = stringOrNull(single);
  return values.length > 0 || legacy == null ? values : [legacy];
}

export function loadSessionViewPreferences(storage?: Pick<Storage, 'getItem'>): SessionViewPreferences {
  try {
    const raw = (storage ?? sessionStorage).getItem(STORAGE_KEY);
    if (raw == null) return DEFAULT_SESSION_VIEW_PREFERENCES;
    const decoded: unknown = JSON.parse(raw);
    if (decoded == null || typeof decoded !== 'object' || Array.isArray(decoded)) {
      return DEFAULT_SESSION_VIEW_PREFERENCES;
    }
    const record = decoded as Record<string, unknown>;
    const storedSort =
      record.sort != null && typeof record.sort === 'object' && !Array.isArray(record.sort)
        ? (record.sort as Record<string, unknown>)
        : {};
    const defaults = DEFAULT_SESSION_VIEW_PREFERENCES;
    return {
      query: stringOrNull(record.query) ?? defaults.query,
      range: oneOf(record.range, RANGES, defaults.range),
      statusFilters: stringArray(record.statusFilters).filter((status): status is SessionStatusGroup =>
        STATUSES.has(status as SessionStatusGroup),
      ),
      groupFilters: stringArray(record.groupFilters),
      machineFilters: stringArrayOrLegacy(record.machineFilters, record.machineFilter),
      agentFilters: stringArrayOrLegacy(record.agentFilters, record.agentFilter),
      view: oneOf(record.view, VIEWS, defaults.view),
      density: oneOf(record.density, DENSITIES, defaults.density),
      boardColumns: oneOf(record.boardColumns, BOARD_GROUP_BYS, defaults.boardColumns),
      boardRows: oneOf(record.boardRows, BOARD_ROW_GROUP_BYS, defaults.boardRows),
      sort: {
        key: oneOf(storedSort.key, SORT_KEYS, defaults.sort.key),
        dir: oneOf(storedSort.dir, SORT_DIRECTIONS, defaults.sort.dir),
      },
    };
  } catch {
    return DEFAULT_SESSION_VIEW_PREFERENCES;
  }
}

export function saveSessionViewPreferences(
  preferences: SessionViewPreferences,
  storage?: Pick<Storage, 'setItem'>,
): void {
  try {
    (storage ?? sessionStorage).setItem(STORAGE_KEY, JSON.stringify(preferences));
  } catch {
    // Storage can be unavailable in private mode. The current-page preferences still work.
  }
}
