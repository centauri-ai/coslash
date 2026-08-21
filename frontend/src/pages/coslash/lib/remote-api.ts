import { apiFetch, decodeApiError, readApiError } from '@/pages/coslash/lib/api';
import { decodeMachineFact, type MachineFact } from '@/pages/coslash/lib/machines';

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
