import { apiFetch } from '@/pages/coslash/lib/api';
import type {
  CanvasApiFailure,
  CanvasSessionIdentity,
  CanvasWorkspaceDocument,
  CanvasWorkspaceWrite,
} from '@/plugins/canvas/contracts';

/** Envelope version understood by this build. It must match the Go store. */
export const CANVAS_WORKSPACE_SCHEMA_VERSION = 1;

const DEFAULT_DEBOUNCE_MS = 400;

/** Stable server codes this client reacts to by name. */
export const WORKSPACE_CONFLICT_CODE = 'REVISION_CONFLICT';

type FetchLike = (path: string, init?: RequestInit) => Promise<Response>;

export class CanvasPersistenceError extends Error {
  readonly code: string;
  readonly status: number;
  readonly field?: string;
  readonly actualRevision?: number;

  constructor(message: string, status: number, failure?: CanvasApiFailure | null) {
    super(message);
    this.name = 'CanvasPersistenceError';
    this.status = status;
    this.code = failure?.code ?? 'PERSISTENCE_FAILED';
    this.field = failure?.field;
    this.actualRevision = failure?.actualRevision;
  }

  get isConflict(): boolean {
    return this.code === WORKSPACE_CONFLICT_CODE;
  }
}

/** Builds the frozen workspace route for a composite session identity. */
export function workspacePath(session: CanvasSessionIdentity): string {
  return `/api/canvas/workspaces/${encodeURIComponent(session.agent)}/${encodeURIComponent(session.id)}`;
}

async function readDocument<State>(response: Response): Promise<CanvasWorkspaceDocument<State>> {
  if (response.ok) return (await response.json()) as CanvasWorkspaceDocument<State>;
  let failure: CanvasApiFailure | null = null;
  try {
    failure = (await response.json()) as CanvasApiFailure;
  } catch {
    failure = null;
  }
  throw new CanvasPersistenceError(
    failure?.error ?? 'The workspace request failed.',
    response.status,
    failure,
  );
}

export async function loadWorkspace<State>(
  session: CanvasSessionIdentity,
  fetchImpl: FetchLike = apiFetch,
): Promise<CanvasWorkspaceDocument<State>> {
  return readDocument<State>(await fetchImpl(workspacePath(session)));
}

export async function saveWorkspace<State>(
  session: CanvasSessionIdentity,
  write: CanvasWorkspaceWrite<State>,
  fetchImpl: FetchLike = apiFetch,
): Promise<CanvasWorkspaceDocument<State>> {
  const response = await fetchImpl(workspacePath(session), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(write),
  });
  return readDocument<State>(response);
}

export type WorkspaceStatus = 'idle' | 'loading' | 'saving' | 'saved' | 'error' | 'conflict';

export type WorkspaceSnapshot<State> = {
  /** Local state, which is always the value the UI should render. */
  readonly state: State | null;
  /** Last server-confirmed revision; the base for the next write. */
  readonly revision: number;
  readonly status: WorkspaceStatus;
  /** True while local edits have not been confirmed by the server. */
  readonly dirty: boolean;
  readonly loaded: boolean;
  readonly error: CanvasPersistenceError | null;
};

export type WorkspaceClientOptions = {
  session: CanvasSessionIdentity;
  fetch?: FetchLike;
  debounceMs?: number;
  schemaVersion?: number;
  setTimer?: (handler: () => void, delayMs: number) => unknown;
  clearTimer?: (handle: unknown) => void;
};

export type WorkspaceClient<State> = {
  snapshot(): WorkspaceSnapshot<State>;
  subscribe(listener: () => void): () => void;
  /** Fetches the stored workspace without discarding newer local edits. */
  load(): Promise<void>;
  /** Records a local edit and schedules a debounced save. */
  update(state: State): void;
  /** Saves any pending edit immediately. */
  flush(): Promise<void>;
  /** Re-sends local state on top of the server revision reported by a conflict. */
  resolveWithLocal(): Promise<void>;
  /** Discards local edits and adopts the stored workspace. */
  reloadFromServer(): Promise<void>;
  /** Cancels pending timers and stops further writes. */
  dispose(): void;
};

/**
 * Creates a debounced, revisioned workspace client.
 *
 * Every local edit increments a generation counter. A response is only allowed
 * to mark state clean if it belongs to the generation that was in flight, so a
 * slow save that lands after a newer edit can never present stale state as
 * saved. Failures keep local state and leave the client dirty, so the UI stays
 * usable and visibly unsaved rather than silently losing work.
 */
