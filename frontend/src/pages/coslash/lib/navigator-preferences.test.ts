import { describe, expect, test } from 'vitest';
import {
  DEFAULT_NAVIGATOR_PREFERENCES,
  loadNavigatorPreferences,
  saveNavigatorPreferences,
  type NavigatorPreferences,
} from './navigator-preferences';

function memoryStorage(initial?: string) {
  let value = initial ?? null;
  return {
    getItem: () => value,
    setItem: (_key: string, next: string) => {
      value = next;
    },
  };
}

describe('navigator preferences', () => {
  test('uses defaults when no preferences have been saved', () => {
    expect(loadNavigatorPreferences(memoryStorage())).toEqual(DEFAULT_NAVIGATOR_PREFERENCES);
  });

  test('round-trips the active navigator scope', () => {
    const storage = memoryStorage();
    const preferences: NavigatorPreferences = {
      query: 'repo:coslash',
      range: 'month',
      statusFilters: ['needs', 'running'],
      groupFilters: ['repo:coslash'],
      machineFilter: 'local',
      agentFilter: 'codex',
      density: 'compact',
      sort: { key: 'cost', dir: 'asc' },
    };
    saveNavigatorPreferences(preferences, storage);
    expect(loadNavigatorPreferences(storage)).toEqual(preferences);
  });

  test('drops invalid enum and status values', () => {
    const storage = memoryStorage(
      JSON.stringify({
        range: 'forever',
        statusFilters: ['needs', 'invented'],
        density: 'tiny',
        sort: { key: 'unknown', dir: 'sideways' },
      }),
    );
    expect(loadNavigatorPreferences(storage)).toMatchObject({
      range: 'this-week',
      statusFilters: ['needs'],
      density: 'comfortable',
      sort: { key: 'recent', dir: 'desc' },
    });
  });

  test('recovers from corrupt storage', () => {
    expect(loadNavigatorPreferences(memoryStorage('{not json'))).toEqual(DEFAULT_NAVIGATOR_PREFERENCES);
  });

  test('uses session storage by default', () => {
    const original = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
    const storage = memoryStorage();
    Object.defineProperty(globalThis, 'sessionStorage', {
      configurable: true,
      value: storage,
    });

    try {
      const preferences: NavigatorPreferences = {
        ...DEFAULT_NAVIGATOR_PREFERENCES,
        query: 'workspace',
        statusFilters: ['running'],
      };
      saveNavigatorPreferences(preferences);
      expect(loadNavigatorPreferences()).toEqual(preferences);
    } finally {
      if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
      else delete (globalThis as { sessionStorage?: Storage }).sessionStorage;
    }
  });
});
