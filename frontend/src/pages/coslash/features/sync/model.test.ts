import { describe, expect, it } from 'vitest';
import type { SessionSyncState, SyncState, SyncStatus } from './api';
import { chipLabel, chipOf, syncHeader, syncMode, syncSettingsView } from './model';
import designCases from './testdata/design-cases.json';

const stateByMode: Record<string, SyncState> = {
  on: 'connected_idle',
  off: 'auto_sync_off',
  paused: 'paused',
  none: 'not_connected',
  revoked: 'disconnected',
};

const stateBySession: Record<string, SessionSyncState> = {
  sync: 'syncing',
  in: 'in_hub',
  out: 'not_in_hub',
  left: 'left_out',
};

describe('Local sync UI design states', () => {
  for (const designCase of designCases.cases) {
    it(designCase.state, () => {
      const state = stateByMode[designCase.input.mode];
      const status: SyncStatus = {
        state,
        hubOrigin: 'https://hub.coslash.io',
        sessions: {},
      };

      expect(syncHeader(status).label).toBe(designCase.output.hd.text);

      const mode = syncMode(status.state);
      for (const session of designCase.input.sessions) {
        const row = designCase.output.rows.find((candidate) => candidate.id === session.id);
        expect(row, `design row ${session.id}`).toBeDefined();
        expect(row?.aria).toBe(`${session.title}, ${chipLabel(chipOf(mode, stateBySession[session.base]))}`);
      }

      const settings = syncSettingsView(status);
      expect(settings.connected).toBe(designCase.output.set.connected);
      if (settings.connected) {
        expect(settings.autoLine).toBe(designCase.output.set.autoLine);
      } else {
        expect(settings.notConnectedText).toBe(designCase.output.set.ncText);
      }
    });
  }
});

describe('disabled sync status', () => {
  it('does not imply that Hub disabled auto-sync', () => {
    const status: SyncStatus = {
      state: 'auto_sync_off',
      hubOrigin: 'https://hub.example.test',
      sessions: {},
    };

    expect(syncHeader(status).label).toBe('Auto-sync off');
    expect(syncSettingsView(status)).toEqual(
      expect.objectContaining({ autoLine: 'Off', autoSyncNote: 'Auto-sync is off.' }),
    );
  });
});

describe('stale Hub contact status', () => {
  it('switches the header chip to a warning after the retry interval', () => {
    const status: SyncStatus = {
      state: 'connected_idle',
      hubOrigin: 'https://hub.example.test',
      sessions: {},
      lastFailure: {
        host: 'hub.example.test',
        cause: 'connect',
        sinceMs: new Date(2026, 9, 12, 8, 12).getTime(),
        attempts: 14,
        sentence: "Hub: can't connect to hub.example.test since 08:12 (14 attempts) — Connection timed out.",
        rawError: 'dial tcp: i/o timeout',
      },
      hubFailureActive: true,
    };

    expect(syncHeader(status).label).toMatch(/^Can't reach Hub · since /);
    expect(syncHeader({ ...status, hubFailureActive: false }).label).toBe('Auto-sync on · managed in Hub');
  });
});
