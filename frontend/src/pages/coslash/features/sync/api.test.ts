import { describe, expect, it } from 'vitest';
import { decodeSyncStatus } from './api';

describe('decodeSyncStatus', () => {
  it('accepts and canonicalizes Hub origins with a default port', () => {
    expect(
      decodeSyncStatus({
        state: 'connected_idle',
        hubOrigin: 'https://Hub.Example.test:443',
        sessions: {},
      }),
    ).toEqual({
      state: 'connected_idle',
      hubOrigin: 'https://hub.example.test',
      sessions: {},
      sessionFailures: {},
    });
  });

  it('preserves parked failure codes separately from in-flight session states', () => {
    expect(
      decodeSyncStatus({
        state: 'connected_idle',
        hubOrigin: '',
        sessions: { 'local:claude:one': 'parked_failed', 'local:claude:two': 'syncing' },
        sessionFailures: { 'local:claude:one': 'unreadable_source' },
      }),
    ).toEqual({
      state: 'connected_idle',
      hubOrigin: '',
      sessions: { 'local:claude:one': 'parked_failed', 'local:claude:two': 'syncing' },
      sessionFailures: { 'local:claude:one': 'unreadable_source' },
    });
  });

  it('decodes a content-free Hub failure streak', () => {
    expect(
      decodeSyncStatus({
        state: 'connected_idle',
        hubOrigin: 'https://hub.example.test',
        sessions: {},
        lastFailure: {
          host: 'hub.example.test',
          cause: 'connect',
          sinceMs: 1791792720000,
          attempts: 14,
          sentence:
            "Hub: can't connect to hub.example.test since 08:12 (14 attempts) — Connection timed out.",
          rawError: 'dial tcp: i/o timeout',
        },
        hubFailureActive: true,
      }),
    ).toEqual({
      state: 'connected_idle',
      hubOrigin: 'https://hub.example.test',
      sessions: {},
      sessionFailures: {},
      lastFailure: {
        host: 'hub.example.test',
        cause: 'connect',
        sinceMs: 1791792720000,
        attempts: 14,
        sentence: "Hub: can't connect to hub.example.test since 08:12 (14 attempts) — Connection timed out.",
        rawError: 'dial tcp: i/o timeout',
      },
      hubFailureActive: true,
    });
  });

  it('rejects unknown Hub failure causes', () => {
    expect(() =>
      decodeSyncStatus({
        state: 'connected_idle',
        hubOrigin: 'https://hub.example.test',
        sessions: {},
        lastFailure: {
          host: 'hub.example.test',
          cause: 'unknown',
          sinceMs: 1,
          attempts: 1,
          sentence: 'failure',
          rawError: 'error',
        },
      }),
    ).toThrow('Invalid sync status');
  });

  it.each(['https://hub.example.test/path', 'https://user@hub.example.test', 'ftp://hub.example.test'])(
    'rejects a non-origin Hub URL: %s',
    (hubOrigin) => {
      expect(() => decodeSyncStatus({ state: 'connected_idle', hubOrigin, sessions: {} })).toThrow(
        'Invalid sync status',
      );
    },
  );
});
