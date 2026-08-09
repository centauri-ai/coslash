// Run mirroring for the DaGama board.
//
// The collector owns the run; this store only mirrors it. That is why there is
// no optimistic run state anywhere below: a run is a durable, server-side fact,
// and showing a locally-invented one would be showing something that may not
// exist on disk. Every state change here comes from a server response.

import { listDaGamaRuns, readDaGamaRun, startDaGamaRun, type DaGamaFetch } from '@/plugins/canvas/dagama/api';
import { readLastRunId, writeLastRunId, type DaGamaStorageLike } from '@/plugins/canvas/dagama/preferences';
import { isLiveRun } from '@/plugins/canvas/dagama/runs';
import type { DaGamaRun, DaGamaRunSourceInput, DaGamaRunSummary } from '@/plugins/canvas/dagama/types';

/** A live run advances on the server, so the board polls while one can change. */
export const DAGAMA_POLL_INTERVAL_MS = 2_000;

export type DaGamaRunSessionSnapshot = {
  runs: readonly DaGamaRunSummary[];
  activeRun: DaGamaRun | null;
  starting: boolean;
  error: string;
};

export type DaGamaRunSessionOptions = {
  fetch?: DaGamaFetch;
  storage?: DaGamaStorageLike | null;
  pollIntervalMs?: number;
  setInterval?: (handler: () => void, delayMs: number) => unknown;
  clearInterval?: (handle: unknown) => void;
};

export type DaGamaRunSession = {
  snapshot(): DaGamaRunSessionSnapshot;
  subscribe(listener: () => void): () => void;
  /** Points the session at a project, reloading its runs. Null clears everything. */
  setProject(projectId: string | null): Promise<void>;
  selectRun(runId: string | null): Promise<void>;
  start(boardId: string, source: DaGamaRunSourceInput): Promise<DaGamaRun>;
  /** Adopts a run returned by a control call, without waiting for the next poll. */
  applyRun(run: DaGamaRun): void;
  refresh(): Promise<void>;
  dispose(): void;
};

export function createDaGamaRunSession(options: DaGamaRunSessionOptions = {}): DaGamaRunSession {
  const {
    fetch: fetchImpl,
    storage,
    pollIntervalMs = DAGAMA_POLL_INTERVAL_MS,
    setInterval: setPoll = (handler, delayMs) => setInterval(handler, delayMs),
    clearInterval: clearPoll = (handle) => clearInterval(handle as ReturnType<typeof setInterval>),
  } = options;

  let projectId: string | null = null;
  let runs: DaGamaRunSummary[] = [];
  let activeRun: DaGamaRun | null = null;
  let starting = false;
  let error = '';
  let poll: unknown = null;
  let polledRunId: string | null = null;
  let disposed = false;

  const listeners = new Set<() => void>();
  let snapshot: DaGamaRunSessionSnapshot = { runs, activeRun, starting, error };

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
          const run = await readDaGamaRun(owner, target, fetchImpl);
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
      const listed = await listDaGamaRuns(owner, fetchImpl);
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
    writeLastRunId(owner, runId, storage);
    if (runId === null) {
      activeRun = null;
      publish();
      syncPolling();
      return;
    }
    try {
      const run = await readDaGamaRun(owner, runId, fetchImpl);
      if (disposed || projectId !== owner) return;
      activeRun = run;
      error = '';
      publish();
      syncPolling();
    } catch (caught) {
      if (disposed || projectId !== owner) return;
      // A run the browser remembers may have been deleted from disk. Forget it
      // rather than showing an error the operator cannot act on.
      writeLastRunId(owner, null, storage);
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
      const remembered = readLastRunId(next, storage);
      if (remembered !== '' && projectId === next) await selectRun(remembered);
    },

    selectRun,

    async start(boardId, source) {
      const owner = projectId;
      if (owner === null) throw new Error('Choose a project before starting a run.');
      starting = true;
      error = '';
      publish();
      try {
        const run = await startDaGamaRun({ projectId: owner, boardId, source }, fetchImpl);
        if (!disposed && projectId === owner) {
          activeRun = run;
          writeLastRunId(owner, run.runId, storage);
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
      writeLastRunId(run.projectId, run.runId, storage);
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
