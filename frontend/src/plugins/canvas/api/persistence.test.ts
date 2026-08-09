import { describe, expect, it } from 'vitest';
import {
  CanvasPersistenceError,
  createWorkspaceClient,
  loadWorkspace,
  saveWorkspace,
  workspacePath,
} from '@/plugins/canvas/api/persistence';
import type { CanvasSessionIdentity } from '@/plugins/canvas/contracts';

type State = { counter: number };

const session: CanvasSessionIdentity = { agent: 'claude', id: 'session-1' };

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function document(revision: number, state: State | null) {
  return {
    schemaVersion: 1,
    revision,
    session,
    state,
    updatedAt: '2026-08-08T22:00:00Z',
  };
}

/** A scheduler that runs debounced work only when the test asks for it. */
function manualTimers() {
  const pending = new Map<number, () => void>();
  let nextHandle = 1;
  return {
    setTimer(handler: () => void): unknown {
      const handle = nextHandle++;
      pending.set(handle, handler);
      return handle;
    },
    clearTimer(handle: unknown): void {
      pending.delete(handle as number);
    },
    run(): void {
      const entries = [...pending.entries()];
      pending.clear();
      for (const [, handler] of entries) handler();
    },
    get size(): number {
      return pending.size;
    },
  };
}

/** A save transport whose responses are released one at a time. */
function deferredSaves() {
  const releases: ((response: Response) => void)[] = [];
  const bodies: string[] = [];
  const fetchImpl = (_path: string, init: RequestInit = {}) => {
    if (init.method !== 'PUT') return Promise.resolve(jsonResponse(document(0, null)));
    bodies.push(String(init.body));
    return new Promise<Response>((resolve) => releases.push(resolve));
  };
  return {
    fetchImpl,
    bodies,
    get pending(): number {
      return releases.length;
    },
    release(response: Response): void {
      const next = releases.shift();
      if (next == null) throw new Error('no pending save to release');
      next(response);
    },
  };
}

const flushMicrotasks = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('workspacePath', () => {
  it('encodes both identity components as separate segments', () => {
    expect(workspacePath({ agent: 'claude', id: 'abc' })).toBe('/api/canvas/workspaces/claude/abc');
  });

  it('escapes characters that would otherwise change the route shape', () => {
    expect(workspacePath({ agent: 'cl/aude', id: '../escape' })).toBe(
      '/api/canvas/workspaces/cl%2Faude/..%2Fescape',
    );
  });
});

describe('loadWorkspace and saveWorkspace', () => {
  it('returns the stored document', async () => {
    const result = await loadWorkspace<State>(session, () =>
      Promise.resolve(jsonResponse(document(3, { counter: 7 }))),
    );
    expect(result.revision).toBe(3);
    expect(result.state).toEqual({ counter: 7 });
  });

  it('sends the write envelope as JSON', async () => {
    let captured: RequestInit = {};
    await saveWorkspace<State>(
      session,
      { schemaVersion: 1, expectedRevision: 2, state: { counter: 1 } },
      (_path, init = {}) => {
        captured = init;
        return Promise.resolve(jsonResponse(document(3, { counter: 1 })));
      },
    );
    expect(captured.method).toBe('PUT');
    expect(JSON.parse(String(captured.body))).toEqual({
      schemaVersion: 1,
      expectedRevision: 2,
      state: { counter: 1 },
    });
  });

  it('raises a typed error carrying the server code and revision', async () => {
    const failing = () =>
      Promise.resolve(
        jsonResponse({ ok: false, code: 'REVISION_CONFLICT', error: 'stale', actualRevision: 9 }, 409),
      );
    const error = await loadWorkspace<State>(session, failing).catch((caught) => caught);
    expect(error).toBeInstanceOf(CanvasPersistenceError);
    expect(error.code).toBe('REVISION_CONFLICT');
    expect(error.actualRevision).toBe(9);
    expect(error.isConflict).toBe(true);
  });

  it('does not crash when a failure body is not JSON', async () => {
    const error = await loadWorkspace<State>(session, () =>
      Promise.resolve(new Response('gateway exploded', { status: 502 })),
    ).catch((caught) => caught);
    expect(error).toBeInstanceOf(CanvasPersistenceError);
    expect(error.code).toBe('PERSISTENCE_FAILED');
    expect(error.status).toBe(502);
  });
});

