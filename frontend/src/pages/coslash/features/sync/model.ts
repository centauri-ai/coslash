import type { SessionSyncState, SyncState, SyncStatus } from './api';

export type SyncMode = 'on' | 'off' | 'paused' | 'none' | 'revoked' | 'update';
export type SyncChipState = SessionSyncState | 'none';

export function syncMode(state: SyncState): SyncMode {
  switch (state) {
    case 'connected_idle':
    case 'connected_syncing':
      return 'on';
    case 'auto_sync_off':
      return 'off';
    case 'paused':
      return 'paused';
    case 'disconnected':
      return 'revoked';
    case 'update_required':
      return 'update';
    default:
      return 'none';
  }
}

export function chipOf(mode: SyncMode, state: SessionSyncState, manualUpload = false): SyncChipState {
  if (mode === 'none' || mode === 'revoked') return 'none';
  if (state === 'left_out') return 'left_out';
  if (state === 'in_hub') return 'in_hub';
  if (manualUpload) return 'syncing';
  if (state === 'syncing') return mode === 'on' || mode === 'update' ? 'syncing' : 'not_in_hub';
  return 'not_in_hub';
}

export function chipLabel(state: SyncChipState): string {
  switch (state) {
    case 'in_hub':
      return 'In Hub';
    case 'syncing':
      return 'Syncing…';
    case 'left_out':
      return 'Left out';
    case 'none':
      return 'Not connected';
    default:
      return 'Not in Hub';
  }
}

export function syncHeader(status: SyncStatus): { label: string; href: string | null; linked: boolean } {
  const hub = status.hubOrigin || null;
  switch (status.state) {
    case 'connected_syncing':
    case 'connected_idle':
      return { label: 'Auto-sync on · managed in Hub', href: hub, linked: true };
    case 'auto_sync_off':
      return { label: 'Auto-sync off', href: hub, linked: true };
    case 'paused':
      return { label: 'Paused on this computer', href: null, linked: false };
    case 'disconnected':
      return { label: 'Disconnected from Hub', href: null, linked: false };
    case 'update_required':
      return { label: 'Update required', href: hub, linked: true };
    default:
      return { label: 'Not connected · Connect in Hub', href: hub, linked: true };
  }
}

export type SyncSettingsView = {
  connected: boolean;
  autoLine: string;
  autoSyncNote: string;
  notConnectedText: string;
};

export function syncSettingsView(status: SyncStatus): SyncSettingsView {
  const connected = status.state !== 'not_connected' && status.state !== 'disconnected';
  const autoLine =
    status.state === 'auto_sync_off' ? 'Off' : status.state === 'update_required' ? 'Update required' : 'On';
  const autoSyncNote =
    status.state === 'auto_sync_off' ? 'Auto-sync is off.' : 'Auto-sync is managed in Hub.';
  const notConnectedText =
    status.state === 'disconnected'
      ? 'Disconnected from Hub. This computer was removed from your account.'
      : 'Not connected to Hub.';
  return { connected, autoLine, autoSyncNote, notConnectedText };
}
