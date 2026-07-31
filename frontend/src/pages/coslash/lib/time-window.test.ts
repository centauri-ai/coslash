import { describe, expect, it } from 'vitest';
import { timeWindowStart } from './time-window';

describe('timeWindowStart', () => {
  const now = new Date(2026, 6, 15, 14, 30);

  it('starts this week on Monday at midnight', () => {
    expect(timeWindowStart('week', now)).toBe(new Date(2026, 6, 13).getTime());
  });

  it('starts this month on the first at midnight', () => {
    expect(timeWindowStart('month', now)).toBe(new Date(2026, 6, 1).getTime());
  });

  it('uses rolling durations for day windows', () => {
    expect(timeWindowStart('7d', now)).toBe(now.getTime() - 7 * 24 * 60 * 60 * 1000);
    expect(timeWindowStart('30d', now)).toBe(now.getTime() - 30 * 24 * 60 * 60 * 1000);
  });

  it('does not constrain all sessions', () => {
    expect(timeWindowStart('all', now)).toBeNull();
  });
});
