import { describe, expect, it } from 'vitest';
import { rangeStart, requestRange } from '@/pages/coslash/lib/session-range';

describe('rangeStart', () => {
  const now = new Date(2026, 6, 15, 14, 30);

  it('starts today at midnight', () => {
    expect(rangeStart('today', now)).toBe(new Date(2026, 6, 15).getTime());
  });

  it('starts this week on Monday at midnight', () => {
    expect(rangeStart('this-week', now)).toBe(new Date(2026, 6, 13).getTime());
  });

  it('uses rolling durations for 7 and 30 days', () => {
    expect(rangeStart('week', now)).toBe(now.getTime() - 7 * 24 * 60 * 60 * 1000);
    expect(rangeStart('month', now)).toBe(now.getTime() - 30 * 24 * 60 * 60 * 1000);
  });

  it('does not constrain all sessions', () => {
    expect(rangeStart('all', now)).toBeNull();
  });
});

describe('requestRange', () => {
  it('requests 7 days for Today and narrows in the list', () => {
    expect(requestRange('today', false)).toBe('week');
  });

  it('requests every other range as it is', () => {
    expect(requestRange('this-week', false)).toBe('this-week');
    expect(requestRange('month', false)).toBe('month');
  });

  it('requests all sessions for Insights and search', () => {
    expect(requestRange('today', true)).toBe('all');
  });
});
