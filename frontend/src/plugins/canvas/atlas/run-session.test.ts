import { describe, expect, it, vi } from 'vitest';
import { FROZEN_ATLAS_COMMITTEE_RUN, FROZEN_ATLAS_PROJECT } from '@/plugins/canvas/atlas/fixtures';
import { createAtlasRunSession } from '@/plugins/canvas/atlas/run-session';
import type { AtlasRun } from '@/plugins/canvas/atlas/types';

/** A poll timer the test drives, so a tick happens exactly when it decides. */
function manualPoll() {
  let pending: (() => void) | null = null;
  return {
    setInterval: (handler: () => void) => {
      pending = handler;
      return 1;
    },
    clearInterval: () => {
      pending = null;
    },
    running: () => pending !== null,
    tick: () => pending?.(),
  };
}

const PROJECT = FROZEN_ATLAS_PROJECT.id;

function summary(run: AtlasRun) {
  return {
    runId: run.runId,
    projectId: run.projectId,
    boardId: run.boardId,
    title: run.title,
    status: run.status,
    createdAt: run.createdAt,
    updatedAt: run.updatedAt,
    finishedAt: run.finishedAt,
  };
}

/** A fake run service that serves whatever the test last stored. */
function fakeServer(initial: AtlasRun = FROZEN_ATLAS_COMMITTEE_RUN) {
  const state = { run: initial, listFails: false, readFails: false };
  const json = (value: unknown, status = 200) =>
    new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

  const fetchImpl = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.startsWith('/api/atlas/runs?') && (init?.method ?? 'GET') === 'GET') {
      if (state.listFails) return json({ ok: false, code: 'IO', error: 'disk is unreadable' }, 500);
      return json({ ok: true, runs: [summary(state.run)], errors: [] });
    }
    if (path.startsWith('/api/atlas/runs?') && init?.method === 'POST') {
      return json({ ok: true, run: state.run });
    }
    if (path.startsWith(`/api/atlas/runs/${state.run.runId}?`)) {
      if (state.readFails) return json({ ok: false, code: 'NOT_FOUND', error: 'no such run' }, 404);
      return json({ ok: true, run: state.run });
    }
    return json({ ok: false, code: 'NOT_FOUND', error: 'unknown route' }, 404);
  });

  return { fetchImpl, state };
}

