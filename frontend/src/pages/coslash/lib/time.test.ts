import { describe, expect, it } from 'vitest';
import { promptCacheTiming } from './time';

describe('promptCacheTiming', () => {
  it('advances at each cache boundary', () => {
    const lastAccessAt = 1_000;
    expect(promptCacheTiming(lastAccessAt, lastAccessAt + 4 * 60_000)).toEqual({
      within5m: true,
      within1h: true,
      nextRefreshAt: lastAccessAt + 5 * 60_000 + 1,
    });
    expect(promptCacheTiming(lastAccessAt, lastAccessAt + 6 * 60_000)).toEqual({
      within5m: false,
      within1h: true,
      nextRefreshAt: lastAccessAt + 60 * 60_000 + 1,
    });
    expect(promptCacheTiming(lastAccessAt, lastAccessAt + 61 * 60_000)).toEqual({
      within5m: false,
      within1h: false,
      nextRefreshAt: null,
    });
  });
});