describe('createWorkspaceClient', () => {
  it('loads stored state and reports a clean snapshot', async () => {
    const client = createWorkspaceClient<State>({
      session,
      fetch: () => Promise.resolve(jsonResponse(document(4, { counter: 2 }))),
    });
    await client.load();

    const snapshot = client.snapshot();
    expect(snapshot.state).toEqual({ counter: 2 });
    expect(snapshot.revision).toBe(4);
    expect(snapshot.loaded).toBe(true);
    expect(snapshot.dirty).toBe(false);
    expect(snapshot.status).toBe('idle');
  });

  it('treats a never-saved workspace as revision 0 with no state', async () => {
    const client = createWorkspaceClient<State>({
      session,
      fetch: () => Promise.resolve(jsonResponse(document(0, null))),
    });
    await client.load();
    expect(client.snapshot().state).toBeNull();
    expect(client.snapshot().revision).toBe(0);
    expect(client.snapshot().loaded).toBe(true);
  });

  it('coalesces rapid edits into a single debounced save', async () => {
    const timers = manualTimers();
    const saves: string[] = [];
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: (_path, init = {}) => {
        if (init.method === 'PUT') saves.push(String(init.body));
        return Promise.resolve(jsonResponse(document(1, { counter: 3 })));
      },
    });

    client.update({ counter: 1 });
    client.update({ counter: 2 });
    client.update({ counter: 3 });
    expect(saves).toHaveLength(0);
    expect(client.snapshot().dirty).toBe(true);

    timers.run();
    await flushMicrotasks();

    expect(saves).toHaveLength(1);
    expect(JSON.parse(saves[0]).state).toEqual({ counter: 3 });
    expect(client.snapshot().dirty).toBe(false);
    expect(client.snapshot().status).toBe('saved');
  });

  it('never lets a stale response mark newer edits as saved', async () => {
    const timers = manualTimers();
    const transport = deferredSaves();
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: transport.fetchImpl,
    });

    client.update({ counter: 1 });
    timers.run();
    await flushMicrotasks();
    expect(transport.pending).toBe(1);

    // A newer edit arrives while the first save is still in flight.
    client.update({ counter: 2 });
    transport.release(jsonResponse(document(1, { counter: 1 })));
    await flushMicrotasks();

    // The in-flight response belongs to the older generation, so the client
    // must still consider itself dirty and must not present counter 1.
    expect(client.snapshot().state).toEqual({ counter: 2 });
    expect(client.snapshot().dirty).toBe(true);

    // It automatically writes the newer generation on top of revision 1.
    expect(transport.pending).toBe(1);
    expect(JSON.parse(transport.bodies[1])).toEqual({
      schemaVersion: 1,
      expectedRevision: 1,
      state: { counter: 2 },
    });

    transport.release(jsonResponse(document(2, { counter: 2 })));
    await flushMicrotasks();
    expect(client.snapshot().dirty).toBe(false);
    expect(client.snapshot().revision).toBe(2);
  });

  it('keeps local edits when a load completes after them', async () => {
    let release: (response: Response) => void = () => {};
    const client = createWorkspaceClient<State>({
      session,
      fetch: () => new Promise<Response>((resolve) => (release = resolve)),
    });

    const loading = client.load();
    client.update({ counter: 42 });
    release(jsonResponse(document(5, { counter: 1 })));
    await loading;

    expect(client.snapshot().state).toEqual({ counter: 42 });
    expect(client.snapshot().dirty).toBe(true);
    // The next write rebases onto the revision observed during the load.
    expect(client.snapshot().revision).toBe(5);
  });

  it('does not let a stale load roll back a revision confirmed by a newer save', async () => {
    const timers = manualTimers();
    let releaseLoad: (response: Response) => void = () => {};
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: (_path, init = {}) => {
        if (init.method === 'PUT') {
          return Promise.resolve(jsonResponse(document(2, { counter: 2 })));
        }
        return new Promise<Response>((resolve) => (releaseLoad = resolve));
      },
    });

    const loading = client.load();
    client.update({ counter: 2 });
    timers.run();
    await flushMicrotasks();
    expect(client.snapshot().revision).toBe(2);
    expect(client.snapshot().status).toBe('saved');

    releaseLoad(jsonResponse(document(1, { counter: 1 })));
    await loading;
    expect(client.snapshot().revision).toBe(2);
    expect(client.snapshot().state).toEqual({ counter: 2 });
    expect(client.snapshot().status).toBe('saved');
    expect(client.snapshot().dirty).toBe(false);
    client.dispose();
  });

  it('surfaces a conflict without discarding local work', async () => {
    const timers = manualTimers();
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: (_path, init = {}) =>
        Promise.resolve(
          init.method === 'PUT'
            ? jsonResponse({ ok: false, code: 'REVISION_CONFLICT', error: 'stale', actualRevision: 8 }, 409)
            : jsonResponse(document(0, null)),
        ),
    });

    client.update({ counter: 5 });
    timers.run();
    await flushMicrotasks();

    const snapshot = client.snapshot();
    expect(snapshot.status).toBe('conflict');
    expect(snapshot.dirty).toBe(true);
    expect(snapshot.state).toEqual({ counter: 5 });
    expect(snapshot.error?.actualRevision).toBe(8);
  });

  it('resolves a conflict by rebasing local state onto the server revision', async () => {
    const timers = manualTimers();
    const bodies: string[] = [];
    let conflict = true;
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: (_path, init = {}) => {
        if (init.method !== 'PUT') return Promise.resolve(jsonResponse(document(0, null)));
        bodies.push(String(init.body));
        if (conflict) {
          conflict = false;
          return Promise.resolve(
            jsonResponse({ ok: false, code: 'REVISION_CONFLICT', error: 'stale', actualRevision: 8 }, 409),
          );
        }
        return Promise.resolve(jsonResponse(document(9, { counter: 5 })));
      },
    });

    client.update({ counter: 5 });
    timers.run();
    await flushMicrotasks();
    expect(client.snapshot().status).toBe('conflict');

    await client.resolveWithLocal();

    expect(JSON.parse(bodies[1]).expectedRevision).toBe(8);
    expect(client.snapshot().status).toBe('saved');
    expect(client.snapshot().dirty).toBe(false);
    expect(client.snapshot().revision).toBe(9);
    expect(client.snapshot().error).toBeNull();
  });

  it('discards local edits when the caller reloads from the server', async () => {
    const client = createWorkspaceClient<State>({
      session,
      fetch: () => Promise.resolve(jsonResponse(document(6, { counter: 99 }))),
    });

    client.update({ counter: 1 });
    await client.reloadFromServer();

    expect(client.snapshot().state).toEqual({ counter: 99 });
    expect(client.snapshot().dirty).toBe(false);
    expect(client.snapshot().revision).toBe(6);
  });

  it('leaves state usable and visibly unsaved when the network fails', async () => {
    const timers = manualTimers();
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: () => Promise.reject(new TypeError('offline')),
    });

    client.update({ counter: 4 });
    timers.run();
    await flushMicrotasks();

    const snapshot = client.snapshot();
    expect(snapshot.status).toBe('error');
    expect(snapshot.dirty).toBe(true);
    expect(snapshot.state).toEqual({ counter: 4 });
    expect(snapshot.error).toBeInstanceOf(CanvasPersistenceError);
  });

  it('retries after a failure when the caller flushes again', async () => {
    const timers = manualTimers();
    let fail = true;
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: (_path, init = {}) => {
        if (init.method !== 'PUT') return Promise.resolve(jsonResponse(document(0, null)));
        if (fail) {
          fail = false;
          return Promise.reject(new TypeError('offline'));
        }
        return Promise.resolve(jsonResponse(document(1, { counter: 4 })));
      },
    });

    client.update({ counter: 4 });
    timers.run();
    await flushMicrotasks();
    expect(client.snapshot().status).toBe('error');

    await client.flush();
    expect(client.snapshot().status).toBe('saved');
    expect(client.snapshot().dirty).toBe(false);
  });

  it('notifies subscribers and stops after unsubscribe', async () => {
    let notifications = 0;
    const client = createWorkspaceClient<State>({
      session,
      fetch: () => Promise.resolve(jsonResponse(document(1, { counter: 1 }))),
    });
    const unsubscribe = client.subscribe(() => {
      notifications += 1;
    });

    client.update({ counter: 1 });
    expect(notifications).toBe(1);

    unsubscribe();
    client.update({ counter: 2 });
    expect(notifications).toBe(1);
  });

  it('cancels pending work on dispose', async () => {
    const timers = manualTimers();
    const saves: string[] = [];
    const client = createWorkspaceClient<State>({
      session,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      fetch: (_path, init = {}) => {
        if (init.method === 'PUT') saves.push(String(init.body));
        return Promise.resolve(jsonResponse(document(1, { counter: 1 })));
      },
    });

    client.update({ counter: 1 });
    expect(timers.size).toBe(1);

    client.dispose();
    expect(timers.size).toBe(0);

    timers.run();
    await client.flush();
    await flushMicrotasks();
    expect(saves).toHaveLength(0);
  });

  it('does not depend on localStorage for functional state', () => {
    const client = createWorkspaceClient<State>({
      session,
      fetch: () => Promise.resolve(jsonResponse(document(1, { counter: 1 }))),
    });
    client.update({ counter: 1 });
    expect(client.snapshot().state).toEqual({ counter: 1 });
    expect(globalThis.localStorage).toBeUndefined();
  });
});
