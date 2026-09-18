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
  sort: { key: 'recent', dir: 'desc' },
};

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
    const range = stringOrNull(record.range);
    const view = stringOrNull(record.view);
    const density = stringOrNull(record.density);
    const sortKey = stringOrNull(storedSort.key);
    const sortDir = stringOrNull(storedSort.dir);
    return {
      query: stringOrNull(record.query) ?? DEFAULT_SESSION_VIEW_PREFERENCES.query,
      range:
        range != null && RANGES.has(range as SessionRange)
          ? (range as SessionRange)
          : DEFAULT_SESSION_VIEW_PREFERENCES.range,
      statusFilters: stringArray(record.statusFilters).filter((status): status is SessionStatusGroup =>
        STATUSES.has(status as SessionStatusGroup),
      ),
      groupFilters: stringArray(record.groupFilters),
      machineFilters: stringArrayOrLegacy(record.machineFilters, record.machineFilter),
      agentFilters: stringArrayOrLegacy(record.agentFilters, record.agentFilter),
      view:
        view != null && VIEWS.has(view as SessionView)
          ? (view as SessionView)
          : DEFAULT_SESSION_VIEW_PREFERENCES.view,
      density:
        density != null && DENSITIES.has(density as SessionListDensity)
          ? (density as SessionListDensity)
          : DEFAULT_SESSION_VIEW_PREFERENCES.density,
      sort: {
        key:
          sortKey != null && SORT_KEYS.has(sortKey as SessionSort['key'])
            ? (sortKey as SessionSort['key'])
            : DEFAULT_SESSION_VIEW_PREFERENCES.sort.key,
        dir:
          sortDir != null && SORT_DIRECTIONS.has(sortDir as SessionSort['dir'])
            ? (sortDir as SessionSort['dir'])
            : DEFAULT_SESSION_VIEW_PREFERENCES.sort.dir,
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
