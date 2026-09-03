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

export async function retryRemoteRefresh(): Promise<{ status: number; machine: MachineFact }> {
  const response = await apiFetch('/api/remote/retry', { method: 'POST' });
  const body: unknown = await response.json();
  if (!response.ok) {
    const apiError = decodeApiError(body);
    throw Object.assign(new Error(apiError.error), { code: apiError.code, status: response.status });
  }
  return { status: response.status, machine: decodeMachineFact(body) };
}

const REMOTE_REFRESH_POLL_INTERVAL_MS = 400;

// The retry endpoint acknowledges that collection has started, rather than
// waiting for it to finish. Wait for its terminal health state before callers
// reload the board, so it does not remain on the transient "connecting" view.
export async function retryRemoteRefreshAndWait(): Promise<MachineFact> {
  let { machine } = await retryRemoteRefresh();
  while (machine.state === 'connecting' || machine.refreshing) {
    await new Promise<void>((resolve) => setTimeout(resolve, REMOTE_REFRESH_POLL_INTERVAL_MS));
    machine = await remoteStatus();
  }
  return machine;
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

export async function remoteStatus(): Promise<MachineFact> {
  const response = await apiFetch('/api/remote/status');
  if (!response.ok) {
    const apiError = await readApiError(response);
    throw new Error(apiError?.error || `Remote status failed (${response.status})`);
  }
  return decodeMachineFact(await response.json());
}
