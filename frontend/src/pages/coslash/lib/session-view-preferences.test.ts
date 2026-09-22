import { describe, expect, test } from 'vitest';
import {
  DEFAULT_SESSION_VIEW_PREFERENCES,
  loadSessionViewPreferences,
  saveSessionViewPreferences,
  type SessionViewPreferences,
} from './session-view-preferences';

function memoryStorage(initial?: string) {
  let value = initial ?? null;
  return {
    getItem: () => value,
    setItem: (_key: string, next: string) => {
      value = next;
    },
  };
}

describe('session view preferences', () => {
  test('uses defaults when no preferences have been saved', () => {
    expect(loadSessionViewPreferences(memoryStorage())).toEqual(DEFAULT_SESSION_VIEW_PREFERENCES);
  });

  test('round-trips the active session scope', () => {
    const storage = memoryStorage();
    const preferences: SessionViewPreferences = {
      query: 'repo:coslash',
      range: 'month',
      statusFilters: ['needs', 'running'],
      groupFilters: ['repo:coslash'],
      machineFilters: ['local', 'remote'],
      agentFilters: ['codex', 'claude'],
      view: 'board',
      density: 'compact',
      boardColumns: 'readiness',
      boardRows: 'branch',
      sort: { key: 'cost', dir: 'asc' },
    };
    saveSessionViewPreferences(preferences, storage);
    expect(loadSessionViewPreferences(storage)).toEqual(preferences);
  });

  test('drops invalid enum and status values', () => {
    const storage = memoryStorage(
      JSON.stringify({
        range: 'forever',
        statusFilters: ['needs', 'invented'],
        view: 'grid',
        density: 'tiny',
        sort: { key: 'unknown', dir: 'sideways' },
      }),
    );
    expect(loadSessionViewPreferences(storage)).toMatchObject({
      range: 'this-week',
      statusFilters: ['needs'],
      view: 'list',
      density: 'comfortable',
      sort: { key: 'recent', dir: 'desc' },
    });
  });

  test('migrates legacy single machine and agent filters', () => {
    const storage = memoryStorage(JSON.stringify({ machineFilter: 'local', agentFilter: 'codex' }));

    expect(loadSessionViewPreferences(storage)).toMatchObject({
      machineFilters: ['local'],
      agentFilters: ['codex'],
    });
  });

  test('migrates legacy per-agent no-location filters into the shared group', () => {
    const storage = memoryStorage(
      JSON.stringify({
        groupFilters: ['unlocated:local:codex', 'repo:coslash', 'unlocated:remote:claude'],
      }),
    );

    expect(loadSessionViewPreferences(storage).groupFilters).toEqual(['unlocated', 'repo:coslash']);
  });

  test('recovers from corrupt storage', () => {
    expect(loadSessionViewPreferences(memoryStorage('{not json'))).toEqual(DEFAULT_SESSION_VIEW_PREFERENCES);
  });

  test('uses session storage by default', () => {
    const original = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
    const storage = memoryStorage();
    Object.defineProperty(globalThis, 'sessionStorage', {
      configurable: true,
      value: storage,
    });

    try {
      const preferences: SessionViewPreferences = {
        ...DEFAULT_SESSION_VIEW_PREFERENCES,
        query: 'workspace',
        statusFilters: ['running'],
      };
      saveSessionViewPreferences(preferences);
      expect(loadSessionViewPreferences()).toEqual(preferences);
    } finally {
      if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
      else delete (globalThis as { sessionStorage?: Storage }).sessionStorage;
    }
  });
});
