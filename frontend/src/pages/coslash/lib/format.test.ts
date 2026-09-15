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
