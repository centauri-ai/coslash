import { DAY } from '@/pages/coslash/lib/time';

export const TIME_WINDOWS = [
  { value: 'week', label: 'This week' },
  { value: 'month', label: 'This month' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: 'all', label: 'All' },
] as const;

export type TimeWindow = (typeof TIME_WINDOWS)[number]['value'];

export const TIME_WINDOW_VALUES = TIME_WINDOWS.map((window) => window.value);

export function timeWindowStart(window: TimeWindow, now = new Date()): number | null {
  switch (window) {
    case 'week': {
      const start = new Date(now);
      const daysSinceMonday = (start.getDay() + 6) % 7;
      start.setHours(0, 0, 0, 0);
      start.setDate(start.getDate() - daysSinceMonday);
      return start.getTime();
    }
    case 'month':
      return new Date(now.getFullYear(), now.getMonth(), 1).getTime();
    case '7d':
      return now.getTime() - 7 * DAY;
    case '30d':
      return now.getTime() - 30 * DAY;
    case 'all':
      return null;
  }
}
