// Client for the guarded Atlas workflow API.
//
// The route group mirrors DaGama's under `/api/atlas`, with the differences the
// product actually has: takeover and handback address an attempt rather than a
// component, because an Atlas stage is a committee.
//
// Every call goes through `apiFetch`, so authentication and the coSlash guards
// apply. A terminal reconnect returns a terminal id, never a URL: the transport
// is the native PTY/WebSocket bridge, so there is no second unauthenticated
// port (D-004).

import { apiFetch } from '@/pages/coslash/lib/api';
import {
  isUnsupportedAtlasBoard,
  normalizeAtlasBoard,
  serializeAtlasBoard,
  wasMigratedFromLegacy,
  type AtlasBoard,
} from '@/plugins/canvas/atlas/graph';
import type {
  AtlasBoardLoadError,
  AtlasProject,
  AtlasPublishPreflight,
  AtlasRun,
  AtlasRunPreview,
  AtlasRunSummary,
  AtlasSourceInput,
  AtlasTerminalHandle,
} from '@/plugins/canvas/atlas/types';
import type { CanvasApiFailure, CanvasBoardDocument } from '@/plugins/canvas/contracts';

export type AtlasFetch = (path: string, init?: RequestInit) => Promise<Response>;

/** Stable server codes this client reacts to by name rather than by message. */
export const ATLAS_REVISION_CONFLICT_CODE = 'REVISION_CONFLICT';
export const ATLAS_PROJECT_NOT_OPEN_CODE = 'PROJECT_NOT_OPEN';

export class AtlasApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly field?: string;
  readonly actualRevision?: number;

  constructor(message: string, status: number, failure?: CanvasApiFailure | null) {
    super(message);
    this.name = 'AtlasApiError';
    this.status = status;
    this.code = failure?.code ?? 'ATLAS_REQUEST_FAILED';
    this.field = failure?.field;
    this.actualRevision = failure?.actualRevision;
  }

  get isConflict(): boolean {
    return this.code === ATLAS_REVISION_CONFLICT_CODE;
  }

  get isProjectNotOpen(): boolean {
    return this.code === ATLAS_PROJECT_NOT_OPEN_CODE;
  }
}

export type AtlasBoardDocument = CanvasBoardDocument<AtlasBoard>;
export type AtlasBoardSummary = Omit<AtlasBoardDocument, 'board'>;

const BASE = '/api/atlas';

function projectQuery(projectId: string): string {
  return `projectId=${encodeURIComponent(projectId)}`;
}

function runPath(runId: string, suffix = ''): string {
  return `${BASE}/runs/${encodeURIComponent(runId)}${suffix}`;
}

