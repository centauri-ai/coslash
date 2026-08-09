import { describe, expect, it, vi } from 'vitest';
import {
  cancelDaGamaRun,
  DaGamaApiError,
  decideDaGamaGate,
  deleteDaGamaBoard,
  listDaGamaBoards,
  openDaGamaProject,
  previewDaGamaRun,
  readDaGamaBoard,
  readDaGamaRunPrompt,
  reconnectDaGamaTerminal,
  retryDaGamaSeat,
  startDaGamaRun,
  takeoverDaGamaSeat,
  writeDaGamaBoard,
} from '@/plugins/canvas/dagama/api';
import { defaultDaGamaBoard, withComponent } from '@/plugins/canvas/dagama/board';
import { FROZEN_DAGAMA_STORED_BOARD } from '@/plugins/canvas/dagama/fixtures';

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

describe('DaGama API client', () => {
  it('scopes every call to the frozen /api/dagama prefix', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, project: { id: 'p', name: 'p', path: '/p' } });
    await openDaGamaProject('/p', fetchImpl);
    expect(calls[0].path).toBe('/api/dagama/projects/open');
    expect(calls[0].init?.method).toBe('POST');
    expect(body(calls[0])).toEqual({ path: '/p' });
  });

  it('carries the project through the query on every project-scoped route', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, boards: [], errors: [] });
    await listDaGamaBoards('demo project', fetchImpl);
    expect(calls[0].path).toBe('/api/dagama/boards?projectId=demo%20project');
  });

  it('encodes identifiers so a crafted id cannot escape its route segment', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, contents: '' });
    await readDaGamaRunPrompt('p', '../../etc', 'plan', fetchImpl);
    expect(calls[0].path).toContain('/api/dagama/runs/..%2F..%2Fetc/prompt');
  });

  it('normalizes a board it reads and re-emits its identity when writing it back', async () => {
    const read = recorder({
      ok: true,
      board: {
        schemaVersion: 1,
        id: 'board-1',
        name: 'Logout button',
        revision: 3,
        createdAt: '2026-08-09T05:00:00Z',
        updatedAt: '2026-08-09T05:14:00Z',
        board: FROZEN_DAGAMA_STORED_BOARD,
      },
    });
    const document = await readDaGamaBoard('demo-project', 'board-1', read.fetchImpl);
    expect(document.board.components.plan.seat?.vendor).toBe('claude');

    const write = recorder({ ok: true, board: { ...document, board: FROZEN_DAGAMA_STORED_BOARD } });
    await writeDaGamaBoard(
      {
        projectId: 'demo-project',
        id: 'board-1',
        name: 'Logout button',
        board: withComponent(document.board, 'plan', { prompt: 'careful' }),
        expectedRevision: 3,
      },
      write.fetchImpl,
    );
    const sent = body(write.calls[0]);
    expect(sent.expectedRevision).toBe(3);
    expect((sent.board as Record<string, unknown>).projectId).toBe('demo-project');
    expect(write.calls[0].init?.method).toBe('PUT');
  });

  it('sends the expected revision on delete so a stale tab cannot remove a newer board', async () => {
    const { calls, fetchImpl } = recorder({ ok: true });
    await deleteDaGamaBoard('p', 'b', 7, fetchImpl);
    expect(calls[0].path).toContain('expectedRevision=7');
    expect(calls[0].init?.method).toBe('DELETE');
  });

  it('previews from a stored board id or from an unsaved draft', async () => {
    const byId = recorder({ ok: true, preview: {} });
    await previewDaGamaRun('p', { boardId: 'b' }, byId.fetchImpl);
    expect(body(byId.calls[0])).toEqual({ boardId: 'b' });

    const byDraft = recorder({ ok: true, preview: {} });
    await previewDaGamaRun('p', { board: defaultDaGamaBoard() }, byDraft.fetchImpl);
    expect(body(byDraft.calls[0])).toHaveProperty('board.components.plan.seat.vendor', 'claude');
  });

  it('starts a run from a stored board, never from the live draft', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, run: {} });
    await startDaGamaRun(
      { projectId: 'p', boardId: 'b', source: { kind: 'text', title: 't', text: 'x' } },
      fetchImpl,
    );
    expect(body(calls[0])).toEqual({ boardId: 'b', source: { kind: 'text', title: 't', text: 'x' } });
    expect(body(calls[0])).not.toHaveProperty('board');
  });

  it('names the seat on every seat-scoped control', async () => {
    for (const [call, suffix] of [
      [retryDaGamaSeat, '/retry'],
      [takeoverDaGamaSeat, '/takeover'],
    ] as const) {
      const { calls, fetchImpl } = recorder({ ok: true, run: {} });
      await call('p', 'r', 'review', fetchImpl);
      expect(calls[0].path).toContain(suffix);
      expect(body(calls[0])).toEqual({ componentId: 'review' });
    }
  });

  it('sends an empty body when cancelling the whole run', async () => {
    const { calls, fetchImpl } = recorder({ ok: true, run: {} });
    await cancelDaGamaRun('p', 'r', undefined, fetchImpl);
    expect(body(calls[0])).toEqual({});
  });

  it('only sends publish:false when approval explicitly opts out', async () => {
    const optOut = recorder({ ok: true, run: {} });
    await decideDaGamaGate('p', 'r', 'approved', { publish: false }, optOut.fetchImpl);
    expect(body(optOut.calls[0])).toEqual({ decision: 'approved', publish: false });

    const approve = recorder({ ok: true, run: {} });
    await decideDaGamaGate('p', 'r', 'approved', {}, approve.fetchImpl);
    expect(body(approve.calls[0])).toEqual({ decision: 'approved' });

    const reject = recorder({ ok: true, run: {} });
    await decideDaGamaGate('p', 'r', 'rejected', { publish: false }, reject.fetchImpl);
    expect(body(reject.calls[0])).toEqual({ decision: 'rejected' });
  });

  it('returns a terminal id for a reconnect and never a URL to an outside port', async () => {
    const { fetchImpl } = recorder({ ok: true, terminalId: 't-1', attemptId: 'a-1', writable: true });
    const handle = await reconnectDaGamaTerminal('p', 'r', 'plan', fetchImpl);
    expect(handle).toEqual({ terminalId: 't-1', attemptId: 'a-1', writable: true, reused: undefined });
    expect(handle).not.toHaveProperty('url');
  });

  it('refuses a reconnect that carries no terminal identifier', async () => {
    const { fetchImpl } = recorder({ ok: true, attemptId: 'a-1' });
    await expect(reconnectDaGamaTerminal('p', 'r', 'plan', fetchImpl)).rejects.toThrow(DaGamaApiError);
  });

  it('surfaces the stable error code and the revision a conflict reports', async () => {
    const { fetchImpl } = recorder(
      { ok: false, code: 'REVISION_CONFLICT', error: 'the board changed', actualRevision: 9 },
      409,
    );
    const failure = await writeDaGamaBoard(
      { projectId: 'p', id: 'b', name: 'n', board: defaultDaGamaBoard(), expectedRevision: 3 },
      fetchImpl,
    ).catch((caught: unknown) => caught);
    expect(failure).toBeInstanceOf(DaGamaApiError);
    const error = failure as DaGamaApiError;
    expect(error.isConflict).toBe(true);
    expect(error.actualRevision).toBe(9);
    expect(error.message).toBe('the board changed');
  });

  it('treats an ok:false body on a 200 as a failure', async () => {
    const { fetchImpl } = recorder({ ok: false, code: 'PROJECT_NOT_OPEN', error: 'reopen it' }, 200);
    const failure = await listDaGamaBoards('p', fetchImpl).catch((caught: unknown) => caught);
    expect((failure as DaGamaApiError).isProjectNotOpen).toBe(true);
  });

  it('distinguishes an unreachable service from a rejected request', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('network down');
    });
    const failure = await listDaGamaBoards('p', fetchImpl).catch((caught: unknown) => caught);
    expect((failure as DaGamaApiError).code).toBe('NETWORK_ERROR');
    expect((failure as DaGamaApiError).status).toBe(0);
  });
});
