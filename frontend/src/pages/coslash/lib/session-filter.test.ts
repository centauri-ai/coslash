import { describe, expect, it } from 'vitest';
import { detectedGroup } from '@/pages/coslash/lib/detected-groups';
import { countFacets, matchesFacets, sortSessions } from '@/pages/coslash/lib/session-filter';
import { makeSession } from '@/pages/coslash/lib/session-fixture';
import { DEFAULT_SESSION_VIEW_PREFERENCES } from '@/pages/coslash/lib/session-view-preferences';

describe('sortSessions', () => {
  it('sorts A-Z on the displayed title', () => {
    const sessions = [
      makeSession({ id: 'named', name: 'Banana' }),
      makeSession({ id: 'prompt-z', firstPrompt: 'Zebra prompt' }),
      makeSession({ id: 'prompt-a', firstPrompt: 'Apple prompt' }),
    ];

    expect(sortSessions(sessions, { key: 'title', dir: 'asc' }).map(({ id }) => id)).toEqual([
      'prompt-a',
      'named',
      'prompt-z',
    ]);
  });

  it('sorts rows with unknown liveness below current rows', () => {
    const sessions = [
      makeSession({ id: 'stale', mtime: 200, displayStale: true }),
      makeSession({ id: 'live', mtime: 100 }),
    ];

    expect(sortSessions(sessions, { key: 'recent', dir: 'desc' }).map(({ id }) => id)).toEqual([
      'live',
      'stale',
    ]);
  });
});

describe('facet filters', () => {
  it('applies a saved task-folder filter to every grouped Codex task', () => {
    const preferences = {
      ...DEFAULT_SESSION_VIEW_PREFERENCES,
      groupFilters: ['folder:local:/users/milan/documents/codex'],
    };
    const task = makeSession({
      cwd: '/Users/milan/Documents/Codex/2026-10-22/other/src',
      repo: 'other',
      repoLocalOnly: true,
    });
    const outside = makeSession({ cwd: '/Users/milan/work/app', repo: 'app', repoLocalOnly: true });

    expect(matchesFacets(task, detectedGroup(task), preferences)).toBe(true);
    expect(matchesFacets(outside, detectedGroup(outside), preferences)).toBe(false);
  });

  it('counts each facet with the other facets applied', () => {
    const sessions = [
      makeSession({ id: 'local-codex', sourceId: 'local', agent: 'codex', status: 'busy' }),
      makeSession({ id: 'local-claude', sourceId: 'local', agent: 'claude', status: 'busy' }),
      makeSession({ id: 'remote-codex', sourceId: 'remote', agent: 'codex' }),
      makeSession({ id: 'remote-claude', sourceId: 'remote', agent: 'claude' }),
    ];
    const counts = countFacets(sessions, detectedGroup, {
      ...DEFAULT_SESSION_VIEW_PREFERENCES,
      agentFilters: ['codex'],
    });

    expect(Object.fromEntries(counts.status)).toEqual({ busy: 1, inactive: 1 });
    expect(Object.fromEntries(counts.agent)).toEqual({ codex: 2, claude: 2 });
    expect(Object.fromEntries(counts.machine)).toEqual({ local: 1, remote: 1 });
  });
});
