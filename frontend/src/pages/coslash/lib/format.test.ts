import { describe, expect, it } from 'vitest';
import { formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';

describe('unknown usage formatting', () => {
  it('uses an em dash for unknown tokens', () => {
    expect(formatTokens(null)).toBe('—');
  });

  it('uses an em dash for unknown cost', () => {
    expect(formatEstimatedCost(null)).toBe('—');
  });
});

describe('small token counts', () => {
  it('preserves positive counts below one thousand and genuine zero', () => {
    for (const count of [0, 1, 45, 210, 499, 500, 999]) expect(formatTokens(count)).toBe(String(count));
    expect(formatTokens(1000)).toBe('1k');
    expect(formatTokens(1000000)).toBe('1M');
  });
});
