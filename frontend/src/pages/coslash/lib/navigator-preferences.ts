export type NavigatorRange = 'today' | 'this-week' | 'week' | 'month' | 'all';
export type NavigatorStatus = 'needs' | 'running' | 'idle' | 'archived';
export type NavigatorDensity = 'comfortable' | 'compact';
export type NavigatorSort = {
  key: 'title' | 'recent' | 'cost';
  dir: 'asc' | 'desc';
};

export type NavigatorPreferences = {
  query: string;
  range: NavigatorRange;
  statusFilters: NavigatorStatus[];
  groupFilters: string[];
  machineFilter: string | null;
  agentFilter: string | null;
  density: NavigatorDensity;
  sort: NavigatorSort;
};

const STORAGE_KEY = 'coslash.navigator-preferences.v1';
const RANGES = new Set<NavigatorRange>(['today', 'this-week', 'week', 'month', 'all']);
const STATUSES = new Set<NavigatorStatus>(['needs', 'running', 'idle', 'archived']);
const DENSITIES = new Set<NavigatorDensity>(['comfortable', 'compact']);
const SORT_KEYS = new Set<NavigatorSort['key']>(['title', 'recent', 'cost']);
const SORT_DIRECTIONS = new Set<NavigatorSort['dir']>(['asc', 'desc']);

export const DEFAULT_NAVIGATOR_PREFERENCES: NavigatorPreferences = {
  query: '',
  range: 'this-week',
  statusFilters: [],
  groupFilters: [],
  machineFilter: null,
  agentFilter: null,
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

export function loadNavigatorPreferences(storage?: Pick<Storage, 'getItem'>): NavigatorPreferences {
  try {
    const raw = (storage ?? sessionStorage).getItem(STORAGE_KEY);
    if (raw == null) return DEFAULT_NAVIGATOR_PREFERENCES;
    const decoded: unknown = JSON.parse(raw);
    if (decoded == null || typeof decoded !== 'object' || Array.isArray(decoded)) {
      return DEFAULT_NAVIGATOR_PREFERENCES;
    }
    const record = decoded as Record<string, unknown>;
    const storedSort =
      record.sort != null && typeof record.sort === 'object' && !Array.isArray(record.sort)
        ? (record.sort as Record<string, unknown>)
        : {};
    const range = stringOrNull(record.range);
    const density = stringOrNull(record.density);
    const sortKey = stringOrNull(storedSort.key);
    const sortDir = stringOrNull(storedSort.dir);
    return {
      query: stringOrNull(record.query) ?? DEFAULT_NAVIGATOR_PREFERENCES.query,
      range:
        range != null && RANGES.has(range as NavigatorRange)
          ? (range as NavigatorRange)
          : DEFAULT_NAVIGATOR_PREFERENCES.range,
      statusFilters: stringArray(record.statusFilters).filter((status): status is NavigatorStatus =>
        STATUSES.has(status as NavigatorStatus),
      ),
      groupFilters: stringArray(record.groupFilters),
      machineFilter: stringOrNull(record.machineFilter),
      agentFilter: stringOrNull(record.agentFilter),
      density:
        density != null && DENSITIES.has(density as NavigatorDensity)
          ? (density as NavigatorDensity)
          : DEFAULT_NAVIGATOR_PREFERENCES.density,
      sort: {
        key:
          sortKey != null && SORT_KEYS.has(sortKey as NavigatorSort['key'])
            ? (sortKey as NavigatorSort['key'])
            : DEFAULT_NAVIGATOR_PREFERENCES.sort.key,
        dir:
          sortDir != null && SORT_DIRECTIONS.has(sortDir as NavigatorSort['dir'])
            ? (sortDir as NavigatorSort['dir'])
            : DEFAULT_NAVIGATOR_PREFERENCES.sort.dir,
      },
    };
  } catch {
    return DEFAULT_NAVIGATOR_PREFERENCES;
  }
}

export function saveNavigatorPreferences(
  preferences: NavigatorPreferences,
  storage?: Pick<Storage, 'setItem'>,
): void {
  try {
    (storage ?? sessionStorage).setItem(STORAGE_KEY, JSON.stringify(preferences));
  } catch {
    // Storage can be unavailable in private mode. The current-page preferences still work.
  }
}
