// The Atlas board editing session: project selection, board open/save/delete,
// debounced autosave, and conflict recovery.
//
// A framework-free store rather than a hook, so the parts that are easy to get
// subtly wrong — coalescing rapid edits into one write without dropping the
// last, and refusing to overwrite a newer revision — are testable directly.
//
// Atlas adds one rule DaGama has no need for: a board whose schema this build
// does not understand is opened read-only. Saving it would rewrite a document
// whose meaning we cannot see, so the session refuses the write rather than
// silently discarding whatever a newer build wrote.

import {
  AtlasApiError,
  boardSummaryOf,
  deleteAtlasBoard,
  listAtlasBoards,
  openAtlasProject,
  readAtlasBoard,
  readAtlasBoardDetailed,
  writeAtlasBoard,
  type AtlasBoardDocument,
  type AtlasBoardSummary,
  type AtlasFetch,
} from '@/plugins/canvas/atlas/api';
import { atlasBoardSignature, defaultAtlasBoard, type AtlasBoard } from '@/plugins/canvas/atlas/graph';
import type { AtlasBoardLoadError, AtlasProject } from '@/plugins/canvas/atlas/types';

export type AtlasSaveState = 'draft' | 'loading' | 'saving' | 'saved' | 'error' | 'conflict';

export type AtlasBoardSessionSnapshot = {
  project: AtlasProject | null;
  boards: readonly AtlasBoardSummary[];
  boardErrors: readonly AtlasBoardLoadError[];
  activeBoard: AtlasBoardSummary | null;
  board: AtlasBoard;
  saveState: AtlasSaveState;
  error: string;
  serverRevision: number | null;
  /** True when the open document declares a schema this build cannot write. */
  readOnly: boolean;
  /** True when the open document was migrated from the record-shaped schema. */
  migrated: boolean;
};

export type AtlasBoardSessionOptions = {
  fetch?: AtlasFetch;
  debounceMs?: number;
  setTimer?: (handler: () => void, delayMs: number) => unknown;
  clearTimer?: (handle: unknown) => void;
  newBoardId?: () => string;
};

const DEFAULT_DEBOUNCE_MS = 350;

function messageOf(caught: unknown, fallback: string): string {
  return caught instanceof Error && caught.message !== '' ? caught.message : fallback;
}

export type AtlasBoardSession = {
  snapshot(): AtlasBoardSessionSnapshot;
  subscribe(listener: () => void): () => void;
  edit(next: AtlasBoard): void;
  chooseProject(projectPath: string): Promise<void>;
  openBoard(id: string): Promise<void>;
  saveAs(name: string): Promise<AtlasBoardSummary>;
  deleteBoard(summary: AtlasBoardSummary): Promise<void>;
  newBoard(): void;
  flush(): Promise<void>;
  keepLocal(): Promise<void>;
  reloadFromServer(): Promise<void>;
  dispose(): void;
};

