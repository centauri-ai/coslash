import { apiFetch } from '@/pages/coslash/lib/api';

export type SyncState =
  | 'not_connected'
  | 'connected_syncing'
  | 'connected_idle'
  | 'paused'
  | 'auto_sync_off'
  | 'disconnected'
  | 'update_required';

export type SessionSyncState = 'in_hub' | 'syncing' | 'not_in_hub' | 'left_out';

export type SyncStatus = {
  state: SyncState;
  hubOrigin: string;
  sessions: Record<string, SessionSyncState>;
};

export type SyncStatusLoad = 'loading' | 'ready' | 'error';

const SYNC_STATES = new Set<SyncState>([
  'not_connected',
  'connected_syncing',
  'connected_idle',
  'paused',
  'auto_sync_off',
  'disconnected',
  'update_required',
]);

const SESSION_STATES = new Set<SessionSyncState>(['in_hub', 'syncing', 'not_in_hub', 'left_out']);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null && !Array.isArray(value);
}

function canonicalHubOrigin(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  if (value === '') return '';
  try {
    const parsed = new URL(value);
    if (
      (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') ||
      parsed.username !== '' ||
      parsed.password !== '' ||
      parsed.pathname !== '/' ||
      parsed.search !== '' ||
      parsed.hash !== ''
    ) {
      return null;
    }
    return parsed.origin;
  } catch {
    return null;
  }
}

export function decodeSyncStatus(value: unknown): SyncStatus {
  if (
    !isRecord(value) ||
    typeof value.state !== 'string' ||
    !SYNC_STATES.has(value.state as SyncState) ||
    !isRecord(value.sessions)
  ) {
    throw new Error('Invalid sync status');
  }
  const hubOrigin = canonicalHubOrigin(value.hubOrigin);
  if (hubOrigin === null) throw new Error('Invalid sync status');
  const sessions: Record<string, SessionSyncState> = {};
  for (const [key, state] of Object.entries(value.sessions)) {
    if (!SESSION_STATES.has(state as SessionSyncState)) throw new Error('Invalid sync status');
    sessions[key] = state as SessionSyncState;
  }
  return { state: value.state as SyncState, hubOrigin, sessions };
}

export async function loadSyncStatus(signal?: AbortSignal): Promise<SyncStatus> {
  const response = await apiFetch('/api/sync/status', { signal });
  if (!response.ok) throw new Error('Sync status is unavailable');
  return decodeSyncStatus((await response.json()) as unknown);
}
