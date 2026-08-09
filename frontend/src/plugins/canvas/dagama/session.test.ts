import { describe, expect, it, vi } from 'vitest';
import { daGamaBoardSignature, defaultDaGamaBoard, withComponent } from '@/plugins/canvas/dagama/board';
import type { DaGamaStorageLike } from '@/plugins/canvas/dagama/preferences';
import { createDaGamaBoardSession, type DaGamaBoardSession } from '@/plugins/canvas/dagama/session';

function memoryStorage(seed: Record<string, string> = {}): DaGamaStorageLike & { map: Map<string, string> } {
  const map = new Map(Object.entries(seed));
  return {
    map,
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  };
}

/** A controllable debounce so a test can decide exactly when a save is attempted. */
function manualTimer() {
  let pending: (() => void) | null = null;
  return {
    setTimer: (handler: () => void) => {
      pending = handler;
      return 1;
    },
    clearTimer: () => {
      pending = null;
    },
    fire: () => {
      const handler = pending;
      pending = null;
      handler?.();
    },
    get scheduled() {
      return pending !== null;
    },
  };
}

type ServerBoard = { revision: number; board: unknown; name: string };

/**
 * A fake DaGama board service.
 *
 * It enforces the same optimistic-revision rule the collector does, which is
 * what makes the conflict tests meaningful rather than mocked.
 */
function fakeServer(initial?: Partial<ServerBoard>) {
  const stored: ServerBoard = {
    revision: initial?.revision ?? 1,
    name: initial?.name ?? 'Logout button',
    board: initial?.board ?? {
      ...JSON.parse(JSON.stringify(defaultDaGamaBoard())),
      id: 'board-1',
      projectId: 'demo-project',
    },
  };
  const writes: Array<Record<string, unknown>> = [];
  let readFailures = 0;

  const json = (value: unknown, status = 200) =>
    new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

  const fetchImpl = vi.fn(async (path: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    const request = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>;
    if (path.startsWith('/api/dagama/projects/open')) {
      return json({ ok: true, project: { id: 'demo-project', name: 'demo', path: '/demo' } });
    }
    if (path.startsWith('/api/dagama/boards?')) {
      return json({
        ok: true,
        boards: [
          {
            schemaVersion: 1,
            id: 'board-1',
            name: stored.name,
            revision: stored.revision,
            createdAt: '2026-08-09T05:00:00Z',
            updatedAt: '2026-08-09T05:10:00Z',
          },
        ],
        errors: [],
      });
    }
    if (path.startsWith('/api/dagama/boards/board-1')) {
      if (method === 'GET') {
        if (readFailures > 0) {
          readFailures -= 1;
          return json({ ok: false, code: 'PROJECT_NOT_OPEN', error: 'reopen' }, 409);
        }
        return json({
          ok: true,
          board: {
            schemaVersion: 1,
            id: 'board-1',
            name: stored.name,
            revision: stored.revision,
            createdAt: '2026-08-09T05:00:00Z',
            updatedAt: '2026-08-09T05:10:00Z',
            board: stored.board,
          },
        });
      }
      if (method === 'PUT') {
        writes.push(request);
        if (request.expectedRevision !== stored.revision) {
          return json(
            {
              ok: false,
              code: 'REVISION_CONFLICT',
              error: 'the board changed since it was loaded',
              actualRevision: stored.revision,
            },
            409,
          );
        }
        stored.revision += 1;
        stored.board = request.board;
        stored.name = String(request.name);
        return json({
          ok: true,
          board: {
            schemaVersion: 1,
            id: 'board-1',
            name: stored.name,
            revision: stored.revision,
            createdAt: '2026-08-09T05:00:00Z',
            updatedAt: '2026-08-09T05:12:00Z',
            board: stored.board,
          },
        });
      }
      if (method === 'DELETE') return json({ ok: true });
    }
    if (path.startsWith('/api/dagama/boards/')) {
      // A save-as writes a new identifier.
      writes.push(request);
      stored.revision = 1;
      stored.board = request.board;
      stored.name = String(request.name);
      return json({
        ok: true,
        board: {
          schemaVersion: 1,
          id: 'board-new',
          name: stored.name,
          revision: 1,
          createdAt: '2026-08-09T05:20:00Z',
          updatedAt: '2026-08-09T05:20:00Z',
          board: stored.board,
        },
      });
    }
    return json({ ok: false, code: 'NOT_FOUND', error: 'unknown route' }, 404);
  });

  return {
    fetchImpl,
    writes,
    stored,
    /** Simulate another writer advancing the stored board. */
    advance() {
      stored.revision += 1;
    },
    failNextBoardRead() {
      readFailures += 1;
    },
  };
}

