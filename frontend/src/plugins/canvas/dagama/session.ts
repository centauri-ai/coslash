// The DaGama board editing session: project selection, board open/save/delete,
// debounced autosave, and conflict recovery.
//
// This is a framework-free store rather than a hook so the parts that are easy
// to get subtly wrong — coalescing rapid edits into one write without ever
// dropping the last one, and refusing to overwrite a newer server revision —
// can be tested directly. `useDaGamaBoardSession` is the thin React binding.
//
// The project file is authoritative throughout. Browser storage holds only a
// recovery copy plus "what was I looking at", and every failure path keeps the
// operator's edits in memory and visibly unsaved rather than discarding them.

import {
  boardSummaryOf,
  DaGamaApiError,
  deleteDaGamaBoard,
  listDaGamaBoards,
  openDaGamaProject,
  readDaGamaBoard,
  writeDaGamaBoard,
  type DaGamaBoardDocument,
  type DaGamaBoardSummary,
  type DaGamaFetch,
} from '@/plugins/canvas/dagama/api';
import { daGamaBoardSignature, defaultDaGamaBoard, type DaGamaBoard } from '@/plugins/canvas/dagama/board';
import {
  readDraft,
  readDraftMetadata,
  readLastBoardId,
  readLastProjectPath,
  writeDraft,
  writeDraftMetadata,
  writeLastBoardId,
  writeLastProjectPath,
  type DaGamaStorageLike,
} from '@/plugins/canvas/dagama/preferences';
import type { DaGamaBoardLoadError, DaGamaProject } from '@/plugins/canvas/dagama/types';

export type DaGamaSaveState = 'draft' | 'loading' | 'saving' | 'saved' | 'error' | 'conflict';

export type DaGamaBoardSessionSnapshot = {
  project: DaGamaProject | null;
  boards: readonly DaGamaBoardSummary[];
  boardErrors: readonly DaGamaBoardLoadError[];
  /** Metadata of the open document. Null while the board is an unsaved draft. */
  activeBoard: DaGamaBoardSummary | null;
  board: DaGamaBoard;
  saveState: DaGamaSaveState;
  error: string;
  /** The revision the server reports, when a conflict told us about one. */
  serverRevision: number | null;
};

export type DaGamaBoardSessionOptions = {
  fetch?: DaGamaFetch;
  storage?: DaGamaStorageLike | null;
  debounceMs?: number;
  setTimer?: (handler: () => void, delayMs: number) => unknown;
  clearTimer?: (handle: unknown) => void;
  /** Injectable so tests get stable board identifiers. */
  newBoardId?: () => string;
};

export type DaGamaBoardSession = {
  snapshot(): DaGamaBoardSessionSnapshot;
  subscribe(listener: () => void): () => void;
  /** Records a local edit and schedules the debounced save. */
  edit(next: DaGamaBoard): void;
  chooseProject(projectPath: string): Promise<void>;
  openBoard(id: string): Promise<void>;
  saveAs(name: string): Promise<DaGamaBoardSummary>;
  deleteBoard(summary: DaGamaBoardSummary): Promise<void>;
  /** Abandons the open document for a fresh unsaved draft. */
  newBoard(): void;
  /** Writes any pending edit immediately. */
  flush(): Promise<void>;
  /** Re-sends local edits on top of the revision the server reported. */
  keepLocal(): Promise<void>;
  /** Discards local edits and adopts the stored board. */
  reloadFromServer(): Promise<void>;
  /** Reopens the last project, if the browser remembers one. */
  restore(): Promise<void>;
  dispose(): void;
};

const DEFAULT_DEBOUNCE_MS = 350;

function messageOf(caught: unknown, fallback: string): string {
  return caught instanceof Error && caught.message !== '' ? caught.message : fallback;
}

