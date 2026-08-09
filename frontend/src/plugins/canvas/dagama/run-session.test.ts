import { describe, expect, it, vi } from 'vitest';
import { FROZEN_DAGAMA_PUBLISH_GATE_RUN, FROZEN_DAGAMA_RUNNING_RUN } from '@/plugins/canvas/dagama/fixtures';
import type { DaGamaStorageLike } from '@/plugins/canvas/dagama/preferences';
import { createDaGamaRunSession } from '@/plugins/canvas/dagama/run-session';
import type { DaGamaRun } from '@/plugins/canvas/dagama/types';

function memoryStorage(seed: Record<string, string> = {}) {
  const map = new Map(Object.entries(seed));
  const storage: DaGamaStorageLike = {
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  };
  return { storage, map };
}

/** A poll timer the test drives, so no wall clock is involved. */
function manualPoll() {
  let handler: (() => void) | null = null;
  return {
    setInterval: (next: () => void) => {
      handler = next;
      return 1;
    },
    clearInterval: () => {
      handler = null;
    },
    tick: () => handler?.(),
    get running() {
      return handler !== null;
    },
  };
}

function fakeService(runs: Record<string, DaGamaRun>, summaries = Object.values(runs)) {
  const json = (value: unknown, status = 200) =>
    new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  const fetchImpl = vi.fn(async (path: string) => {
    if (path.startsWith('/api/dagama/runs?')) {
      return json({
        ok: true,
        runs: summaries.map((run) => ({
          runId: run.runId,
          projectId: run.projectId,
          boardId: run.boardId,
          title: run.title,
          status: run.status,
          createdAt: run.createdAt,
          updatedAt: run.updatedAt,
          finishedAt: run.finishedAt,
        })),
        errors: [],
      });
    }
    const match = /^\/api\/dagama\/runs\/([^/?]+)\?/.exec(path);
    if (match) {
      const run = runs[decodeURIComponent(match[1])];
      if (run === undefined) return json({ ok: false, code: 'NOT_FOUND', error: 'gone' }, 404);
      return json({ ok: true, run });
    }
    return json({ ok: false, code: 'NOT_FOUND', error: 'unknown route' }, 404);
  });
  return { fetchImpl };
}

describe('DaGama run session', () => {
  it('lists runs and reopens the remembered one', async () => {
    const service = fakeService({ [FROZEN_DAGAMA_RUNNING_RUN.runId]: FROZEN_DAGAMA_RUNNING_RUN });
    const { storage } = memoryStorage({
      'coslash.canvas.dagama.runId.v1.demo-project': FROZEN_DAGAMA_RUNNING_RUN.runId,
    });
    const session = createDaGamaRunSession({ fetch: service.fetchImpl, storage });

    await session.setProject('demo-project');

    expect(session.snapshot().runs).toHaveLength(1);
    expect(session.snapshot().activeRun?.runId).toBe(FROZEN_DAGAMA_RUNNING_RUN.runId);
  });

  it('forgets a remembered run that no longer exists instead of stranding an error', async () => {
    const service = fakeService({});
    const { storage, map } = memoryStorage({
      'coslash.canvas.dagama.runId.v1.demo-project': 'run-20260101t000000-deadbeef',
    });
    const session = createDaGamaRunSession({ fetch: service.fetchImpl, storage });

    await session.setProject('demo-project');

    expect(session.snapshot().activeRun).toBeNull();
    expect(map.get('coslash.canvas.dagama.runId.v1.demo-project')).toBeUndefined();
  });

  it('polls only while the watched run can still change', async () => {
    const poll = manualPoll();
    const service = fakeService({ [FROZEN_DAGAMA_RUNNING_RUN.runId]: FROZEN_DAGAMA_RUNNING_RUN });
    const { storage } = memoryStorage();
    const session = createDaGamaRunSession({
      fetch: service.fetchImpl,
      storage,
      setInterval: poll.setInterval,
      clearInterval: poll.clearInterval,
    });

    await session.setProject('demo-project');
    expect(poll.running).toBe(false);

    await session.selectRun(FROZEN_DAGAMA_RUNNING_RUN.runId);
    expect(poll.running).toBe(true);

    session.applyRun({ ...FROZEN_DAGAMA_RUNNING_RUN, status: 'succeeded' });
    expect(poll.running).toBe(false);
  });

  it('keeps polling an awaiting_approval run so a gate decided elsewhere still lands', async () => {
    const poll = manualPoll();
    const service = fakeService({
      [FROZEN_DAGAMA_PUBLISH_GATE_RUN.runId]: FROZEN_DAGAMA_PUBLISH_GATE_RUN,
    });
    const { storage } = memoryStorage();
    const session = createDaGamaRunSession({
      fetch: service.fetchImpl,
      storage,
      setInterval: poll.setInterval,
      clearInterval: poll.clearInterval,
    });

    await session.setProject('demo-project');
    await session.selectRun(FROZEN_DAGAMA_PUBLISH_GATE_RUN.runId);

    expect(poll.running).toBe(true);
  });

  it('ignores a poll result for a run that is no longer being watched', async () => {
    const poll = manualPoll();
    const service = fakeService({
      [FROZEN_DAGAMA_RUNNING_RUN.runId]: FROZEN_DAGAMA_RUNNING_RUN,
    });
    const { storage } = memoryStorage();
    const session = createDaGamaRunSession({
      fetch: service.fetchImpl,
      storage,
      setInterval: poll.setInterval,
      clearInterval: poll.clearInterval,
    });
    await session.setProject('demo-project');
    await session.selectRun(FROZEN_DAGAMA_RUNNING_RUN.runId);

    poll.tick();
    await session.selectRun(null);
    await Promise.resolve();
    await Promise.resolve();

    expect(session.snapshot().activeRun).toBeNull();
  });

  it('clears everything when the project goes away', async () => {
    const service = fakeService({ [FROZEN_DAGAMA_RUNNING_RUN.runId]: FROZEN_DAGAMA_RUNNING_RUN });
    const { storage } = memoryStorage({
      'coslash.canvas.dagama.runId.v1.demo-project': FROZEN_DAGAMA_RUNNING_RUN.runId,
    });
    const session = createDaGamaRunSession({ fetch: service.fetchImpl, storage });
    await session.setProject('demo-project');

    await session.setProject(null);

    expect(session.snapshot().runs).toHaveLength(0);
    expect(session.snapshot().activeRun).toBeNull();
  });

  it('refuses to start a run before a project is chosen', async () => {
    const service = fakeService({});
    const { storage } = memoryStorage();
    const session = createDaGamaRunSession({ fetch: service.fetchImpl, storage });
    await expect(session.start('board-1', { kind: 'text', title: 't', text: 'x' })).rejects.toThrow(
      /project/i,
    );
  });

  it('ignores a control response that belongs to another project', async () => {
    const service = fakeService({ [FROZEN_DAGAMA_RUNNING_RUN.runId]: FROZEN_DAGAMA_RUNNING_RUN });
    const { storage } = memoryStorage();
    const session = createDaGamaRunSession({ fetch: service.fetchImpl, storage });
    await session.setProject('demo-project');

    session.applyRun({ ...FROZEN_DAGAMA_RUNNING_RUN, projectId: 'other-project' });

    expect(session.snapshot().activeRun).toBeNull();
  });
});
