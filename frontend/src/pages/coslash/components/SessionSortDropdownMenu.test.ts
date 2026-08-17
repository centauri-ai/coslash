import { describe, expect, it } from 'vitest';
import { SortKey, sortSessions } from '@/pages/coslash/components/SessionSortDropdownMenu';
import type { Session } from '@/pages/coslash/lib/session';

function session(id: string, status: string | null): Session {
  return { id, status } as Session;
}

describe('sortSessions', () => {
  it('sorts descending status by current activity', () => {
    const sessions = [
      session('inactive', null),
      session('waiting', 'waiting'),
      session('active', 'busy'),
      session('idle', 'idle'),
    ];

    expect(sortSessions(sessions, SortKey.Status, 'desc').map(({ id }) => id)).toEqual([
      'active',
      'idle',
      'waiting',
      'inactive',
    ]);
  });
});
