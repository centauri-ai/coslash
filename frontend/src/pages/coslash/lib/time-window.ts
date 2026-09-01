import { DAY, HOUR } from '@/pages/coslash/lib/time';

export const TIME_WINDOWS = [
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: 'all', label: 'All' },
] as const;

export type TimeWindow = (typeof TIME_WINDOWS)[number]['value'];

export const TIME_WINDOW_VALUES = TIME_WINDOWS.map((window) => window.value);

export function timeWindowStart(window: TimeWindow, now = new Date()): number | null {
  switch (window) {
    case '24h':
      return now.getTime() - 24 * HOUR;
    case '7d':
      return now.getTime() - 7 * DAY;
    case '30d':
      return now.getTime() - 30 * DAY;
    case 'all':
      return null;
  }
}
