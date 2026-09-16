import { describe, expect, it } from 'vitest';
import { SortKey, sortSessions } from '@/pages/coslash/components/SessionSortDropdownMenu';
import type { Session } from '@/pages/coslash/lib/session';

function session(id: string, status: string | null, sourceId = 'local', displayStale = false): Session {
  return { id, status, sourceId, displayStale } as Session;
}

describe('sortSessions', () => {
  it('sorts descending status by current activity', () => {
    const sessions: Session[] = [
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
    const sessions: Session[] = [
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

  it.each(['asc', 'desc'] as const)('places unknown costs last when sorting %s', (dir) => {
    const sessions: Session[] = [
      { ...session('unknown', null), cost: null },
      { ...session('zero', null), cost: 0 },
      { ...session('priced', null), cost: 1 },
    ];

    expect(sortSessions(sessions, SortKey.Value, dir).at(-1)?.id).toBe('unknown');
  });

  it.each(['asc', 'desc'] as const)('places unknown token counts last when sorting %s', (dir) => {
    const zeroTokens = {
      input_tokens: 0,
      output_tokens: 0,
      cache_creation_input_tokens: 0,
      cache_creation_1h_input_tokens: 0,
      cache_read_input_tokens: 0,
    };
    const sessions: Session[] = [
      { ...session('unknown', null), tokens: {} },
      { ...session('zero', null), tokens: { model: zeroTokens } },
      { ...session('used', null), tokens: { model: { ...zeroTokens, input_tokens: 1 } } },
    ];

    expect(sortSessions(sessions, SortKey.Tokens, dir).at(-1)?.id).toBe('unknown');
  });
});
