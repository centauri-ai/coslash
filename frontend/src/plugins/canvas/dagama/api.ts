// Client for the guarded DaGama workflow API.
//
// Route shapes are the ones CONTRACTS.md freezes for workflow compatibility:
// project open, board CRUD, run preview/start/list/read, artifacts, prompts,
// attempt outputs, retry/trigger, terminal reconnect, cancel, takeover/handback,
// publish preflight, and gate decisions. Everything goes through `apiFetch`, so
// authentication and the coSlash guards apply to every call.
//
// The dev server this replaced returned a ttyd URL for a terminal reconnect.
// That is deliberately gone (D-004): a reconnect now yields a terminal id that
// the native PTY/WebSocket transport attaches to, so a Canvas terminal is never
// reachable on an unauthenticated port.

import { apiFetch } from '@/pages/coslash/lib/api';
import type { CanvasApiFailure, CanvasBoardDocument } from '@/plugins/canvas/contracts';
import { normalizeDaGamaBoard, serializeDaGamaBoard, type DaGamaBoard } from '@/plugins/canvas/dagama/board';
import type {
  DaGamaBoardLoadError,
  DaGamaProject,
  DaGamaPublishPreflight,
  DaGamaRun,
  DaGamaRunPreview,
  DaGamaRunSourceInput,
  DaGamaRunSummary,
  DaGamaTerminalHandle,
} from '@/plugins/canvas/dagama/types';
import type { DaGamaSeatComponentId } from '@/plugins/canvas/dagama/vocabulary';

export type DaGamaFetch = (path: string, init?: RequestInit) => Promise<Response>;

/** Stable server codes this client reacts to by name rather than by message. */
export const DAGAMA_REVISION_CONFLICT_CODE = 'REVISION_CONFLICT';
export const DAGAMA_PROJECT_NOT_OPEN_CODE = 'PROJECT_NOT_OPEN';

export class DaGamaApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly field?: string;
  readonly actualRevision?: number;

  constructor(message: string, status: number, failure?: CanvasApiFailure | null) {
    super(message);
    this.name = 'DaGamaApiError';
    this.status = status;
    this.code = failure?.code ?? 'DAGAMA_REQUEST_FAILED';
    this.field = failure?.field;
    this.actualRevision = failure?.actualRevision;
  }

  get isConflict(): boolean {
    return this.code === DAGAMA_REVISION_CONFLICT_CODE;
  }

  get isProjectNotOpen(): boolean {
    return this.code === DAGAMA_PROJECT_NOT_OPEN_CODE;
  }
}

/** The document envelope the board API exchanges, with the board still unparsed. */
export type DaGamaBoardDocument = CanvasBoardDocument<DaGamaBoard>;
export type DaGamaBoardSummary = Omit<DaGamaBoardDocument, 'board'>;

const BASE = '/api/dagama';

function projectQuery(projectId: string): string {
  return `projectId=${encodeURIComponent(projectId)}`;
}

function runPath(runId: string, suffix = ''): string {
  return `${BASE}/runs/${encodeURIComponent(runId)}${suffix}`;
}

async function request<Value>(
  path: string,
  init: RequestInit | undefined,
  fetchImpl: DaGamaFetch,
): Promise<Value> {
  let response: Response;
  try {
    response = await fetchImpl(path, init);
  } catch (caught) {
    // A transport failure is not a protocol failure; give it its own code so
    // recovery paths can retry instead of treating it as a rejected request.
    throw new DaGamaApiError(
      caught instanceof Error ? caught.message : 'The DaGama service could not be reached.',
      0,
      { ok: false, code: 'NETWORK_ERROR', error: 'unreachable' },
    );
  }
  let body: unknown = null;
  try {
    body = await response.json();
  } catch {
    body = null;
  }
  if (!response.ok || (body as { ok?: boolean } | null)?.ok === false) {
    const failure = (body ?? null) as CanvasApiFailure | null;
    throw new DaGamaApiError(
      failure?.error ?? `The DaGama service returned ${response.status}.`,
      response.status,
      failure,
    );
  }
  return body as Value;
}

function json(method: string, body: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
}

/** Adopt a wire document, repairing the board it carries. */
function adoptDocument(document: CanvasBoardDocument<unknown>): DaGamaBoardDocument {
  return { ...document, board: normalizeDaGamaBoard(document.board) };
}

