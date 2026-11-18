import { describe, expect, it } from 'vitest';
import { makeSession } from '@/pages/coslash/lib/session-fixture';
import { boardGroupKey, groupSessions } from '@/pages/coslash/lib/session-grouping';

describe('groupSessions', () => {
  it('orders status columns by board priority, not by the order sessions arrive in', () => {
    const groups = groupSessions(
      [
        makeSession({ status: 'inactive' }),
        makeSession({ status: 'busy' }),
        makeSession({ status: 'waiting' }),
      ],
      'status',
    );

    expect(groups.map((group) => group.key)).toEqual(['busy', 'waiting', 'inactive']);
    expect(groups.map((group) => group.label)).toEqual(['Active', 'Waiting', 'Inactive']);
  });

  it('keeps the sorted session order for dimensions without a fixed order', () => {
    const groups = groupSessions(
      [
        makeSession({ branch: 'milan/ui' }),
        makeSession({ branch: 'main' }),
        makeSession({ branch: 'milan/ui' }),
      ],
      'branch',
    );

    expect(groups.map((group) => group.key)).toEqual(['milan/ui', 'main']);
    expect(groups[0].sessions).toHaveLength(2);
  });

  it('labels a repository by its last segment while keying on the full path', () => {
    const groups = groupSessions(
      [
        makeSession({ repo: 'github.com/centauri-ai/coslash' }),
        makeSession({ repo: 'github.com/other/coslash' }),
      ],
      'repo',
    );

    expect(groups.map((group) => group.label)).toEqual(['coslash', 'coslash']);
    expect(groups.map((group) => group.key)).toEqual([
      'github.com/centauri-ai/coslash',
      'github.com/other/coslash',
    ]);
  });

  it('collects sessions with no recorded value under one labelled group', () => {
    const groups = groupSessions([makeSession({ repo: null }), makeSession({ repo: '  ' })], 'repo');

    expect(groups).toHaveLength(1);
    expect(groups[0].label).toBe('No repository');
  });
});

describe('boardGroupKey', () => {
  it('matches the key a session was grouped under, so cells find their column', () => {
    const remote = makeSession({ sourceId: 'remote', status: null, repo: 'github.com/centauri-ai/coslash' });

    expect(boardGroupKey(remote, 'status')).toBe('inactive');
    expect(boardGroupKey(remote, 'repo')).toBe('github.com/centauri-ai/coslash');
  });
});
