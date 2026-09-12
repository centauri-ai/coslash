import { apiFetch, decodeApiError, readApiError } from '@/pages/coslash/lib/api';
import { decodeMachineFact, type MachineFact } from '@/pages/coslash/lib/machines';
import { assertOneOf } from '@/pages/coslash/lib/narrow';

export const HELPER_SETUP_OUTCOMES = [
  'installed_and_tested',
  'reused_and_tested',
  'deprecated_helper_active',
  'consent_required',
  'unsupported',
  'blocked',
  'incompatible',
  'revoked',
  'verification_failed',
  'installation_failed',
  'rolled_back',
  'helper_test_failed',
  'sftp_fallback',
] as const;
export type HelperSetupOutcome = (typeof HELPER_SETUP_OUTCOMES)[number];

export function remoteTestRequestInit(sshAlias: string): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sshAlias }),
  };
}

export async function testRemoteAlias(sshAlias: string): Promise<MachineFact> {
  const response = await apiFetch('/api/remote/test', remoteTestRequestInit(sshAlias));
  if (!response.ok) {
    const apiError = await readApiError(response);
    throw new Error(apiError?.error || `Remote test failed (${response.status})`);
  }
  return decodeMachineFact(await response.json());
}

export type RemoteAuthAttempt = {
  id: string;
  state: 'waiting' | 'ready' | 'timed_out' | 'cancelled' | 'failed';
};

function decodeRemoteAuthAttempt(value: unknown): RemoteAuthAttempt {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid authentication status');
  const raw = value as Record<string, unknown>;
  if (typeof raw.id !== 'string' || !['waiting', 'ready', 'timed_out', 'cancelled', 'failed'].includes(String(raw.state))) {
    throw new Error('Invalid authentication status');
  }
  return { id: raw.id, state: raw.state as RemoteAuthAttempt['state'] };
}

export async function startRemoteAuthentication(sshAlias: string, signal?: AbortSignal): Promise<RemoteAuthAttempt> {
  const response = await apiFetch('/api/remote/auth/start', { ...remoteTestRequestInit(sshAlias), signal });
  const body: unknown = await response.json();
  if (!response.ok) throw new Error(decodeApiError(body).error);
  return decodeRemoteAuthAttempt(body);
}

export async function remoteAuthenticationStatus(id: string, signal?: AbortSignal): Promise<RemoteAuthAttempt> {
  const response = await apiFetch(`/api/remote/auth/status?id=${encodeURIComponent(id)}`, { signal });
  const body: unknown = await response.json();
  if (!response.ok) throw new Error(decodeApiError(body).error);
  return decodeRemoteAuthAttempt(body);
}

export async function cancelRemoteAuthentication(id: string, signal?: AbortSignal): Promise<RemoteAuthAttempt> {
  const response = await apiFetch('/api/remote/auth/cancel', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id }), signal,
  });
  const body: unknown = await response.json();
  if (!response.ok) throw new Error(decodeApiError(body).error);
  return decodeRemoteAuthAttempt(body);
}

export async function retryRemoteRefresh(signal?: AbortSignal): Promise<{ status: number; machine: MachineFact }> {
  const response = await apiFetch('/api/remote/retry', { method: 'POST', signal });
  const body: unknown = await response.json();
  if (!response.ok) {
    const apiError = decodeApiError(body);
    throw Object.assign(new Error(apiError.error), { code: apiError.code, status: response.status });
  }
  return { status: response.status, machine: decodeMachineFact(body) };
}

const REMOTE_REFRESH_POLL_INTERVAL_MS = 400;

function remoteRefreshInProgress(machine: MachineFact): boolean {
  return (
    machine.refreshing ||
    machine.state === 'connecting' ||
    machine.reason === 'initial_refresh' ||
    machine.helperProbeState === 'probing'
  );
}

function waitForAbort(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(signal.reason);
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener(
      'abort',
      () => {
        clearTimeout(timer);
        reject(signal.reason);
      },
      { once: true },
    );
  });
}

export async function waitForRemoteRefresh(
  machine?: MachineFact,
  signal?: AbortSignal,
): Promise<MachineFact> {
  let current = machine ?? (await remoteStatus(signal));
  while (remoteRefreshInProgress(current)) {
    await waitForAbort(REMOTE_REFRESH_POLL_INTERVAL_MS, signal);
    current = await remoteStatus(signal);
  }
  return current;
}

// The retry endpoint acknowledges that collection has started, rather than
// waiting for it to finish. Wait for its terminal health state before callers
// reload the board, so it does not remain on the transient "connecting" view.
export async function retryRemoteRefreshAndWait(signal?: AbortSignal): Promise<MachineFact> {
  try {
    const { machine } = await retryRemoteRefresh(signal);
    return waitForRemoteRefresh(machine, signal);
  } catch (error: unknown) {
    // Authentication can make a shared master available just as the manager
    // starts its own refresh. Waiting for that in-flight refresh is equivalent
    // to retrying, and avoids presenting a successful reconnect as an error.
    if (
      error instanceof Error &&
      (error as Error & { status?: unknown; code?: unknown }).status === 429 &&
      (error as Error & { status?: unknown; code?: unknown }).code === 'remote_retry_throttled'
    ) {
      return waitForRemoteRefresh(undefined, signal);
    }
    throw error;
  }
}

export async function setupRemoteHelper(
  sshAlias: string,
  consent: 'install' | 'upgrade',
): Promise<HelperSetupResult> {
  const response = await apiFetch('/api/remote/helper/setup', helperSetupRequestInit(sshAlias, consent));
  const body: unknown = await response.json();
  try {
    return decodeHelperSetup(body);
  } catch (error) {
    if (!response.ok) throw new Error(decodeApiError(body).error);
    throw error;
  }
}

export function helperSetupRequestInit(sshAlias: string, consent: 'install' | 'upgrade'): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sshAlias, install: consent === 'install', upgrade: consent === 'upgrade' }),
  };
}

export type HelperSetupResult = { machine: MachineFact; outcome: HelperSetupOutcome; error?: string };

export function decodeHelperSetup(value: unknown): HelperSetupResult {
  if (value == null || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid helper setup result');
  const raw = value as Record<string, unknown>;
  if (typeof raw.outcome !== 'string') throw new Error('Invalid helper setup result');
  const result: HelperSetupResult = {
    machine: decodeMachineFact(raw.machine),
    outcome: assertOneOf(raw.outcome, HELPER_SETUP_OUTCOMES),
  };
  if (raw.error != null) {
    if (typeof raw.error !== 'string') throw new Error('Invalid helper setup result');
    result.error = raw.error;
  }
  return result;
}

export async function remoteStatus(signal?: AbortSignal): Promise<MachineFact> {
  const response = await apiFetch('/api/remote/status', { signal });
  if (!response.ok) {
    const apiError = await readApiError(response);
    throw new Error(apiError?.error || `Remote status failed (${response.status})`);
  }
  return decodeMachineFact(await response.json());
}