export function createAtlasBoardSession(options: AtlasBoardSessionOptions = {}): AtlasBoardSession {
  const {
    fetch: fetchImpl,
    debounceMs = DEFAULT_DEBOUNCE_MS,
    setTimer = (handler, delayMs) => setTimeout(handler, delayMs),
    clearTimer = (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
    newBoardId = () => crypto.randomUUID(),
  } = options;

  let project: AtlasProject | null = null;
  let boards: AtlasBoardSummary[] = [];
  let boardErrors: AtlasBoardLoadError[] = [];
  let activeBoard: AtlasBoardSummary | null = null;
  let board: AtlasBoard = defaultAtlasBoard();
  let saveState: AtlasSaveState = 'draft';
  let error = '';
  let serverRevision: number | null = null;
  let readOnly = false;
  let migrated = false;

  let savedSignature: string | null = null;
  let requested = 0;
  let completed = 0;
  let running: Promise<void> | null = null;
  let timer: unknown = null;
  let disposed = false;

  const listeners = new Set<() => void>();
  let snapshot: AtlasBoardSessionSnapshot = build();

  function build(): AtlasBoardSessionSnapshot {
    return {
      project,
      boards,
      boardErrors,
      activeBoard,
      board,
      saveState,
      error,
      serverRevision,
      readOnly,
      migrated,
    };
  }

  function publish(): void {
    snapshot = build();
    for (const listener of listeners) listener();
  }

  function cancelTimer(): void {
    if (timer == null) return;
    clearTimer(timer);
    timer = null;
  }

  function rememberSummary(summary: AtlasBoardSummary): void {
    boards = [summary, ...boards.filter((candidate) => candidate.id !== summary.id)].sort((a, b) =>
      b.updatedAt.localeCompare(a.updatedAt),
    );
  }

  /**
   * The collector keeps opened projects in memory only until it restarts.
   * Reopen by the path already held and retry once, rather than stranding
   * autosave until the operator notices and re-picks the project by hand.
   */
  async function withOpenProject<Value>(run: () => Promise<Value>): Promise<Value> {
    try {
      return await run();
    } catch (caught) {
      const opened = project;
      if (opened == null || !(caught instanceof AtlasApiError) || !caught.isProjectNotOpen) throw caught;
      await openAtlasProject(opened.path, fetchImpl);
      return run();
    }
  }

  function adopt(document: AtlasBoardDocument, unsupported: boolean, fromLegacy: boolean): void {
    const summary = boardSummaryOf(document);
    completed = requested;
    board = document.board;
    activeBoard = summary;
    savedSignature = atlasBoardSignature(document.board);
    readOnly = unsupported;
    migrated = fromLegacy;
    saveState = 'saved';
    error = unsupported
      ? 'This workflow was written by a newer coSlash. It is shown read-only so a save cannot discard what that version stored.'
      : '';
    serverRevision = null;
    rememberSummary(summary);
    publish();
  }

  async function drain(): Promise<void> {
    while (!disposed && completed < requested) {
      const generation = requested;
      const opened = project;
      const document = activeBoard;
      if (opened == null || document == null || readOnly) {
        completed = requested;
        break;
      }
      const pending = board;
      const signature = atlasBoardSignature(pending);
      try {
        const saved = await withOpenProject(() =>
          writeAtlasBoard(
            {
              projectId: opened.id,
              id: document.id,
              name: document.name,
              board: pending,
              expectedRevision: document.revision,
            },
            fetchImpl,
          ),
        );
        if (disposed) return;
        const summary = boardSummaryOf(saved);
        rememberSummary(summary);
        // Only the generation actually sent becomes clean; a newer edit made
        // while this request was in flight stays dirty and loops.
        completed = Math.max(completed, generation);
        if (project?.id !== opened.id || activeBoard?.id !== document.id) break;
        activeBoard = summary;
        savedSignature = signature;
        serverRevision = null;
        if (completed === requested) {
          saveState = 'saved';
          error = '';
        }
        publish();
      } catch (caught) {
        if (disposed) return;
        // Stop the loop: retrying a write the server just refused would spin.
        completed = Math.max(completed, requested);
        const conflict = caught instanceof AtlasApiError && caught.isConflict;
        saveState = conflict ? 'conflict' : 'error';
        serverRevision = conflict ? ((caught as AtlasApiError).actualRevision ?? null) : null;
        error = messageOf(caught, 'The workflow could not be saved.');
        publish();
        return;
      }
    }
  }

  function flush(): Promise<void> {
    cancelTimer();
    if (disposed || completed >= requested) return Promise.resolve();
    if (running != null) return running;
    running = drain().finally(() => {
      running = null;
    });
    return running;
  }

  function schedule(): void {
    if (disposed) return;
    cancelTimer();
    timer = setTimer(() => {
      timer = null;
      void flush();
    }, debounceMs);
  }

  async function loadProject(projectPath: string): Promise<AtlasProject> {
    const opened = await openAtlasProject(projectPath, fetchImpl);
    project = opened;
    const listed = await listAtlasBoards(opened.id, fetchImpl);
    boards = [...listed.boards].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
    boardErrors = listed.errors;
    error =
      listed.errors.length > 0
        ? `${listed.errors.length} workflow file(s) in this project could not be read.`
        : '';
    return opened;
  }

  async function readAndAdopt(projectId: string, boardId: string): Promise<void> {
    // One request carries the board and the two facts normalization erases.
    const read = await withOpenProject(() => readAtlasBoardDetailed(projectId, boardId, fetchImpl));
    adopt(read.document, read.unsupported, read.migrated);
  }

  async function chooseProject(projectPath: string): Promise<void> {
    if (activeBoard != null && atlasBoardSignature(board) !== savedSignature && !readOnly) {
      if (saveState === 'conflict') {
        throw new Error('Resolve the workflow conflict before switching projects.');
      }
      await flush();
      if (atlasBoardSignature(board) !== savedSignature) {
        throw new Error('The open workflow could not be saved, so the project was not switched.');
      }
    }
    saveState = 'loading';
    publish();

    const opened = await loadProject(projectPath);
    const target = boards[0];
    if (target === undefined) {
      completed = requested;
      activeBoard = null;
      savedSignature = null;
      readOnly = false;
      migrated = false;
      saveState = 'draft';
      publish();
      return;
    }
    await readAndAdopt(opened.id, target.id);
  }

  return {
    snapshot: () => snapshot,

    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },

    edit(next) {
      if (disposed) return;
      // A read-only document accepts no edit at all: offering one and then
      // refusing the save would lose the operator's work at the worst moment.
      if (readOnly) return;
      board = next;
      if (activeBoard != null && atlasBoardSignature(next) === savedSignature) {
        completed = requested;
        if (saveState !== 'conflict' && saveState !== 'error') saveState = 'saved';
        publish();
        return;
      }
      if (activeBoard != null && saveState !== 'conflict') {
        requested += 1;
        saveState = 'saving';
      }
      publish();
      schedule();
    },

    chooseProject,

    async openBoard(id) {
      const opened = project;
      if (opened == null) return;
      saveState = 'loading';
      error = '';
      publish();
      try {
        await readAndAdopt(opened.id, id);
      } catch (caught) {
        saveState = 'error';
        error = messageOf(caught, 'The workflow could not be opened.');
        publish();
      }
    },

    async saveAs(name) {
      const opened = project;
      if (opened == null) throw new Error('Choose a project before saving a workflow.');
      saveState = 'saving';
      error = '';
      publish();
      try {
        const saved = await withOpenProject(() =>
          writeAtlasBoard(
            { projectId: opened.id, id: newBoardId(), name, board, expectedRevision: 0 },
            fetchImpl,
          ),
        );
        adopt({ ...saved, board }, false, false);
        return boardSummaryOf(saved);
      } catch (caught) {
        saveState = 'error';
        error = messageOf(caught, 'The workflow could not be saved.');
        publish();
        throw caught;
      }
    },

    async deleteBoard(summary) {
      const opened = project;
      if (opened == null) return;
      try {
        await withOpenProject(() => deleteAtlasBoard(opened.id, summary.id, summary.revision, fetchImpl));
        boards = boards.filter((candidate) => candidate.id !== summary.id);
        if (activeBoard?.id === summary.id) {
          completed = requested;
          activeBoard = null;
          savedSignature = null;
          readOnly = false;
          migrated = false;
          board = defaultAtlasBoard();
          saveState = 'draft';
        }
        publish();
      } catch (caught) {
        saveState = caught instanceof AtlasApiError && caught.isConflict ? 'conflict' : 'error';
        error = messageOf(caught, 'The workflow could not be deleted.');
        publish();
      }
    },

    newBoard() {
      cancelTimer();
      completed = requested;
      activeBoard = null;
      savedSignature = null;
      readOnly = false;
      migrated = false;
      board = defaultAtlasBoard();
      saveState = 'draft';
      error = '';
      serverRevision = null;
      publish();
    },

    flush,

    async keepLocal() {
      if (disposed || saveState !== 'conflict') return;
      const opened = project;
      const document = activeBoard;
      if (opened == null || document == null) return;
      let revision = serverRevision;
      if (revision == null) {
        try {
          revision = (await withOpenProject(() => readAtlasBoard(opened.id, document.id, fetchImpl)))
            .revision;
        } catch (caught) {
          error = messageOf(caught, 'The stored workflow could not be read.');
          publish();
          return;
        }
      }
      activeBoard = { ...document, revision };
      serverRevision = null;
      error = '';
      saveState = 'saving';
      requested += 1;
      publish();
      await flush();
    },

    async reloadFromServer() {
      const opened = project;
      const document = activeBoard;
      if (opened == null || document == null) return;
      saveState = 'loading';
      publish();
      try {
        await readAndAdopt(opened.id, document.id);
      } catch (caught) {
        saveState = 'error';
        error = messageOf(caught, 'The stored workflow could not be read.');
        publish();
      }
    },

    dispose() {
      disposed = true;
      cancelTimer();
      listeners.clear();
    },
  };
}
