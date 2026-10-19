import { apiFetch } from '@/pages/coslash/lib/api';

export type SyncState =
  | 'not_connected'
  | 'connected_syncing'
  | 'connected_idle'
  | 'paused'
  | 'auto_sync_off'
  | 'disconnected'
  | 'update_required';

export type SessionSyncState = 'in_hub' | 'syncing' | 'not_in_hub' | 'left_out' | 'parked_failed';

export type HubFailureCause = 'dns' | 'connect' | 'tls' | 'proxy' | 'hub_unavailable' | 'rejected';

export type HubFailureStatus = {
  host: string;
  cause: HubFailureCause;
  sinceMs: number;
  attempts: number;
  sentence: string;
  rawError: string;
};

export type SyncStatus = {
  state: SyncState;
  hubOrigin: string;
  sessions: Record<string, SessionSyncState>;
  lastFailure?: HubFailureStatus;
  hubFailureActive?: boolean;
  sessionFailures?: Record<string, string>;
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

const SESSION_STATES = new Set<SessionSyncState>([
  'in_hub',
  'syncing',
  'not_in_hub',
  'left_out',
  'parked_failed',
]);
const HUB_FAILURE_CAUSES = new Set<HubFailureCause>([
  'dns',
  'connect',
  'tls',
  'proxy',
  'hub_unavailable',
  'rejected',
]);

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
  const sessionFailures: Record<string, string> = {};
  if (value.sessionFailures != null) {
    if (!isRecord(value.sessionFailures)) throw new Error('Invalid sync status');
    for (const [key, code] of Object.entries(value.sessionFailures)) {
      if (typeof code !== 'string') throw new Error('Invalid sync status');
      sessionFailures[key] = code;
    }
  }
  let lastFailure: HubFailureStatus | undefined;
  if (value.lastFailure !== undefined) {
    if (
      !isRecord(value.lastFailure) ||
      typeof value.lastFailure.host !== 'string' ||
      typeof value.lastFailure.cause !== 'string' ||
      !HUB_FAILURE_CAUSES.has(value.lastFailure.cause as HubFailureCause) ||
      typeof value.lastFailure.sinceMs !== 'number' ||
      !Number.isFinite(value.lastFailure.sinceMs) ||
      typeof value.lastFailure.attempts !== 'number' ||
      !Number.isInteger(value.lastFailure.attempts) ||
      value.lastFailure.attempts < 1 ||
      typeof value.lastFailure.sentence !== 'string' ||
      typeof value.lastFailure.rawError !== 'string'
    ) {
      throw new Error('Invalid sync status');
    }
    lastFailure = {
      host: value.lastFailure.host,
      cause: value.lastFailure.cause as HubFailureCause,
      sinceMs: value.lastFailure.sinceMs,
      attempts: value.lastFailure.attempts,
      sentence: value.lastFailure.sentence,
      rawError: value.lastFailure.rawError,
    };
  }
  if (value.hubFailureActive !== undefined && typeof value.hubFailureActive !== 'boolean') {
    throw new Error('Invalid sync status');
  }
  return {
    state: value.state as SyncState,
    hubOrigin,
    sessions,
    sessionFailures,
    ...(lastFailure ? { lastFailure } : {}),
    ...(typeof value.hubFailureActive === 'boolean' ? { hubFailureActive: value.hubFailureActive } : {}),
  };
}

export async function loadSyncStatus(signal?: AbortSignal): Promise<SyncStatus> {
  const response = await apiFetch('/api/sync/status', { signal });
  if (!response.ok) throw new Error('Sync status is unavailable');
  return decodeSyncStatus((await response.json()) as unknown);
}

export async function retrySyncNow(): Promise<void> {
  const response = await apiFetch('/api/sync/retry', { method: 'POST' });
  if (!response.ok) throw new Error('Hub sync could not be retried.');
}
