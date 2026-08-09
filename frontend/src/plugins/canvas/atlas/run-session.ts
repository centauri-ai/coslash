// Run mirroring for the Atlas board.
//
// The collector owns the run; this store only mirrors it. That is why there is
// no optimistic run state anywhere below: a run is a durable, server-side fact,
// and showing a locally-invented one would be showing something that may not
// exist on disk. Every state change here comes from a server response.

import { listAtlasRuns, readAtlasRun, startAtlasRun, type AtlasFetch } from '@/plugins/canvas/atlas/api';
import { isLiveRun } from '@/plugins/canvas/atlas/runs';
import type { AtlasRun, AtlasRunSummary, AtlasSourceInput } from '@/plugins/canvas/atlas/types';

/** A live run advances on the server, so the board polls while one can change. */
export const ATLAS_POLL_INTERVAL_MS = 2_000;

export type AtlasRunSessionSnapshot = {
  runs: readonly AtlasRunSummary[];
  activeRun: AtlasRun | null;
  starting: boolean;
  error: string;
};

export type AtlasRunSessionOptions = {
  fetch?: AtlasFetch;
  pollIntervalMs?: number;
  setInterval?: (handler: () => void, delayMs: number) => unknown;
  clearInterval?: (handle: unknown) => void;
};

export type AtlasRunSession = {
  snapshot(): AtlasRunSessionSnapshot;
  subscribe(listener: () => void): () => void;
  /** Points the session at a project, reloading its runs. Null clears everything. */
  setProject(projectId: string | null): Promise<void>;
  selectRun(runId: string | null): Promise<void>;
  start(boardId: string, source: AtlasSourceInput): Promise<AtlasRun>;
  /** Adopts a run returned by a control call, without waiting for the next poll. */
  applyRun(run: AtlasRun): void;
  refresh(): Promise<void>;
  dispose(): void;
};

export function createAtlasRunSession(options: AtlasRunSessionOptions = {}): AtlasRunSession {
  const {
    fetch: fetchImpl,
    pollIntervalMs = ATLAS_POLL_INTERVAL_MS,
    setInterval: setPoll = (handler, delayMs) => setInterval(handler, delayMs),
    clearInterval: clearPoll = (handle) => clearInterval(handle as ReturnType<typeof setInterval>),
  } = options;

  let projectId: string | null = null;
  let runs: AtlasRunSummary[] = [];
  let activeRun: AtlasRun | null = null;
  let starting = false;
  let error = '';
  let poll: unknown = null;
  let polledRunId: string | null = null;
  let disposed = false;

  const listeners = new Set<() => void>();
  let snapshot: AtlasRunSessionSnapshot = { runs, activeRun, starting, error };

  function publish(): void {
    snapshot = { runs, activeRun, starting, error };
    for (const listener of listeners) listener();
  }

  function stopPolling(): void {
    if (poll != null) clearPoll(poll);
    poll = null;
    polledRunId = null;
  }

  /**
   * Poll only while the active run can still change, and key the interval on the
   * run id rather than the run object: every poll produces a new object, so
   * keying on it would tear the timer down and rebuild it on every tick.
   */
  function syncPolling(): void {
    const target = isLiveRun(activeRun) ? activeRun!.runId : null;
    if (target === polledRunId) return;
    stopPolling();
    if (target === null || projectId === null || disposed) return;
    polledRunId = target;
    const owner = projectId;
    poll = setPoll(() => {
      void (async () => {
        try {
          const run = await readAtlasRun(owner, target, fetchImpl);
          // Accept the poll only if it is still the run being shown.
          if (disposed || projectId !== owner || activeRun?.runId !== target) return;
          activeRun = run;
          publish();
          syncPolling();
        } catch {
          // A transient failure must not tear down the view; the next tick retries.
        }
      })();
    }, pollIntervalMs);
  }

  async function refresh(): Promise<void> {
    const owner = projectId;
    if (owner === null) return;
    try {
      const listed = await listAtlasRuns(owner, fetchImpl);
      if (disposed || projectId !== owner) return;
      runs = listed.runs;
      publish();
    } catch (caught) {
      if (disposed || projectId !== owner) return;
      error = caught instanceof Error ? caught.message : 'The runs could not be listed.';
      publish();
    }
  }

  async function selectRun(runId: string | null): Promise<void> {
    const owner = projectId;
    if (owner === null) return;
    if (runId === null) {
      activeRun = null;
      publish();
      syncPolling();
      return;
    }
    try {
      const run = await readAtlasRun(owner, runId, fetchImpl);
      if (disposed || projectId !== owner) return;
      activeRun = run;
      error = '';
      publish();
      syncPolling();
    } catch (caught) {
      if (disposed || projectId !== owner) return;
      // A run the browser remembers may have been deleted from disk. Forget it
      // rather than showing an error the operator cannot act on.
      activeRun = null;
      error = caught instanceof Error ? caught.message : 'This run could not be opened.';
      publish();
      syncPolling();
    }
  }

  return {
    snapshot: () => snapshot,

    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },

    async setProject(next) {
      if (next === projectId) return;
      projectId = next;
      runs = [];
      activeRun = null;
      error = '';
      stopPolling();
      publish();
      if (next === null) return;
      await refresh();
    },

    selectRun,

    async start(boardId, source) {
      const owner = projectId;
      if (owner === null) throw new Error('Choose a project before starting a run.');
      starting = true;
      error = '';
      publish();
      try {
        const run = await startAtlasRun({ projectId: owner, boardId, source }, fetchImpl);
        if (!disposed && projectId === owner) {
          activeRun = run;
          publish();
          syncPolling();
          await refresh();
        }
        return run;
      } finally {
        starting = false;
        publish();
      }
    },

    applyRun(run) {
      if (disposed || projectId !== run.projectId) return;
      activeRun = run;
      publish();
      syncPolling();
    },

    refresh,

    dispose() {
      disposed = true;
      stopPolling();
      listeners.clear();
    },
  };
}
