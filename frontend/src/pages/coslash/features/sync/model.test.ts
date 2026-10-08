import { describe, expect, it } from 'vitest';
import type { SyncStatus } from './api';
import { syncHeader, syncSettingsView } from './model';

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