describe('Atlas run session', () => {
  it('lists a project’s runs when it is pointed at one', async () => {
    const server = fakeServer();
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await session.setProject(PROJECT);
    expect(session.snapshot().runs).toHaveLength(1);
    expect(session.snapshot().activeRun).toBeNull();
  });

  it('never invents a run the server has not confirmed', async () => {
    const server = fakeServer();
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await session.setProject(PROJECT);
    await session.selectRun(FROZEN_ATLAS_COMMITTEE_RUN.runId);
    // Every field comes from the response; nothing is filled in locally.
    expect(session.snapshot().activeRun).toEqual(FROZEN_ATLAS_COMMITTEE_RUN);
  });

  it('polls while the run can still change and stops once it cannot', async () => {
    const server = fakeServer();
    const poll = manualPoll();
    const session = createAtlasRunSession({
      fetch: server.fetchImpl,
      setInterval: poll.setInterval,
      clearInterval: poll.clearInterval,
    });
    await session.setProject(PROJECT);
    await session.selectRun(FROZEN_ATLAS_COMMITTEE_RUN.runId);
    expect(poll.running()).toBe(true);

    server.state.run = { ...FROZEN_ATLAS_COMMITTEE_RUN, status: 'succeeded' };
    poll.tick();
    await vi.waitFor(() => expect(session.snapshot().activeRun?.status).toBe('succeeded'));
    expect(poll.running()).toBe(false);
  });

  it('keeps a poll failure from tearing down the view', async () => {
    const server = fakeServer();
    const poll = manualPoll();
    const session = createAtlasRunSession({
      fetch: server.fetchImpl,
      setInterval: poll.setInterval,
      clearInterval: poll.clearInterval,
    });
    await session.setProject(PROJECT);
    await session.selectRun(FROZEN_ATLAS_COMMITTEE_RUN.runId);

    server.state.readFails = true;
    poll.tick();
    await vi.waitFor(() => expect(server.fetchImpl.mock.calls.length).toBeGreaterThan(2));
    // The run the operator was watching is still on screen, and polling continues.
    expect(session.snapshot().activeRun?.runId).toBe(FROZEN_ATLAS_COMMITTEE_RUN.runId);
    expect(poll.running()).toBe(true);
  });

  it('forgets a run that no longer exists instead of showing a dead card', async () => {
    const server = fakeServer();
    server.state.readFails = true;
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await session.setProject(PROJECT);
    await session.selectRun(FROZEN_ATLAS_COMMITTEE_RUN.runId);
    expect(session.snapshot().activeRun).toBeNull();
    expect(session.snapshot().error).toContain('no such run');
  });

  it('adopts a run returned by a control call without waiting for the next poll', async () => {
    const server = fakeServer();
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await session.setProject(PROJECT);
    const canceled: AtlasRun = { ...FROZEN_ATLAS_COMMITTEE_RUN, status: 'canceled' };
    session.applyRun(canceled);
    expect(session.snapshot().activeRun?.status).toBe('canceled');
  });

  it('ignores a run belonging to a different project', async () => {
    const server = fakeServer();
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await session.setProject(PROJECT);
    session.applyRun({ ...FROZEN_ATLAS_COMMITTEE_RUN, projectId: 'someone-else' });
    expect(session.snapshot().activeRun).toBeNull();
  });

  it('clears everything when the project changes', async () => {
    const server = fakeServer();
    const poll = manualPoll();
    const session = createAtlasRunSession({
      fetch: server.fetchImpl,
      setInterval: poll.setInterval,
      clearInterval: poll.clearInterval,
    });
    await session.setProject(PROJECT);
    await session.selectRun(FROZEN_ATLAS_COMMITTEE_RUN.runId);
    await session.setProject(null);
    expect(session.snapshot().activeRun).toBeNull();
    expect(session.snapshot().runs).toHaveLength(0);
    expect(poll.running()).toBe(false);
  });

  it('reports a failed listing rather than showing an empty project', async () => {
    const server = fakeServer();
    server.state.listFails = true;
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await session.setProject(PROJECT);
    expect(session.snapshot().error).toContain('disk is unreadable');
  });

  it('refuses to start without a project', async () => {
    const server = fakeServer();
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await expect(session.start('board-1', { kind: 'text', title: 't', text: 'x' })).rejects.toThrow(
      'Choose a project',
    );
  });

  it('reports starting while the request is in flight and clears it after', async () => {
    const server = fakeServer();
    const session = createAtlasRunSession({ fetch: server.fetchImpl });
    await session.setProject(PROJECT);
    const pending = session.start('board-1', { kind: 'text', title: 't', text: 'x' });
    expect(session.snapshot().starting).toBe(true);
    await pending;
    expect(session.snapshot().starting).toBe(false);
    expect(session.snapshot().activeRun?.runId).toBe(FROZEN_ATLAS_COMMITTEE_RUN.runId);
  });

  it('stops publishing after dispose', async () => {
    const server = fakeServer();
    const poll = manualPoll();
    const session = createAtlasRunSession({
      fetch: server.fetchImpl,
      setInterval: poll.setInterval,
      clearInterval: poll.clearInterval,
    });
    await session.setProject(PROJECT);
    const listener = vi.fn();
    session.subscribe(listener);
    session.dispose();
    session.applyRun(FROZEN_ATLAS_COMMITTEE_RUN);
    expect(listener).not.toHaveBeenCalled();
    expect(poll.running()).toBe(false);
  });
});
