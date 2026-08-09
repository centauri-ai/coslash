import { describe, expect, it, vi } from 'vitest';
import {
  AtlasApiError,
  cancelAtlasRun,
  decideAtlasGate,
  deleteAtlasBoard,
  handbackAtlasAttempt,
  listAtlasBoards,
  openAtlasProject,
  previewAtlasRun,
  readAtlasBoard,
  readAtlasPrompt,
  reconnectAtlasTerminal,
  retryAtlasStage,
  startAtlasRun,
  takeoverAtlasAttempt,
  writeAtlasBoard,
} from '@/plugins/canvas/atlas/api';
import { FROZEN_ATLAS_STORED_BOARD } from '@/plugins/canvas/atlas/fixtures';
import { defaultAtlasBoard } from '@/plugins/canvas/atlas/graph';

type Call = { path: string; init?: RequestInit };

function recorder(response: unknown, status = 200) {
  const calls: Call[] = [];
  const fetchImpl = vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, init });
    return new Response(JSON.stringify(response), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  return { calls, fetchImpl };
}

function body(call: Call): Record<string, unknown> {
  return JSON.parse(String(call.init?.body ?? '{}')) as Record<string, unknown>;
}

describe('Atlas API client', () => {
  it('scopes every call to the frozen /api/atlas prefix', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, project: { id: 'p', name: 'p', path: '/p' } });
    await openAtlasProject('/p', fetchImpl);
    expect(calls[0].path).toBe('/api/atlas/projects/open');
    expect(body(calls[0])).toEqual({ path: '/p' });
  });

  it('carries the project through the query on every scoped route', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, boards: [], errors: [] });
    await listAtlasBoards('demo project', fetchImpl);
    expect(calls[0].path).toBe('/api/atlas/boards?projectId=demo%20project');
  });

  it('encodes identifiers so a crafted id cannot escape its route segment', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, contents: '' });
    await readAtlasPrompt('p', '../../etc', 'a-1', fetchImpl);
    expect(calls[0].path).toContain('/api/atlas/runs/..%2F..%2Fetc/prompt');
  });

  it('normalizes a board it reads and re-serializes it when writing', async () => {
    const read = recorder({
      ok: true,
      board: {
        schemaVersion: 1,
        id: 'board-1',
        name: 'Starter',
        revision: 3,
        createdAt: '2026-08-09T07:00:00Z',
        updatedAt: '2026-08-09T07:04:00Z',
        board: FROZEN_ATLAS_STORED_BOARD,
      },
    });
    const document = await readAtlasBoard('demo', 'board-1', read.fetchImpl);
    expect(document.board.components).toHaveLength(3);

    const write = recorder({ ok: true, board: { ...document, board: FROZEN_ATLAS_STORED_BOARD } });
    await writeAtlasBoard(
      { projectId: 'demo', id: 'board-1', name: 'Starter', board: document.board, expectedRevision: 3 },
      write.fetchImpl,
    );
    const sent = body(write.calls[0]);
    expect(sent.expectedRevision).toBe(3);
    expect(write.calls[0].init?.method).toBe('PUT');
    // The graph is sent, not the editor's internal preserved-field wrappers.
    const board = sent.board as Record<string, unknown>;
    expect(board.schemaVersion).toBe(2);
    expect(board).not.toHaveProperty('preserved');
  });

  it('sends the expected revision on delete so a stale tab cannot remove newer work', async () => {
    const { calls, fetchImpl } = recorder({ ok: true });
    await deleteAtlasBoard('p', 'b', 7, fetchImpl);
    expect(calls[0].path).toContain('expectedRevision=7');
    expect(calls[0].init?.method).toBe('DELETE');
  });

  it('previews from a stored board id or from an unsaved draft', async () => {
    const byId = recorder({ ok: true, preview: {} });
    await previewAtlasRun('p', { boardId: 'b' }, byId.fetchImpl);
    expect(body(byId.calls[0])).toEqual({ boardId: 'b' });

    const byDraft = recorder({ ok: true, preview: {} });
    await previewAtlasRun('p', { board: defaultAtlasBoard() }, byDraft.fetchImpl);
    expect(body(byDraft.calls[0])).toHaveProperty('board.schemaVersion', 2);
  });

  it('starts a run from a stored board, never from the live draft', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, run: {} });
    await startAtlasRun(
      { projectId: 'p', boardId: 'b', source: { kind: 'text', title: 't', text: 'x' } },
      fetchImpl,
    );
    expect(body(calls[0])).toEqual({ boardId: 'b', source: { kind: 'text', title: 't', text: 'x' } });
    expect(body(calls[0])).not.toHaveProperty('board');
  });

  it('retries a stage by component and takes over by attempt', async () => {
    // A committee retries whole; an individual turn is only addressable by id.
    const retry = recorder({ ok: true, run: {} });
    await retryAtlasStage('p', 'r', 'plan', retry.fetchImpl);
    expect(retry.calls[0].path).toContain('/retry');
    expect(body(retry.calls[0])).toEqual({ componentId: 'plan' });

    for (const [call, suffix] of [
      [takeoverAtlasAttempt, '/takeover'],
      [handbackAtlasAttempt, '/handback'],
    ] as const) {
      const { calls, fetchImpl } = recorder({ ok: true, run: {} });
      await call('p', 'r', 'a-plan-2', fetchImpl);
      expect(calls[0].path).toContain(suffix);
      expect(body(calls[0])).toEqual({ attemptId: 'a-plan-2' });
    }
  });

  it('cancels the run rather than one seat', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, run: {} });
    await cancelAtlasRun('p', 'r', fetchImpl);
    expect(calls[0].path).toContain('/cancel');
    expect(body(calls[0])).toEqual({});
  });

  it('only sends publish:false when approval explicitly opts out', async () => {
    const optOut = recorder({ ok: true, run: {} });
    await decideAtlasGate('p', 'r', 'approved', { publish: false }, optOut.fetchImpl);
    expect(body(optOut.calls[0])).toEqual({ decision: 'approved', publish: false });

    const approve = recorder({ ok: true, run: {} });
    await decideAtlasGate('p', 'r', 'approved', {}, approve.fetchImpl);
    expect(body(approve.calls[0])).toEqual({ decision: 'approved' });

    const reject = recorder({ ok: true, run: {} });
    await decideAtlasGate('p', 'r', 'rejected', { publish: false }, reject.fetchImpl);
    expect(body(reject.calls[0])).toEqual({ decision: 'rejected' });
  });

  it('returns a terminal id for a reconnect and never a URL to an outside port', async () => {
    const { fetchImpl } = recorder({ ok: true, terminalId: 't-1', attemptId: 'a-1', writable: false });
    const handle = await reconnectAtlasTerminal('p', 'r', 'a-1', fetchImpl);
    expect(handle).toEqual({ terminalId: 't-1', attemptId: 'a-1', writable: false });
    expect(handle).not.toHaveProperty('url');
  });

  it('refuses a reconnect that carries no terminal identifier', async () => {
    const { fetchImpl } = recorder({ ok: true, attemptId: 'a-1' });
    await expect(reconnectAtlasTerminal('p', 'r', 'a-1', fetchImpl)).rejects.toThrow(AtlasApiError);
  });

  it('surfaces the stable code and the revision a conflict reports', async () => {
    const { fetchImpl } = recorder(
      { ok: false, code: 'REVISION_CONFLICT', error: 'the board changed', actualRevision: 9 },
      409,
    );
    const failure = await writeAtlasBoard(
      { projectId: 'p', id: 'b', name: 'n', board: defaultAtlasBoard(), expectedRevision: 3 },
      fetchImpl,
    ).catch((caught: unknown) => caught);
    expect(failure).toBeInstanceOf(AtlasApiError);
    expect((failure as AtlasApiError).isConflict).toBe(true);
    expect((failure as AtlasApiError).actualRevision).toBe(9);
  });

  it('treats an ok:false body on a 200 as a failure', async () => {
    const { fetchImpl } = recorder({ ok: false, code: 'PROJECT_NOT_OPEN', error: 'reopen it' }, 200);
    const failure = await listAtlasBoards('p', fetchImpl).catch((caught: unknown) => caught);
    expect((failure as AtlasApiError).isProjectNotOpen).toBe(true);
  });

  it('distinguishes an unreachable service from a rejected request', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('network down');
    });
    const failure = await listAtlasBoards('p', fetchImpl).catch((caught: unknown) => caught);
    expect((failure as AtlasApiError).code).toBe('NETWORK_ERROR');
    expect((failure as AtlasApiError).status).toBe(0);
  });
});