async function request<Value>(
  path: string,
  init: RequestInit | undefined,
  fetchImpl: AtlasFetch,
): Promise<Value> {
  let response: Response;
  try {
    response = await fetchImpl(path, init);
  } catch (caught) {
    // A transport failure is not a protocol failure; its own code lets a
    // recovery path retry instead of treating it as a rejected request.
    throw new AtlasApiError(
      caught instanceof Error ? caught.message : 'The Atlas service could not be reached.',
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
    throw new AtlasApiError(
      failure?.error ?? `The Atlas service returned ${response.status}.`,
      response.status,
      failure,
    );
  }
  return body as Value;
}

function json(method: string, body: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
}

function adoptDocument(document: CanvasBoardDocument<unknown>): AtlasBoardDocument {
  return { ...document, board: normalizeAtlasBoard(document.board) };
}

export function boardSummaryOf(document: AtlasBoardDocument): AtlasBoardSummary {
  const { board: _board, ...summary } = document;
  return summary;
}

// ---------------------------------------------------------------------------
// Projects and boards
// ---------------------------------------------------------------------------

export async function openAtlasProject(
  projectPath: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasProject> {
  const response = await request<{ ok: true; project: AtlasProject }>(
    `${BASE}/projects/open`,
    json('POST', { path: projectPath }),
    fetchImpl,
  );
  return response.project;
}

export async function listAtlasBoards(
  projectId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<{ boards: AtlasBoardSummary[]; errors: AtlasBoardLoadError[] }> {
  const response = await request<{
    ok: true;
    boards?: AtlasBoardSummary[];
    errors?: AtlasBoardLoadError[];
  }>(`${BASE}/boards?${projectQuery(projectId)}`, undefined, fetchImpl);
  return { boards: response.boards ?? [], errors: response.errors ?? [] };
}

/**
 * A board plus the two facts normalization erases.
 *
 * `unsupported` and `migrated` are read from the raw graph before it is
 * repaired, because repairing is exactly what makes an unknown schema look
 * ordinary. A caller that saves without knowing would rewrite a document whose
 * meaning it cannot see.
 */
export type AtlasBoardRead = {
  document: AtlasBoardDocument;
  unsupported: boolean;
  migrated: boolean;
};

export async function readAtlasBoardDetailed(
  projectId: string,
  id: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasBoardRead> {
  const response = await request<{ ok: true; board: CanvasBoardDocument<unknown> }>(
    `${BASE}/boards/${encodeURIComponent(id)}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return {
    document: adoptDocument(response.board),
    unsupported: isUnsupportedAtlasBoard(response.board.board),
    migrated: wasMigratedFromLegacy(response.board.board),
  };
}

export async function readAtlasBoard(
  projectId: string,
  id: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasBoardDocument> {
  return (await readAtlasBoardDetailed(projectId, id, fetchImpl)).document;
}

export async function writeAtlasBoard(
  input: { projectId: string; id: string; name: string; board: AtlasBoard; expectedRevision: number },
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasBoardDocument> {
  const response = await request<{ ok: true; board: CanvasBoardDocument<unknown> }>(
    `${BASE}/boards/${encodeURIComponent(input.id)}?${projectQuery(input.projectId)}`,
    json('PUT', {
      name: input.name,
      board: serializeAtlasBoard(input.board),
      expectedRevision: input.expectedRevision,
    }),
    fetchImpl,
  );
  return adoptDocument(response.board);
}

export async function deleteAtlasBoard(
  projectId: string,
  id: string,
  expectedRevision: number,
  fetchImpl: AtlasFetch = apiFetch,
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

export async function previewAtlasRun(
  projectId: string,
  input: { boardId: string } | { board: AtlasBoard },
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRunPreview> {
  const body = 'boardId' in input ? { boardId: input.boardId } : { board: serializeAtlasBoard(input.board) };
  const response = await request<{ ok: true; preview: AtlasRunPreview }>(
    `${BASE}/runs/preview?${projectQuery(projectId)}`,
    json('POST', body),
    fetchImpl,
  );
  return response.preview;
}

export async function startAtlasRun(
  input: { projectId: string; boardId: string; source: AtlasSourceInput },
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRun> {
  const response = await request<{ ok: true; run: AtlasRun }>(
    `${BASE}/runs?${projectQuery(input.projectId)}`,
    json('POST', { boardId: input.boardId, source: input.source }),
    fetchImpl,
  );
  return response.run;
}

export async function listAtlasRuns(
  projectId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<{ runs: AtlasRunSummary[]; errors: Array<{ run: string; message: string }> }> {
  const response = await request<{
    ok: true;
    runs?: AtlasRunSummary[];
    errors?: Array<{ run: string; message: string }>;
  }>(`${BASE}/runs?${projectQuery(projectId)}`, undefined, fetchImpl);
  return { runs: response.runs ?? [], errors: response.errors ?? [] };
}

export async function readAtlasRun(
  projectId: string,
  runId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRun> {
  const response = await request<{ ok: true; run: AtlasRun }>(
    `${runPath(runId)}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return response.run;
}

export async function readAtlasArtifact(
  projectId: string,
  runId: string,
  name: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<string> {
  const response = await request<{ ok: true; contents: string }>(
    `${runPath(runId, `/artifacts/${encodeURIComponent(name)}`)}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return response.contents;
}

/** The assembled prompt for one attempt, recomposed by the controller. */
export async function readAtlasPrompt(
  projectId: string,
  runId: string,
  attemptId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<string> {
  const response = await request<{ ok: true; contents: string }>(
    `${runPath(runId, '/prompt')}?${projectQuery(projectId)}&attemptId=${encodeURIComponent(attemptId)}`,
    undefined,
    fetchImpl,
  );
  return response.contents;
}

/** Retry names a stage: a committee is retried whole, never seat by seat. */
export async function retryAtlasStage(
  projectId: string,
  runId: string,
  componentId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRun> {
  const response = await request<{ ok: true; run: AtlasRun }>(
    `${runPath(runId, '/retry')}?${projectQuery(projectId)}`,
    json('POST', { componentId }),
    fetchImpl,
  );
  return response.run;
}

/** Cancel is run-level: it stops every live sibling, not one seat. */
export async function cancelAtlasRun(
  projectId: string,
  runId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRun> {
  const response = await request<{ ok: true; run: AtlasRun }>(
    `${runPath(runId, '/cancel')}?${projectQuery(projectId)}`,
    json('POST', {}),
    fetchImpl,
  );
  return response.run;
}

/** Takeover names an attempt: a fan-out makes a component id ambiguous. */
export async function takeoverAtlasAttempt(
  projectId: string,
  runId: string,
  attemptId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRun> {
  const response = await request<{ ok: true; run: AtlasRun }>(
    `${runPath(runId, '/takeover')}?${projectQuery(projectId)}`,
    json('POST', { attemptId }),
    fetchImpl,
  );
  return response.run;
}

export async function handbackAtlasAttempt(
  projectId: string,
  runId: string,
  attemptId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRun> {
  const response = await request<{ ok: true; run: AtlasRun }>(
    `${runPath(runId, '/handback')}?${projectQuery(projectId)}`,
    json('POST', { attemptId }),
    fetchImpl,
  );
  return response.run;
}

export async function reconnectAtlasTerminal(
  projectId: string,
  runId: string,
  attemptId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasTerminalHandle> {
  const response = await request<{
    ok: true;
    terminalId?: string;
    attemptId?: string;
    writable?: boolean;
    terminal?: { terminalId: string; writable?: boolean };
  }>(`${runPath(runId, '/terminal')}?${projectQuery(projectId)}`, json('POST', { attemptId }), fetchImpl);
  const terminalId = response.terminalId ?? response.terminal?.terminalId ?? '';
  if (terminalId === '') {
    throw new AtlasApiError('The seat terminal did not return an identifier.', 502, {
      ok: false,
      code: 'TERMINAL_UNAVAILABLE',
      error: 'no terminal id',
    });
  }
  return {
    terminalId,
    attemptId: response.attemptId ?? attemptId,
    writable: response.writable ?? response.terminal?.writable,
  };
}

export async function readAtlasPublishPreflight(
  projectId: string,
  runId: string,
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasPublishPreflight> {
  const response = await request<{ ok: true; preflight: AtlasPublishPreflight }>(
    `${runPath(runId, '/publish-preflight')}?${projectQuery(projectId)}`,
    undefined,
    fetchImpl,
  );
  return response.preflight;
}

export async function decideAtlasGate(
  projectId: string,
  runId: string,
  decision: 'approved' | 'rejected',
  options: { publish?: boolean } = {},
  fetchImpl: AtlasFetch = apiFetch,
): Promise<AtlasRun> {
  const response = await request<{ ok: true; run: AtlasRun }>(
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
