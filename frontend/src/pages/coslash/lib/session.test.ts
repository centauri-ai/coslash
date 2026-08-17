import { describe, expect, it } from 'vitest';
import { getSessionVendors, type Session } from '@/pages/coslash/lib/session';

describe('getSessionVendors', () => {
  it('returns only vendors represented by loaded sessions in display order', () => {
    const sessions = [
      { agent: 'opencode' },
      { agent: 'claude' },
      { agent: 'claude' },
      { agent: 'unknown' },
    ] satisfies Pick<Session, 'agent'>[];

    expect(getSessionVendors(sessions)).toEqual(['claude', 'opencode']);
  });
});
