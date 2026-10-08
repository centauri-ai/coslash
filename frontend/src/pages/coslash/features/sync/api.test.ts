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
    ).toEqual({ state: 'connected_idle', hubOrigin: 'https://hub.example.test', sessions: {} });
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
