import { describe, expect, it, vi } from 'vitest';
import { atlasBoardSignature, defaultAtlasBoard, withComponent } from '@/plugins/canvas/atlas/graph';
import { createAtlasBoardSession } from '@/plugins/canvas/atlas/session';

/** A debounce the test drives, so a save happens exactly when it decides. */
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
  };
}

type ServerBoard = { revision: number; graph: unknown; name: string };

/**
 * A fake Atlas board service enforcing the same optimistic revision rule the
 * collector does, so the conflict tests are meaningful rather than mocked.
 */
function fakeServer(initial?: Partial<ServerBoard>) {
  const stored: ServerBoard = {
    revision: initial?.revision ?? 1,
    name: initial?.name ?? 'Starter',
    graph: initial?.graph ?? { ...JSON.parse(JSON.stringify(defaultAtlasBoard())), schemaVersion: 2 },
  };
  const writes: Array<Record<string, unknown>> = [];

  const json = (value: unknown, status = 200) =>
    new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

  const document = () => ({
    schemaVersion: 1,
    id: 'board-1',
    name: stored.name,
    revision: stored.revision,
    createdAt: '2026-08-09T07:00:00Z',
    updatedAt: '2026-08-09T07:10:00Z',
    board: stored.graph,
  });

  const fetchImpl = vi.fn(async (path: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    const request = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>;
    if (path.startsWith('/api/atlas/projects/open')) {
      return json({ ok: true, project: { id: 'demo', name: 'demo', path: '/demo' } });
    }
    if (path.startsWith('/api/atlas/boards?')) {
      return json({ ok: true, boards: [{ ...document(), board: undefined }], errors: [] });
    }
    if (path.startsWith('/api/atlas/boards/board-1')) {
      if (method === 'GET') return json({ ok: true, board: document() });
      if (method === 'PUT') {
        writes.push(request);
        if (request.expectedRevision !== stored.revision) {
          return json(
            {
              ok: false,
              code: 'REVISION_CONFLICT',
              error: 'the workflow changed since it was loaded',
              actualRevision: stored.revision,
            },
            409,
          );
        }
        stored.revision += 1;
        stored.graph = request.board;
        stored.name = String(request.name);
        return json({ ok: true, board: document() });
      }
      if (method === 'DELETE') return json({ ok: true });
    }
    return json({ ok: false, code: 'NOT_FOUND', error: 'unknown route' }, 404);
  });

  return {
    fetchImpl,
    writes,
    stored,
    advance() {
      stored.revision += 1;
    },
  };
}

