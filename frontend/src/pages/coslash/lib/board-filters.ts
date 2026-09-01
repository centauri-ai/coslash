import { VENDOR_KEYS, type VendorKey } from '@/pages/coslash/lib/session';
import { TIME_WINDOW_VALUES, type TimeWindow } from '@/pages/coslash/lib/time-window';

export type AgentVendor = 'all' | VendorKey;

export type BoardFilters = {
  vendor: AgentVendor;
  timeWindow: TimeWindow;
};

const STORAGE_KEY = 'coslash.board-filters';
const AGENT_VENDORS = ['all', ...VENDOR_KEYS] as const satisfies readonly AgentVendor[];

export const DEFAULT_BOARD_FILTERS: BoardFilters = {
  vendor: 'all',
  timeWindow: 'week',
};

function oneOf<T extends string>(value: unknown, allowed: readonly T[], fallback: T): T {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : fallback;
}

export function loadBoardFilters(storage: Pick<Storage, 'getItem'> = sessionStorage): BoardFilters {
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (raw == null) return DEFAULT_BOARD_FILTERS;
    const parsed: unknown = JSON.parse(raw);
    if (parsed == null || typeof parsed !== 'object') return DEFAULT_BOARD_FILTERS;
    const record = parsed as Record<string, unknown>;
    return {
      vendor: oneOf(record.vendor, AGENT_VENDORS, DEFAULT_BOARD_FILTERS.vendor),
      timeWindow: oneOf(record.timeWindow, TIME_WINDOW_VALUES, DEFAULT_BOARD_FILTERS.timeWindow),
    };
  } catch {
    return DEFAULT_BOARD_FILTERS;
  }
}

export function saveBoardFilters(
  filters: BoardFilters,
  storage: Pick<Storage, 'setItem'> = sessionStorage,
): void {
  try {
    storage.setItem(STORAGE_KEY, JSON.stringify(filters));
  } catch {
    // Private mode / quota — filters still work for the current page life.
  }
}

/** Keep a selected vendor tab mounted even when the current result set has none. */
export function vendorsForFilterMenu(
  present: readonly VendorKey[],
  selected: AgentVendor,
): VendorKey[] {
  if (selected === 'all' || present.includes(selected)) return [...present];
  return VENDOR_KEYS.filter((vendor) => present.includes(vendor) || vendor === selected);
}
