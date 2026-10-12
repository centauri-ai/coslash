import { describe, expect, it } from 'vitest';
import type { SyncStatus } from './api';
import { sameStatus } from './use-sync-status';

const healthy: SyncStatus = {
  state: 'connected_idle',
  hubOrigin: 'https://hub.example.test',
  sessions: {},
};

describe('sameStatus', () => {
  it('observes when a repeated failure passes its retry interval', () => {
    const failure: SyncStatus = {
      ...healthy,
      lastFailure: {
        host: 'hub.example.test',
        cause: 'connect',
        sinceMs: 1,
        attempts: 1,
        sentence: "Hub: can't connect to hub.example.test since 08:12 (1 attempts).",
        rawError: 'dial tcp: i/o timeout',
      },
    };

    expect(sameStatus(failure, { ...failure, hubFailureActive: true })).toBe(false);
  });

  it('observes recovery and changing attempt counts', () => {
    const failure: SyncStatus = {
      ...healthy,
      lastFailure: {
        host: 'hub.example.test',
        cause: 'connect',
        sinceMs: 1,
        attempts: 1,
        sentence: "Hub: can't connect to hub.example.test since 08:12 (1 attempts).",
        rawError: 'dial tcp: i/o timeout',
      },
    };

    expect(sameStatus(failure, healthy)).toBe(false);
    expect(sameStatus(failure, { ...failure, lastFailure: { ...failure.lastFailure!, attempts: 2 } })).toBe(
      false,
    );
  });
});
