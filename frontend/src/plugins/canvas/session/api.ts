import { apiFetch } from '@/pages/coslash/lib/api';
import type { CanvasApiFailure, CanvasSessionIdentity, TerminalStatus } from '@/plugins/canvas/contracts';
import type { SessionCanvasDetail, TurnAnalysis } from '@/plugins/canvas/session/types';

export type SessionFetch = (path: string, init?: RequestInit) => Promise<Response>;

export class SessionCanvasApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly field?: string;

  constructor(status: number, failure?: CanvasApiFailure | null) {
    super(failure?.error ?? 'The Canvas request failed.');
    this.name = 'SessionCanvasApiError';
    this.status = status;
    this.code = failure?.code ?? 'CANVAS_REQUEST_FAILED';
    this.field = failure?.field;
  }
}

function identityPath(identity: CanvasSessionIdentity): string {
  return `${encodeURIComponent(identity.agent)}/${encodeURIComponent(identity.id)}`;
}

export function sessionDetailPath(identity: CanvasSessionIdentity): string {
  return `/api/canvas/sessions/${identityPath(identity)}`;
}

async function readJSON<Value>(response: Response): Promise<Value> {
  if (response.ok) return (await response.json()) as Value;
  let failure: CanvasApiFailure | null = null;
  try {
    failure = (await response.json()) as CanvasApiFailure;
  } catch {
    failure = null;
  }
  throw new SessionCanvasApiError(response.status, failure);
}

export async function loadSessionDetail(
  identity: CanvasSessionIdentity,
  fetchImpl: SessionFetch = apiFetch,
): Promise<SessionCanvasDetail> {
  return readJSON(await fetchImpl(sessionDetailPath(identity)));
}

export async function renameSession(
  identity: CanvasSessionIdentity,
  name: string,
  fetchImpl: SessionFetch = apiFetch,
): Promise<{ ok: true; name: string }> {
  return readJSON(
    await fetchImpl(`${sessionDetailPath(identity)}/name`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    }),
  );
}

export type SessionLaunchOptions = {
  prompt?: string;
  model?: string;
  effort?: string;
  permission?: string;
  writable?: boolean;
};

export type TerminalLaunch = {
  ok: true;
  reused?: boolean;
  terminal: TerminalStatus;
  childSessionId?: string;
};

export async function launchSessionTerminal(
  identity: CanvasSessionIdentity,
  options: SessionLaunchOptions = {},
  fetchImpl: SessionFetch = apiFetch,
): Promise<TerminalLaunch> {
  return readJSON(
    await fetchImpl(`${sessionDetailPath(identity)}/terminal`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(options),
    }),
  );
}

export async function forkSession(
  identity: CanvasSessionIdentity,
  options: SessionLaunchOptions & { prompt: string },
  fetchImpl: SessionFetch = apiFetch,
): Promise<TerminalLaunch> {
  return readJSON(
    await fetchImpl(`${sessionDetailPath(identity)}/fork`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(options),
    }),
  );
}

export async function analyzeTurn(
  identity: CanvasSessionIdentity,
  turn: number,
  fetchImpl: SessionFetch = apiFetch,
): Promise<{ ok: true; cacheKey: string; cached: boolean; analysis: TurnAnalysis }> {
  return readJSON(
    await fetchImpl(`${sessionDetailPath(identity)}/turns/${turn}/analysis`, {
      method: 'POST',
    }),
  );
}

export async function sendTerminalInput(
  terminalId: string,
  data: string,
  fetchImpl: SessionFetch = apiFetch,
): Promise<void> {
  await readJSON(
    await fetchImpl(`/api/terminals/${encodeURIComponent(terminalId)}/input`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: 'input', data }),
    }),
  );
}

export async function stopTerminal(terminalId: string, fetchImpl: SessionFetch = apiFetch): Promise<void> {
  await readJSON(
    await fetchImpl(`/api/terminals/${encodeURIComponent(terminalId)}/stop`, { method: 'POST' }),
  );
}

export function bracketedPaste(text: string): string {
  const safe = text.replaceAll('\u0000', '');
  return `\u001b[200~${safe}\u001b[201~`;
}
