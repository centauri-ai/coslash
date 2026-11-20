import { DAY } from '@/pages/coslash/lib/time';

export const SESSION_RANGES = [
  { value: 'today', label: 'Today' },
  { value: 'this-week', label: 'This week' },
  { value: 'week', label: '7 days' },
  { value: 'month', label: '30 days' },
  { value: 'all', label: 'All time' },
] as const;

export type SessionRange = (typeof SESSION_RANGES)[number]['value'];

/** The earliest activity time a range includes: a calendar day or week, or a rolling 7 or 30 days. */
export function rangeStart(range: SessionRange, now = new Date()): number | null {
  const start = new Date(now);
  switch (range) {
    case 'today':
      start.setHours(0, 0, 0, 0);
      return start.getTime();
    case 'this-week':
      start.setHours(0, 0, 0, 0);
      start.setDate(start.getDate() - ((start.getDay() + 6) % 7));
      return start.getTime();
    case 'week':
      return now.getTime() - 7 * DAY;
    case 'month':
      return now.getTime() - 30 * DAY;
    case 'all':
      return null;
  }
}

/** The range to request from the API. Today reuses the 7-day request and the list narrows it; Insights needs all sessions. */
export function requestRange(range: SessionRange, insights: boolean): SessionRange {
  if (insights) return 'all';
  return range === 'today' ? 'week' : range;
}