describe('Atlas board session', () => {
  it('opens a project and adopts its stored workflow', async () => {
    const server = fakeServer();
    const session = createAtlasBoardSession({ fetch: server.fetchImpl });
    await session.chooseProject('/demo');
    const snapshot = session.snapshot();
    expect(snapshot.project?.id).toBe('demo');
    expect(snapshot.activeBoard?.id).toBe('board-1');
    expect(snapshot.saveState).toBe('saved');
    expect(snapshot.readOnly).toBe(false);
  });

  it('coalesces rapid edits into one write without dropping the last', async () => {
    const server = fakeServer();
    const timer = manualTimer();
    const session = createAtlasBoardSession({
      fetch: server.fetchImpl,
      setTimer: timer.setTimer,
      clearTimer: timer.clearTimer,
    });
    await session.chooseProject('/demo');

    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'a' }));
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'ab' }));
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'abc' }));
    expect(session.snapshot().saveState).toBe('saving');

    timer.fire();
    await session.flush();

    expect(server.writes).toHaveLength(1);
    const graph = server.writes[0].board as { components: Array<{ id: string; prompt: string }> };
    expect(graph.components.find((component) => component.id === 'plan')?.prompt).toBe('abc');
    expect(session.snapshot().saveState).toBe('saved');
  });

  it('raises a visible conflict and keeps the local edit', async () => {
    const server = fakeServer();
    const timer = manualTimer();
    const session = createAtlasBoardSession({
      fetch: server.fetchImpl,
      setTimer: timer.setTimer,
      clearTimer: timer.clearTimer,
    });
    await session.chooseProject('/demo');
    server.advance();

    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'mine' }));
    timer.fire();
    await session.flush();

    const snapshot = session.snapshot();
    expect(snapshot.saveState).toBe('conflict');
    expect(snapshot.serverRevision).toBe(2);
    // The operator's work is never discarded by a refused save.
    expect(snapshot.board.components.find((component) => component.id === 'plan')?.prompt).toBe('mine');
  });

  it('offers both recoveries for a conflict', async () => {
    const server = fakeServer();
    const timer = manualTimer();
    const build = () =>
      createAtlasBoardSession({
        fetch: server.fetchImpl,
        setTimer: timer.setTimer,
        clearTimer: timer.clearTimer,
      });

    const keep = build();
    await keep.chooseProject('/demo');
    server.advance();
    keep.edit(withComponent(keep.snapshot().board, 'plan', { prompt: 'mine' }));
    timer.fire();
    await keep.flush();
    await keep.keepLocal();
    expect(keep.snapshot().saveState).toBe('saved');
    expect(keep.snapshot().board.components.find((component) => component.id === 'plan')?.prompt).toBe(
      'mine',
    );

    const discard = build();
    await discard.chooseProject('/demo');
    server.advance();
    discard.edit(withComponent(discard.snapshot().board, 'plan', { prompt: 'local only' }));
    timer.fire();
    await discard.flush();
    await discard.reloadFromServer();
    expect(discard.snapshot().saveState).toBe('saved');
    expect(discard.snapshot().board.components.find((component) => component.id === 'plan')?.prompt).not.toBe(
      'local only',
    );
  });

  it('opens a board written by a newer build read-only and refuses to edit it', async () => {
    // Saving a schema we cannot read would rewrite whatever that build stored.
    const server = fakeServer({ graph: { schemaVersion: 99, components: [{ id: 'plan' }] } });
    const timer = manualTimer();
    const session = createAtlasBoardSession({
      fetch: server.fetchImpl,
      setTimer: timer.setTimer,
      clearTimer: timer.clearTimer,
    });
    await session.chooseProject('/demo');

    expect(session.snapshot().readOnly).toBe(true);
    expect(session.snapshot().error).toContain('read-only');

    const before = atlasBoardSignature(session.snapshot().board);
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'nope' }));
    // The edit is refused outright rather than accepted and then unsaveable,
    // which would lose the work at the worst possible moment.
    expect(atlasBoardSignature(session.snapshot().board)).toBe(before);
    timer.fire();
    await session.flush();
    expect(server.writes).toHaveLength(0);
  });

  it('reports a board migrated from the record-shaped schema', async () => {
    const server = fakeServer({
      graph: {
        schemaVersion: 1,
        components: { plan: { prompt: 'p' }, build: { prompt: 'b' }, review: { prompt: 'r' } },
      },
    });
    const session = createAtlasBoardSession({ fetch: server.fetchImpl });
    await session.chooseProject('/demo');

    expect(session.snapshot().migrated).toBe(true);
    // A migrated board is ordinary and editable; only an unknown one is not.
    expect(session.snapshot().readOnly).toBe(false);
    expect(session.snapshot().board.schemaVersion).toBe(2);
  });

  it('settles clean when an edit lands back on the stored value', async () => {
    const server = fakeServer();
    const timer = manualTimer();
    const session = createAtlasBoardSession({
      fetch: server.fetchImpl,
      setTimer: timer.setTimer,
      clearTimer: timer.clearTimer,
    });
    await session.chooseProject('/demo');
    const original = session.snapshot().board;

    session.edit(withComponent(original, 'plan', { prompt: 'typo' }));
    session.edit(original);
    expect(session.snapshot().saveState).toBe('saved');

    timer.fire();
    await session.flush();
    expect(server.writes).toHaveLength(0);
  });

  it('stops writing while a conflict is unresolved', async () => {
    const server = fakeServer();
    const timer = manualTimer();
    const session = createAtlasBoardSession({
      fetch: server.fetchImpl,
      setTimer: timer.setTimer,
      clearTimer: timer.clearTimer,
    });
    await session.chooseProject('/demo');
    server.advance();
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'one' }));
    timer.fire();
    await session.flush();
    const attempts = server.writes.length;

    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'two' }));
    timer.fire();
    await session.flush();
    expect(server.writes).toHaveLength(attempts);
  });

  it('returns to a fresh starter chain on new workflow', async () => {
    const server = fakeServer();
    const session = createAtlasBoardSession({ fetch: server.fetchImpl });
    await session.chooseProject('/demo');
    session.newBoard();
    expect(session.snapshot().activeBoard).toBeNull();
    expect(session.snapshot().saveState).toBe('draft');
    expect(atlasBoardSignature(session.snapshot().board)).toBe(atlasBoardSignature(defaultAtlasBoard()));
  });

  it('stops publishing after dispose', async () => {
    const server = fakeServer();
    const timer = manualTimer();
    const session = createAtlasBoardSession({
      fetch: server.fetchImpl,
      setTimer: timer.setTimer,
      clearTimer: timer.clearTimer,
    });
    await session.chooseProject('/demo');
    const listener = vi.fn();
    session.subscribe(listener);
    session.dispose();
    session.edit(withComponent(session.snapshot().board, 'plan', { prompt: 'ignored' }));
    timer.fire();
    expect(listener).not.toHaveBeenCalled();
  });
});
