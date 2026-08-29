import { describe, expect, it } from 'vitest';
import { SortKey, sortSessions } from '@/pages/coslash/components/SessionSortDropdownMenu';
import type { Session } from '@/pages/coslash/lib/session';

function session(id: string, status: string | null, sourceId = 'local', displayStale = false): Session {
  return { id, status, sourceId, displayStale } as Session;
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
      'waiting',
      'idle',
      'inactive',
    ]);
  });

  it('places sessions with unknown remote liveness after inactive sessions', () => {
    const sessions = [
      session('unknown', null, 'r_0123456789abcdef'),
      session('inactive', null),
      session('stale', 'busy', 'r_0123456789abcdef', true),
    ];

    expect(sortSessions(sessions, SortKey.Status, 'desc').map(({ id }) => id)).toEqual([
      'inactive',
      'unknown',
      'stale',
    ]);
  });
});