export function createDaGamaBoardSession(options: DaGamaBoardSessionOptions = {}): DaGamaBoardSession {
  const {
    fetch: fetchImpl,
    storage,
    debounceMs = DEFAULT_DEBOUNCE_MS,
    setTimer = (handler, delayMs) => setTimeout(handler, delayMs),
    clearTimer = (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
    newBoardId = () => crypto.randomUUID(),
  } = options;

  let project: DaGamaProject | null = null;
  let boards: DaGamaBoardSummary[] = [];
  let boardErrors: DaGamaBoardLoadError[] = [];
  let activeBoard: DaGamaBoardSummary | null = null;
  let board: DaGamaBoard = readDraft(storage) ?? defaultDaGamaBoard();
  let saveState: DaGamaSaveState = 'draft';
  let error = '';
  let serverRevision: number | null = null;

  // The signature of the board the server last confirmed. Comparing signatures
  // rather than object identity is what lets an edit that round-trips back to
  // the stored value settle as `saved` instead of looping.
  let savedSignature: string | null = null;
  let requested = 0;
  let completed = 0;
  let running: Promise<void> | null = null;
  let timer: unknown = null;
  let disposed = false;

  const listeners = new Set<() => void>();
  let snapshot: DaGamaBoardSessionSnapshot = buildSnapshot();

  function buildSnapshot(): DaGamaBoardSessionSnapshot {
    return { project, boards, boardErrors, activeBoard, board, saveState, error, serverRevision };
  }

  function publish(): void {
    snapshot = buildSnapshot();
    for (const listener of listeners) listener();
  }

  function cancelTimer(): void {
    if (timer == null) return;
    clearTimer(timer);
    timer = null;
  }

  function cacheDraft(): void {
    writeDraft(board, storage);
    if (project != null && activeBoard != null) {
      writeDraftMetadata(
        { projectId: project.id, boardId: activeBoard.id, revision: activeBoard.revision },
        storage,
      );
    }
  }

  function rememberSummary(summary: DaGamaBoardSummary): void {
    boards = [summary, ...boards.filter((candidate) => candidate.id !== summary.id)].sort((a, b) =>
      b.updatedAt.localeCompare(a.updatedAt),
    );
  }

  /**
   * The collector keeps opened projects in memory, so a restart forgets them and
   * every call starts failing with PROJECT_NOT_OPEN. Reopen by the path already
   * held and retry once, rather than stranding autosave in an error state until
   * the operator notices and re-picks the project by hand.
   */
  async function withOpenProject<Value>(run: () => Promise<Value>): Promise<Value> {
    try {
      return await run();
    } catch (caught) {
      const opened = project;
      if (opened == null || !(caught instanceof DaGamaApiError) || !caught.isProjectNotOpen) throw caught;
      await openDaGamaProject(opened.path, fetchImpl);
      return run();
    }
  }

  function adopt(document: DaGamaBoardDocument, opened: DaGamaProject): void {
    const summary = boardSummaryOf(document);
    completed = requested;
    board = document.board;
    activeBoard = summary;
    savedSignature = daGamaBoardSignature(document.board);
    saveState = 'saved';
    error = '';
    serverRevision = null;
    rememberSummary(summary);
    writeLastBoardId(opened.id, document.id, storage);
    cacheDraft();
    publish();
  }

  function clearActiveBoard(nextBoard: DaGamaBoard | null): void {
    completed = requested;
    activeBoard = null;
    savedSignature = null;
    saveState = 'draft';
    serverRevision = null;
    if (nextBoard !== null) board = nextBoard;
    writeDraftMetadata(null, storage);
    writeDraft(board, storage);
    publish();
  }

  async function drain(): Promise<void> {
    while (!disposed && completed < requested) {
      const generation = requested;
      const opened = project;
      const document = activeBoard;
      if (opened == null || document == null) {
        // Nothing to write against; the edit stays a local draft.
        completed = requested;
        break;
      }
      const pending = board;
      const signature = daGamaBoardSignature(pending);
      try {
        const saved = await withOpenProject(() =>
          writeDaGamaBoard(
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
        // Only the generation actually sent becomes clean. A newer edit made
        // while this request was in flight stays dirty and loops.
        completed = Math.max(completed, generation);
        const stillActive = project?.id === opened.id && activeBoard?.id === document.id;
        if (!stillActive) break;
        activeBoard = summary;
        savedSignature = signature;
        serverRevision = null;
        writeDraftMetadata(
          { projectId: opened.id, boardId: summary.id, revision: summary.revision },
          storage,
        );
        if (completed === requested) {
          saveState = 'saved';
          error = '';
        }
        publish();
      } catch (caught) {
        if (disposed) return;
        // Stop the loop: retrying a write the server just refused would spin.
        completed = Math.max(completed, requested);
        const conflict = caught instanceof DaGamaApiError && caught.isConflict;
        saveState = conflict ? 'conflict' : 'error';
        serverRevision = conflict ? ((caught as DaGamaApiError).actualRevision ?? null) : null;
        error = messageOf(caught, 'The board could not be saved.');
        publish();
        return;
      }
    }
  }

  function flush(): Promise<void> {
    cancelTimer();
    cacheDraft();
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

  async function loadProject(projectPath: string): Promise<DaGamaProject> {
    const opened = await openDaGamaProject(projectPath, fetchImpl);
    project = opened;
    writeLastProjectPath(opened.path, storage);
    const listed = await listDaGamaBoards(opened.id, fetchImpl);
    boards = [...listed.boards].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
    boardErrors = listed.errors;
    error =
      listed.errors.length > 0
        ? `${listed.errors.length} board file(s) in this project could not be read.`
        : '';
    return opened;
  }

  async function chooseProject(projectPath: string): Promise<void> {
    // A pending edit belongs to the project being left, so it is flushed before
    // switching and the switch is abandoned if it cannot land.
    if (activeBoard != null && daGamaBoardSignature(board) !== savedSignature) {
      if (saveState === 'conflict') {
        throw new Error('Resolve the board conflict before switching projects.');
      }
      await flush();
      if (daGamaBoardSignature(board) !== savedSignature) {
        throw new Error('The open board could not be saved, so the project was not switched.');
      }
    }

    const hadOpenBoard = activeBoard != null;
    const cachedSignature = daGamaBoardSignature(board);
    saveState = 'loading';
    publish();

    const opened = await loadProject(projectPath);
    const lastId = readLastBoardId(opened.id, storage);
    const target = boards.find((candidate) => candidate.id === lastId) ?? boards[0];
    if (target === undefined) {
      // No boards yet: keep the current draft so a workflow configured before a
      // project was picked is not thrown away.
      clearActiveBoard(null);
      return;
    }

    const document = await withOpenProject(() => readDaGamaBoard(opened.id, target.id, fetchImpl));
    const storedSignature = daGamaBoardSignature(document.board);
    const draft = readDraftMetadata(storage);
    const draftMatches = draft?.projectId === opened.id && draft.boardId === document.id;
    const unboundDraft =
      !hadOpenBoard && draft == null && cachedSignature !== daGamaBoardSignature(defaultDaGamaBoard());

    if (unboundDraft) {
      clearActiveBoard(null);
      return;
    }
    if (draftMatches && cachedSignature !== storedSignature) {
      // A recovery draft exists for this exact board. The same revision means an
      // autosave never landed and can resume; a different revision means the
      // file moved on and the operator must choose.
      completed = requested;
      activeBoard = boardSummaryOf(document);
      savedSignature = storedSignature;
      if (draft.revision === document.revision) {
        saveState = 'saving';
        error = '';
        requested += 1;
        publish();
        schedule();
      } else {
        saveState = 'conflict';
        serverRevision = document.revision;
        error = 'This board changed while a recovery draft was pending.';
        publish();
      }
      return;
    }
    adopt(document, opened);
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
      board = next;
      if (activeBoard != null && daGamaBoardSignature(next) === savedSignature) {
        // The edit landed back on the stored value; nothing to write.
        completed = requested;
        if (saveState !== 'conflict' && saveState !== 'error') saveState = 'saved';
        publish();
        cacheDraft();
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
        adopt(await withOpenProject(() => readDaGamaBoard(opened.id, id, fetchImpl)), opened);
      } catch (caught) {
        saveState = 'error';
        error = messageOf(caught, 'The board could not be opened.');
        publish();
      }
    },

    async saveAs(name) {
      const opened = project;
      if (opened == null) throw new Error('Choose a project before saving a board.');
      saveState = 'saving';
      error = '';
      publish();
      try {
        const saved = await withOpenProject(() =>
          writeDaGamaBoard(
            { projectId: opened.id, id: newBoardId(), name, board, expectedRevision: 0 },
            fetchImpl,
          ),
        );
        // Adopt the *saved* document so identity fields the server added are
        // kept, but keep the operator's live board content, which is what was
        // just written.
        adopt({ ...saved, board }, opened);
        return boardSummaryOf(saved);
      } catch (caught) {
        saveState = 'error';
        error = messageOf(caught, 'The board could not be saved.');
        publish();
        throw caught;
      }
    },

    async deleteBoard(summary) {
      const opened = project;
      if (opened == null) return;
      try {
        await withOpenProject(() => deleteDaGamaBoard(opened.id, summary.id, summary.revision, fetchImpl));
        boards = boards.filter((candidate) => candidate.id !== summary.id);
        if (activeBoard?.id === summary.id) {
          writeLastBoardId(opened.id, null, storage);
          clearActiveBoard(defaultDaGamaBoard());
          return;
        }
        publish();
      } catch (caught) {
        saveState = caught instanceof DaGamaApiError && caught.isConflict ? 'conflict' : 'error';
        error = messageOf(caught, 'The board could not be deleted.');
        publish();
      }
    },

    newBoard() {
      cancelTimer();
      if (project != null) writeLastBoardId(project.id, null, storage);
      clearActiveBoard(defaultDaGamaBoard());
    },

    flush,

    async keepLocal() {
      if (disposed || saveState !== 'conflict') return;
      const opened = project;
      const document = activeBoard;
      if (opened == null || document == null) return;
      // Rebase onto the revision the server actually holds. When the conflict
      // did not carry one, read the stored document for it — the local board is
      // never discarded here, only re-based.
      let revision = serverRevision;
      if (revision == null) {
        try {
          revision = (await withOpenProject(() => readDaGamaBoard(opened.id, document.id, fetchImpl)))
            .revision;
        } catch (caught) {
          error = messageOf(caught, 'The stored board could not be read.');
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
        adopt(await withOpenProject(() => readDaGamaBoard(opened.id, document.id, fetchImpl)), opened);
      } catch (caught) {
        saveState = 'error';
        error = messageOf(caught, 'The stored board could not be read.');
        publish();
      }
    },

    async restore() {
      const path = readLastProjectPath(storage);
      if (path === '') return;
      try {
        await chooseProject(path);
      } catch (caught) {
        saveState = 'error';
        error = messageOf(caught, 'The last project could not be reopened.');
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
