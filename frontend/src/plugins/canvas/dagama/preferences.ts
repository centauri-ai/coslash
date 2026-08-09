// Browser-local navigation memory for the DaGama board.
//
// None of this is authoritative — the project's board file is, and the run
// directory is — so every accessor degrades to a neutral value rather than
// throwing. What lives here is only "which project, board, and run was I last
// looking at", plus a recovery copy of the live board so a reload during an
// interrupted autosave neither loses the edit nor overwrites a newer server
// revision with it.
//
// The keys are namespaced under `coslash.canvas.dagama.` and are deliberately
// distinct from every legacy Fleetlog key: sharing one would be silent data
// loss rather than an inconvenience, because each canvas's normalizer discards
// what it does not recognise and autosave writes the result straight back.
// Nothing written here may contain a token, a prompt body, or terminal output.

import { normalizeDaGamaBoard, type DaGamaBoard } from '@/plugins/canvas/dagama/board';

const PREFIX = 'coslash.canvas.dagama.';
const LAST_PROJECT_KEY = `${PREFIX}project.v1`;
const LAST_BOARD_PREFIX = `${PREFIX}boardId.v1.`;
const LAST_RUN_PREFIX = `${PREFIX}runId.v1.`;
const DRAFT_KEY = `${PREFIX}draft.v1`;
const DRAFT_METADATA_KEY = `${PREFIX}draftMetadata.v1`;

export type DaGamaStorageLike = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

function store(): DaGamaStorageLike | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    // A blocked or partitioned storage is the same as no storage here.
    return null;
  }
}

function read(key: string, storage: DaGamaStorageLike | null): string {
  try {
    return storage?.getItem(key) ?? '';
  } catch {
    return '';
  }
}

function write(key: string, value: string | null, storage: DaGamaStorageLike | null): void {
  try {
    if (value === null) storage?.removeItem(key);
    else storage?.setItem(key, value);
  } catch {
    // The selection remains usable for this tab.
  }
}

export function readLastProjectPath(storage = store()): string {
  return read(LAST_PROJECT_KEY, storage);
}

export function writeLastProjectPath(projectPath: string, storage = store()): void {
  write(LAST_PROJECT_KEY, projectPath, storage);
}

export function readLastBoardId(projectId: string, storage = store()): string {
  return read(`${LAST_BOARD_PREFIX}${projectId}`, storage);
}

export function writeLastBoardId(projectId: string, boardId: string | null, storage = store()): void {
  write(`${LAST_BOARD_PREFIX}${projectId}`, boardId, storage);
}

export function readLastRunId(projectId: string, storage = store()): string {
  return read(`${LAST_RUN_PREFIX}${projectId}`, storage);
}

export function writeLastRunId(projectId: string, runId: string | null, storage = store()): void {
  write(`${LAST_RUN_PREFIX}${projectId}`, runId, storage);
}

/**
 * Binds a cached editing copy to one board revision.
 *
 * Without the revision, reload could not tell "my autosave never landed" from
 * "someone else saved a newer board", and would have to guess. With it, the
 * first case resumes the write and the second raises a visible conflict.
 */
export type DaGamaDraftMetadata = { projectId: string; boardId: string; revision: number };

export function readDraft(storage = store()): DaGamaBoard | null {
  const raw = read(DRAFT_KEY, storage);
  if (raw === '') return null;
  try {
    return normalizeDaGamaBoard(JSON.parse(raw));
  } catch {
    return null;
  }
}

export function writeDraft(board: DaGamaBoard | null, storage = store()): void {
  if (board === null) {
    write(DRAFT_KEY, null, storage);
    return;
  }
  try {
    write(DRAFT_KEY, JSON.stringify(board), storage);
  } catch {
    // A board that cannot be cached is still fully usable in this tab.
  }
}

export function readDraftMetadata(storage = store()): DaGamaDraftMetadata | null {
  const raw = read(DRAFT_METADATA_KEY, storage);
  if (raw === '') return null;
  try {
    const value = JSON.parse(raw) as Partial<DaGamaDraftMetadata> | null;
    if (
      value == null ||
      typeof value.projectId !== 'string' ||
      typeof value.boardId !== 'string' ||
      typeof value.revision !== 'number'
    ) {
      return null;
    }
    return { projectId: value.projectId, boardId: value.boardId, revision: value.revision };
  } catch {
    return null;
  }
}

export function writeDraftMetadata(metadata: DaGamaDraftMetadata | null, storage = store()): void {
  write(DRAFT_METADATA_KEY, metadata === null ? null : JSON.stringify(metadata), storage);
}