export function boardSummaryOf(document: DaGamaBoardDocument): DaGamaBoardSummary {
  const { board: _board, ...summary } = document;
  return summary;
}

// ---------------------------------------------------------------------------
// Projects and boards
// ---------------------------------------------------------------------------

export async function openDaGamaProject(
  projectPath: string,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaProject> {
  const response = await request<{ ok: true; project: DaGamaProject }>(
    `${BASE}/projects/open`,
    json('POST', { path: projectPath }),
    fetchImpl,
  );
  return response.project;
}

export async function listDaGamaBoards(
  projectId: string,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<{ boards: DaGamaBoardSummary[]; errors: DaGamaBoardLoadError[] }> {
  const response = await request<{
    ok: true;
    boards?: DaGamaBoardSummary[];
    errors?: DaGamaBoardLoadError[];
  }>(`${BASE}/boards?${projectQuery(projectId)}`, undefined, fetchImpl);
  return { boards: response.boards ?? [], errors: response.errors ?? [] };
}

export async function readDaGamaBoard(
  projectId: string,
  id: string,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaBoardDocument> {
  const response = await request<{ ok: true; board: CanvasBoardDocument<unknown> }>(
    `${BASE}/boards/${encodeURIComponent(id)}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return adoptDocument(response.board);
}

export async function writeDaGamaBoard(
  input: { projectId: string; id: string; name: string; board: DaGamaBoard; expectedRevision: number },
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaBoardDocument> {
  const response = await request<{ ok: true; board: CanvasBoardDocument<unknown> }>(
    `${BASE}/boards/${encodeURIComponent(input.id)}?${projectQuery(input.projectId)}`,
    json('PUT', {
      name: input.name,
      board: serializeDaGamaBoard(input.board),
      expectedRevision: input.expectedRevision,
    }),
    fetchImpl,
  );
  return adoptDocument(response.board);
}

export async function deleteDaGamaBoard(
  projectId: string,
  id: string,
  expectedRevision: number,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<void> {
  await request<{ ok: true }>(
    `${BASE}/boards/${encodeURIComponent(id)}?${projectQuery(projectId)}&expectedRevision=${expectedRevision}`,
    { method: 'DELETE' },
    fetchImpl,
  );
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

/**
 * What the run would do, computed by the same preflight the real start uses — so
 * the dialog cannot promise a base branch or a path that starting would reject.
 *
 * A saved `boardId` is preferred. An inline board is allowed so the run dialog
 * can preview before the draft has been named and saved; starting still requires
 * a stored revision.
 */
export async function previewDaGamaRun(
  projectId: string,
  input: { boardId: string } | { board: DaGamaBoard },
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRunPreview> {
  const body = 'boardId' in input ? { boardId: input.boardId } : { board: serializeDaGamaBoard(input.board) };
  const response = await request<{ ok: true; preview: DaGamaRunPreview }>(
    `${BASE}/runs/preview?${projectQuery(projectId)}`,
    json('POST', body),
    fetchImpl,
  );
  return response.preview;
}

export async function startDaGamaRun(
  input: { projectId: string; boardId: string; source: DaGamaRunSourceInput },
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRun> {
  const response = await request<{ ok: true; run: DaGamaRun }>(
    `${BASE}/runs?${projectQuery(input.projectId)}`,
    json('POST', { boardId: input.boardId, source: input.source }),
    fetchImpl,
  );
  return response.run;
}

export async function listDaGamaRuns(
  projectId: string,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<{ runs: DaGamaRunSummary[]; errors: Array<{ run: string; message: string }> }> {
  const response = await request<{
    ok: true;
    runs?: DaGamaRunSummary[];
    errors?: Array<{ run: string; message: string }>;
  }>(`${BASE}/runs?${projectQuery(projectId)}`, undefined, fetchImpl);
  return { runs: response.runs ?? [], errors: response.errors ?? [] };
}

export async function readDaGamaRun(
  projectId: string,
  runId: string,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRun> {
  const response = await request<{ ok: true; run: DaGamaRun }>(
    `${runPath(runId)}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return response.run;
}

export async function readDaGamaRunArtifact(
  projectId: string,
  runId: string,
  name: string,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<string> {
  const response = await request<{ ok: true; contents: string }>(
    `${runPath(runId, `/artifacts/${encodeURIComponent(name)}`)}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return response.contents;
}

export async function readDaGamaRunPrompt(
  projectId: string,
  runId: string,
  componentId: DaGamaSeatComponentId = 'plan',
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<string> {
  const response = await request<{ ok: true; contents: string }>(
    `${runPath(runId, '/prompt')}?${projectQuery(projectId)}&componentId=${encodeURIComponent(componentId)}`,
    undefined,
    fetchImpl,
  );
  return response.contents;
}

export async function retryDaGamaSeat(
  projectId: string,
  runId: string,
  componentId: DaGamaSeatComponentId,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRun> {
  const response = await request<{ ok: true; run: DaGamaRun }>(
    `${runPath(runId, '/retry')}?${projectQuery(projectId)}`,
    json('POST', { componentId }),
    fetchImpl,
  );
  return response.run;
}

/**
 * Reattach the guarded native terminal for a seat attempt.
 *
 * Returns a terminal id, never a URL: the transport is the coSlash
 * PTY/WebSocket bridge, so there is no second port and no second auth surface.
 */
export async function reconnectDaGamaTerminal(
  projectId: string,
  runId: string,
  componentId: DaGamaSeatComponentId,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaTerminalHandle> {
  const response = await request<{
    ok: true;
    terminalId?: string;
    attemptId?: string;
    writable?: boolean;
    reused?: boolean;
    terminal?: { terminalId: string; writable?: boolean };
  }>(`${runPath(runId, '/terminal')}?${projectQuery(projectId)}`, json('POST', { componentId }), fetchImpl);
  const terminalId = response.terminalId ?? response.terminal?.terminalId ?? '';
  if (terminalId === '') {
    throw new DaGamaApiError('The seat terminal did not return an identifier.', 502, {
      ok: false,
      code: 'TERMINAL_UNAVAILABLE',
      error: 'no terminal id',
    });
  }
  return {
    terminalId,
    attemptId: response.attemptId ?? '',
    writable: response.writable ?? response.terminal?.writable,
    reused: response.reused,
  };
}

export async function cancelDaGamaRun(
  projectId: string,
  runId: string,
  componentId: DaGamaSeatComponentId | undefined,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRun> {
  const response = await request<{ ok: true; run: DaGamaRun }>(
    `${runPath(runId, '/cancel')}?${projectQuery(projectId)}`,
    json('POST', componentId ? { componentId } : {}),
    fetchImpl,
  );
  return response.run;
}

export async function takeoverDaGamaSeat(
  projectId: string,
  runId: string,
  componentId: DaGamaSeatComponentId,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRun> {
  const response = await request<{ ok: true; run: DaGamaRun }>(
    `${runPath(runId, '/takeover')}?${projectQuery(projectId)}`,
    json('POST', { componentId }),
    fetchImpl,
  );
  return response.run;
}

export async function handbackDaGamaSeat(
  projectId: string,
  runId: string,
  componentId: DaGamaSeatComponentId,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRun> {
  const response = await request<{ ok: true; run: DaGamaRun }>(
    `${runPath(runId, '/handback')}?${projectQuery(projectId)}`,
    json('POST', { componentId }),
    fetchImpl,
  );
  return response.run;
}

export async function readDaGamaPublishPreflight(
  projectId: string,
  runId: string,
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaPublishPreflight> {
  const response = await request<{ ok: true; preflight: DaGamaPublishPreflight }>(
    `${runPath(runId, '/publish-preflight')}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return response.preflight;
}

export async function decideDaGamaGate(
  projectId: string,
  runId: string,
  decision: 'approved' | 'rejected',
  options: { publish?: boolean } = {},
  fetchImpl: DaGamaFetch = apiFetch,
): Promise<DaGamaRun> {
  const response = await request<{ ok: true; run: DaGamaRun }>(
    `${runPath(runId, '/gate')}?${projectQuery(projectId)}`,
    json('POST', {
      decision,
      // `publish` is only meaningful on approval, and only when opting out.
      ...(decision === 'approved' && options.publish === false ? { publish: false } : {}),
    }),
    fetchImpl,
  );
  return response.run;
}