async function openedSession(
  server: ReturnType<typeof fakeServer>,
  timer = manualTimer(),
  storage = memoryStorage(),
): Promise<{
  session: DaGamaBoardSession;
  timer: ReturnType<typeof manualTimer>;
  storage: ReturnType<typeof memoryStorage>;
}> {
  const session = createDaGamaBoardSession({
    fetch: server.fetchImpl,
    storage,
    setTimer: timer.setTimer,
    clearTimer: timer.clearTimer,
    newBoardId: () => 'board-new',
  });
  await session.chooseProject('/demo');
  return { session, timer, storage };
}

describe('DaGama board session', () => {
  it('opens a project and adopts its most recent board', async () => {
    const server = fakeServer();
    const { session } = await openedSession(server);
    const snapshot = session.snapshot();
    expect(snapshot.project?.id).toBe('demo-project');
    expect(snapshot.activeBoard?.id).toBe('board-1');
    expect(snapshot.saveState).toBe('saved');
    expect(snapshot.boards).toHaveLength(1);
  });

  it('coalesces rapid edits into one write without dropping the last one', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);

    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'a' }));
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'ab' }));
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'abc' }));
    expect(session.snapshot().saveState).toBe('saving');

    timer.fire();
    await session.flush();

    expect(server.writes).toHaveLength(1);
    expect(
      (server.writes[0].board as { components: Record<string, { prompt: string }> }).components.plan.prompt,
    ).toBe('abc');
    expect(session.snapshot().saveState).toBe('saved');
  });

  it('settles clean when an edit lands back on the stored value', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);
    const original = session.snapshot().board;

    session.edit(withComponent(original, 'plan', { prompt: 'typo' }));
    session.edit(original);

    expect(session.snapshot().saveState).toBe('saved');
    timer.fire();
    await session.flush();
    expect(server.writes).toHaveLength(0);
  });

  it('raises a visible conflict and keeps the local edit when the server moved on', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);
    server.advance();

    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'mine' }));
    timer.fire();
    await session.flush();

    const snapshot = session.snapshot();
    expect(snapshot.saveState).toBe('conflict');
    expect(snapshot.serverRevision).toBe(2);
    expect(snapshot.board.components.plan.prompt).toBe('mine');
  });

  it('resolves a conflict by rebasing the local edit onto the reported revision', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);
    server.advance();
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'mine' }));
    timer.fire();
    await session.flush();

    await session.keepLocal();

    expect(session.snapshot().saveState).toBe('saved');
    expect(session.snapshot().board.components.plan.prompt).toBe('mine');
    expect(
      (server.stored.board as { components: Record<string, { prompt: string }> }).components.plan.prompt,
    ).toBe('mine');
  });

  it('resolves a conflict the other way by discarding local edits for the stored board', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);
    server.advance();
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'mine' }));
    timer.fire();
    await session.flush();

    await session.reloadFromServer();

    expect(session.snapshot().saveState).toBe('saved');
    expect(session.snapshot().board.components.plan.prompt).toBe('');
  });

  it('stops writing while a conflict is unresolved instead of looping on a refused save', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);
    server.advance();
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'one' }));
    timer.fire();
    await session.flush();
    const attempts = server.writes.length;

    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'two' }));
    timer.fire();
    await session.flush();

    expect(server.writes).toHaveLength(attempts);
    expect(session.snapshot().saveState).toBe('conflict');
  });

  it('refuses to leave a project while a conflict is unresolved', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);
    server.advance();
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'mine' }));
    timer.fire();
    await session.flush();

    await expect(session.chooseProject('/other')).rejects.toThrow(/conflict/i);
  });

  it('reopens a project the collector forgot and retries once', async () => {
    const server = fakeServer();
    const storage = memoryStorage();
    const session = createDaGamaBoardSession({ fetch: server.fetchImpl, storage });
    await session.chooseProject('/demo');
    const opensBefore = server.fetchImpl.mock.calls.filter(
      ([path]) => path === '/api/dagama/projects/open',
    ).length;

    server.failNextBoardRead();
    await session.openBoard('board-1');

    const opensAfter = server.fetchImpl.mock.calls.filter(
      ([path]) => path === '/api/dagama/projects/open',
    ).length;
    // The collector restarted and forgot the project; the client reopened it by
    // the path it already held rather than stranding the operator in an error.
    expect(opensAfter).toBe(opensBefore + 1);
    expect(session.snapshot().saveState).toBe('saved');
  });

  it('saves an unnamed draft as a new board and adopts it', async () => {
    const server = fakeServer();
    const storage = memoryStorage();
    const session = createDaGamaBoardSession({
      fetch: server.fetchImpl,
      storage,
      newBoardId: () => 'board-new',
    });
    session.edit(withComponent(session.snapshot().board, 'build', { prompt: 'fresh' }));
    // No project yet: saving must refuse rather than invent one.
    await expect(session.saveAs('Fresh')).rejects.toThrow(/project/i);

    await session.chooseProject('/demo');
    session.edit(withComponent(session.snapshot().board, 'build', { prompt: 'fresh' }));
    const summary = await session.saveAs('Fresh');

    expect(summary.id).toBe('board-new');
    expect(session.snapshot().activeBoard?.id).toBe('board-new');
    expect(session.snapshot().board.components.build.prompt).toBe('fresh');
  });

  it('keeps a draft configured before a project was chosen', async () => {
    const server = fakeServer();
    const storage = memoryStorage();
    const session = createDaGamaBoardSession({ fetch: server.fetchImpl, storage });
    session.edit(withComponent(session.snapshot().board, 'intake', { prompt: 'drafted first' }));

    await session.chooseProject('/demo');

    expect(session.snapshot().board.components.intake.prompt).toBe('drafted first');
    expect(session.snapshot().activeBoard).toBeNull();
    expect(session.snapshot().saveState).toBe('draft');
  });

  it('resumes an interrupted autosave when the recovery draft matches the stored revision', async () => {
    const server = fakeServer();
    const recovered = withComponent(defaultDaGamaBoard(), 'plan', { prompt: 'never landed' });
    const storage = memoryStorage({
      'coslash.canvas.dagama.project.v1': '/demo',
      'coslash.canvas.dagama.boardId.v1.demo-project': 'board-1',
      'coslash.canvas.dagama.draft.v1': JSON.stringify(recovered),
      'coslash.canvas.dagama.draftMetadata.v1': JSON.stringify({
        projectId: 'demo-project',
        boardId: 'board-1',
        revision: 1,
      }),
    });
    const timer = manualTimer();
    const session = createDaGamaBoardSession({
      fetch: server.fetchImpl,
      storage,
      setTimer: timer.setTimer,
      clearTimer: timer.clearTimer,
    });

    await session.restore();
    expect(session.snapshot().saveState).toBe('saving');
    expect(session.snapshot().board.components.plan.prompt).toBe('never landed');

    timer.fire();
    await session.flush();
    expect(session.snapshot().saveState).toBe('saved');
    expect(server.writes).toHaveLength(1);
  });

  it('raises a conflict when the recovery draft was bound to an older revision', async () => {
    const server = fakeServer({ revision: 5 });
    const recovered = withComponent(defaultDaGamaBoard(), 'plan', { prompt: 'stale local' });
    const storage = memoryStorage({
      'coslash.canvas.dagama.project.v1': '/demo',
      'coslash.canvas.dagama.boardId.v1.demo-project': 'board-1',
      'coslash.canvas.dagama.draft.v1': JSON.stringify(recovered),
      'coslash.canvas.dagama.draftMetadata.v1': JSON.stringify({
        projectId: 'demo-project',
        boardId: 'board-1',
        revision: 2,
      }),
    });
    const session = createDaGamaBoardSession({ fetch: server.fetchImpl, storage });

    await session.restore();

    expect(session.snapshot().saveState).toBe('conflict');
    expect(session.snapshot().serverRevision).toBe(5);
    expect(session.snapshot().board.components.plan.prompt).toBe('stale local');
    expect(server.writes).toHaveLength(0);
  });

  it('returns to a fresh draft after deleting the open board', async () => {
    const server = fakeServer();
    const { session, storage } = await openedSession(server);
    const summary = session.snapshot().activeBoard!;

    await session.deleteBoard(summary);

    expect(session.snapshot().activeBoard).toBeNull();
    expect(session.snapshot().boards).toHaveLength(0);
    expect(daGamaBoardSignature(session.snapshot().board)).toBe(daGamaBoardSignature(defaultDaGamaBoard()));
    expect(storage.map.get('coslash.canvas.dagama.boardId.v1.demo-project')).toBeUndefined();
  });

  it('records the last project and board so a reload reopens them', async () => {
    const server = fakeServer();
    const { storage } = await openedSession(server);
    expect(storage.map.get('coslash.canvas.dagama.project.v1')).toBe('/demo');
    expect(storage.map.get('coslash.canvas.dagama.boardId.v1.demo-project')).toBe('board-1');
  });

  it('survives storage that refuses to write', async () => {
    const server = fakeServer();
    const hostile: DaGamaStorageLike = {
      getItem: () => null,
      setItem: () => {
        throw new Error('quota exceeded');
      },
      removeItem: () => {
        throw new Error('quota exceeded');
      },
    };
    const session = createDaGamaBoardSession({ fetch: server.fetchImpl, storage: hostile });
    await session.chooseProject('/demo');
    expect(session.snapshot().activeBoard?.id).toBe('board-1');
  });

  it('stops publishing after dispose', async () => {
    const server = fakeServer();
    const { session, timer } = await openedSession(server);
    const listener = vi.fn();
    session.subscribe(listener);
    session.dispose();
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'ignored' }));
    timer.fire();
    expect(listener).not.toHaveBeenCalled();
  });
});
