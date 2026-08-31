import { apiFetch, decodeApiError, readApiError } from '@/pages/coslash/lib/api';
import { decodeMachineFact, type MachineFact } from '@/pages/coslash/lib/machines';

/** Client budget slightly above the server's 90s remote deadline. */
export const REMOTE_TEST_TIMEOUT_MS = 95_000;

export function remoteTestTimeoutMessage(timeoutMs = REMOTE_TEST_TIMEOUT_MS): string {
  return `Remote test timed out after ${Math.round(timeoutMs / 1000)}s. Check the coslash terminal and ~/.coslash/logs.`;
}

export function remoteTestRequestInit(sshAlias: string): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sshAlias }),
  };
}

export async function testRemoteAlias(sshAlias: string): Promise<MachineFact> {
  const controller = new AbortController();
  const timer = globalThis.setTimeout(() => controller.abort(), REMOTE_TEST_TIMEOUT_MS);
  try {
    const response = await apiFetch('/api/remote/test', {
      ...remoteTestRequestInit(sshAlias),
      signal: controller.signal,
    });
    if (!response.ok) {
      const apiError = await readApiError(response);
      throw new Error(apiError?.error || `Remote test failed (${response.status})`);
    }
    return decodeMachineFact(await response.json());
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new Error(remoteTestTimeoutMessage());
    }
    throw error;
  } finally {
    globalThis.clearTimeout(timer);
  }
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