export function createWorkspaceClient<State>(options: WorkspaceClientOptions): WorkspaceClient<State> {
  const {
    session,
    fetch: fetchImpl = apiFetch,
    debounceMs = DEFAULT_DEBOUNCE_MS,
    schemaVersion = CANVAS_WORKSPACE_SCHEMA_VERSION,
    setTimer = (handler, delayMs) => setTimeout(handler, delayMs),
    clearTimer = (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
  } = options;

  let state: State | null = null;
  let revision = 0;
  let status: WorkspaceStatus = 'idle';
  let error: CanvasPersistenceError | null = null;
  let loaded = false;

  let generation = 0;
  let savedGeneration = 0;
  let timer: unknown = null;
  let running: Promise<void> | null = null;
  let disposed = false;

  const listeners = new Set<() => void>();
  let snapshot: WorkspaceSnapshot<State> = {
    state: null,
    revision: 0,
    status: 'idle',
    dirty: false,
    loaded: false,
    error: null,
  };

  function publish(): void {
    snapshot = {
      state,
      revision,
      status,
      dirty: generation !== savedGeneration,
      loaded,
      error,
    };
    for (const listener of listeners) listener();
  }

  function cancelTimer(): void {
    if (timer == null) return;
    clearTimer(timer);
    timer = null;
  }

  function schedule(): void {
    if (disposed) return;
    cancelTimer();
    timer = setTimer(() => {
      timer = null;
      void flush();
    }, debounceMs);
  }

  async function drain(): Promise<void> {
    while (!disposed && generation !== savedGeneration) {
      const generationInFlight = generation;
      const write: CanvasWorkspaceWrite<State> = {
        schemaVersion,
        expectedRevision: revision,
        state: state as State,
      };
      status = 'saving';
      publish();

      try {
        const document = await saveWorkspace<State>(session, write, fetchImpl);
        if (disposed) return;
        revision = document.revision;
        // Only the generation that was actually sent becomes clean. A newer
        // edit made while this request was in flight stays dirty and loops.
        savedGeneration = generationInFlight;
        error = null;
        status = generation === savedGeneration ? 'saved' : 'saving';
        publish();
      } catch (caught) {
        if (disposed) return;
        error =
          caught instanceof CanvasPersistenceError
            ? caught
            : new CanvasPersistenceError('The workspace could not be saved.', 0, null);
        status = error.isConflict ? 'conflict' : 'error';
        // State and dirty are preserved so the UI can show unsaved work.
        publish();
        return;
      }
    }
  }

  function flush(): Promise<void> {
    cancelTimer();
    if (disposed || generation === savedGeneration) return Promise.resolve();
    if (running != null) return running;
    running = drain().finally(() => {
      running = null;
    });
    return running;
  }

  async function fetchDocument(discardLocalEdits: boolean): Promise<void> {
    if (disposed) return;
    const generationAtStart = generation;
    status = 'loading';
    publish();
    try {
      const document = await loadWorkspace<State>(session, fetchImpl);
      if (disposed) return;
      revision = document.revision;
      loaded = true;
      error = null;
      if (discardLocalEdits || generation === generationAtStart) {
        // Nothing newer exists locally, so the stored document is authoritative.
        state = document.state ?? null;
        savedGeneration = generation;
        status = 'idle';
      } else {
        // The user edited while the load was in flight. Keep their work and
        // rebase the next write onto the revision we just observed.
        status = 'idle';
      }
      publish();
    } catch (caught) {
      if (disposed) return;
      error =
        caught instanceof CanvasPersistenceError
          ? caught
          : new CanvasPersistenceError('The workspace could not be loaded.', 0, null);
      status = 'error';
      publish();
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
    load: () => fetchDocument(false),
    update(next) {
      if (disposed) return;
      state = next;
      generation += 1;
      if (status === 'saved') status = 'idle';
      publish();
      schedule();
    },
    flush,
    async resolveWithLocal() {
      if (disposed || error == null || !error.isConflict) return;
      if (error.actualRevision == null) {
        await fetchDocument(false);
        if (error != null) return;
      } else {
        revision = error.actualRevision;
      }
      error = null;
      status = 'idle';
      publish();
      await flush();
    },
    reloadFromServer: () => fetchDocument(true),
    dispose() {
      disposed = true;
      cancelTimer();
      listeners.clear();
    },
  };
}
