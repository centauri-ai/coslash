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

function keyedMemoryStorage(initial: Record<string, string>) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
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
      statusFilters: ['waiting', 'busy'],
      groupFilters: ['repo:coslash'],
      machineFilters: ['local', 'remote'],
      agentFilters: ['codex', 'claude'],
      view: 'insights',
      density: 'compact',
      boardColumns: 'readiness',
      boardRows: 'branch',
      sort: { key: 'cost', dir: 'asc' },
    };
    saveSessionViewPreferences(preferences, storage);
    expect(loadSessionViewPreferences(storage)).toEqual(preferences);
  });

  test('loads saved v1 preferences without converting obsolete group IDs', () => {
    const preferences = {
      query: 'repo:coslash',
      range: 'month',
      statusFilters: ['waiting'],
      groupFilters: ['unlocated:local:codex'],
      machineFilter: 'local',
      agentFilter: 'codex',
      view: 'board',
      density: 'compact',
      boardColumns: 'readiness',
      boardRows: 'branch',
      sort: { key: 'cost', dir: 'asc' },
    };
    const storage = keyedMemoryStorage({
      'coslash.session-view-preferences.v1': JSON.stringify(preferences),
    });

    expect(loadSessionViewPreferences(storage)).toEqual({
      query: 'repo:coslash',
      range: 'month',
      statusFilters: ['waiting'],
      groupFilters: ['unlocated:local:codex'],
      machineFilters: ['local'],
      agentFilters: ['codex'],
      view: 'board',
      density: 'compact',
      boardColumns: 'readiness',
      boardRows: 'branch',
      sort: { key: 'cost', dir: 'asc' },
    });
  });

  test('drops invalid enum and status values', () => {
    const storage = memoryStorage(
      JSON.stringify({
        range: 'forever',
        statusFilters: ['waiting', 'invented'],
        view: 'grid',
        density: 'tiny',
        sort: { key: 'unknown', dir: 'sideways' },
      }),
    );
    expect(loadSessionViewPreferences(storage)).toMatchObject({
      range: 'this-week',
      statusFilters: ['waiting'],
      view: 'list',
      density: 'comfortable',
      sort: { key: 'recent', dir: 'desc' },
    });
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
        statusFilters: ['busy'],
      };
      saveSessionViewPreferences(preferences);
      expect(loadSessionViewPreferences()).toEqual(preferences);
    } finally {
      if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
      else delete (globalThis as { sessionStorage?: Storage }).sessionStorage;
    }
  });
});
