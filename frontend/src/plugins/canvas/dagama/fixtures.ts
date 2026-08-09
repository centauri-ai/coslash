// Frozen DaGama fixtures.
//
// These are the shapes the collector's `dagama` package emits, transcribed from
// its Go JSON tags rather than captured from a live server, so a backend field
// rename shows up here as a failing test instead of as an empty card. They are
// the board's parity reference until the `/api/dagama` route group exists.

import type { DaGamaBoardDocument } from '@/plugins/canvas/dagama/api';
import { defaultDaGamaBoard, serializeDaGamaBoard } from '@/plugins/canvas/dagama/board';
import type {
  DaGamaComponentRunState,
  DaGamaProject,
  DaGamaPublishPreflight,
  DaGamaRun,
  DaGamaRunPreview,
} from '@/plugins/canvas/dagama/types';
import { DAGAMA_COMPONENT_IDS, type DaGamaComponentId } from '@/plugins/canvas/dagama/vocabulary';

export const FROZEN_DAGAMA_PROJECT: DaGamaProject = {
  id: 'demo-project',
  name: 'demo',
  path: '/Users/example/code/demo',
};

function blocked(id: DaGamaComponentId): DaGamaComponentRunState {
  return {
    id,
    status: 'blocked',
    instance: 1,
    reason: '',
    message: '',
    startedAt: null,
    finishedAt: null,
    outputs: [],
    attempt: null,
  };
}

function components(
  overrides: Partial<Record<DaGamaComponentId, DaGamaComponentRunState>>,
): Record<DaGamaComponentId, DaGamaComponentRunState> {
  return Object.fromEntries(DAGAMA_COMPONENT_IDS.map((id) => [id, overrides[id] ?? blocked(id)])) as Record<
    DaGamaComponentId,
    DaGamaComponentRunState
  >;
}

const BASE_RUN: DaGamaRun = {
  schemaVersion: 1,
  runId: 'run-20260809t051500-0a1b2c3d',
  projectId: FROZEN_DAGAMA_PROJECT.id,
  boardId: 'board-1',
  boardRevision: 3,
  title: 'Add a logout button',
  status: 'running',
  createdAt: '2026-08-09T05:15:00Z',
  updatedAt: '2026-08-09T05:19:00Z',
  finishedAt: null,
  source: {
    kind: 'text',
    title: 'Add a logout button',
    path: null,
    bytes: 128,
    sha256: 'a'.repeat(64),
  },
  runRoot: '/Users/example/.coslash/dagama/runs/run-20260809t051500-0a1b2c3d',
  branch: 'dagama/run-20260809t051500-0a1b2c3d',
  baseBranch: 'main',
  baseSha: 'b'.repeat(40),
  remoteUrl: 'https://github.com/example/demo.git',
  components: components({}),
  artifacts: [],
  change: null,
  gate: null,
  publication: null,
  failure: null,
  lastSeq: 12,
};

/** A Build seat running an automated Claude attempt. */
export const FROZEN_DAGAMA_RUNNING_RUN: DaGamaRun = {
  ...BASE_RUN,
  components: components({
    intake: {
      ...blocked('intake'),
      status: 'succeeded',
      startedAt: '2026-08-09T05:15:01Z',
      finishedAt: '2026-08-09T05:15:04Z',
      outputs: ['SOURCE.md', 'source.json', 'PROBLEM.md'],
    },
    plan: {
      ...blocked('plan'),
      status: 'succeeded',
      startedAt: '2026-08-09T05:15:05Z',
      finishedAt: '2026-08-09T05:17:00Z',
      outputs: ['PLAN.md'],
    },
    build: {
      ...blocked('build'),
      status: 'running',
      startedAt: '2026-08-09T05:17:01Z',
      attempt: {
        attemptId: 'run-20260809t051500-0a1b2c3d-build-1-1',
        componentId: 'build',
        instance: 1,
        seatId: 'build-1',
        attempt: 1,
        tmuxName: 'coslash-dagama-run-20260809t051500-0a1b2c3d-build-1-1',
        sessionId: '0f9a4d1e-2b3c-4d5e-8f60-112233445566',
        ownership: 'automated',
        status: 'running',
        exitCode: null,
        startedAt: '2026-08-09T05:17:01Z',
        finishedAt: null,
      },
    },
  }),
};

/** The publish gate, open and decidable, with a captured change revision. */
export const FROZEN_DAGAMA_PUBLISH_GATE_RUN: DaGamaRun = {
  ...BASE_RUN,
  status: 'awaiting_approval',
  change: {
    changeRevision: 2,
    treeOid: 'c'.repeat(40),
    patchSha256: 'd'.repeat(64),
    patchBytes: 2048,
    insertions: 42,
    deletions: 7,
    changedFiles: [{ path: 'src/app.tsx', status: 'M' }],
    baseSha: 'b'.repeat(40),
  },
  gate: {
    componentId: 'publish',
    instance: 1,
    reason: 'awaiting_publish_approval',
    message: 'The change is verified and reviewed.',
    decision: '',
    changeRevision: 2,
    openedAt: '2026-08-09T05:20:00Z',
    decidedAt: null,
  },
  components: components({
    publish: { ...blocked('publish'), status: 'awaiting_approval', reason: 'awaiting_publish_approval' },
  }),
};

/** A repair gate opened on Verify after the automatic Build repair bound. */
export const FROZEN_DAGAMA_REPAIR_GATE_RUN: DaGamaRun = {
  ...BASE_RUN,
  status: 'awaiting_approval',
  gate: {
    componentId: 'verify',
    instance: 2,
    reason: 'waiting_for_repair',
    message: 'Two repair rounds did not make the checks pass.',
    decision: '',
    changeRevision: null,
    openedAt: '2026-08-09T05:22:00Z',
    decidedAt: null,
  },
  components: components({
    verify: {
      ...blocked('verify'),
      status: 'awaiting_approval',
      instance: 2,
      reason: 'waiting_for_repair',
      message: 'Two repair rounds did not make the checks pass.',
      outputs: ['verification.json'],
    },
  }),
};

/** A Build seat the operator has taken control of. */
export const FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN: DaGamaRun = {
  ...FROZEN_DAGAMA_RUNNING_RUN,
  components: components({
    ...FROZEN_DAGAMA_RUNNING_RUN.components,
    build: {
      ...FROZEN_DAGAMA_RUNNING_RUN.components.build,
      attempt: {
        ...FROZEN_DAGAMA_RUNNING_RUN.components.build.attempt!,
        attempt: 2,
        ownership: 'human_controlled',
      },
    },
  }),
};

/** A failed Build seat, which is the only state Retry is accepted in. */
export const FROZEN_DAGAMA_FAILED_RUN: DaGamaRun = {
  ...BASE_RUN,
  components: components({
    build: {
      ...blocked('build'),
      status: 'failed',
      reason: 'missing_output',
      message: 'The attempt exited without writing IMPLEMENTATION.md.',
      startedAt: '2026-08-09T05:17:01Z',
      finishedAt: '2026-08-09T05:18:30Z',
      attempt: {
        attemptId: 'run-20260809t051500-0a1b2c3d-build-1-1',
        componentId: 'build',
        instance: 1,
        seatId: 'build-1',
        attempt: 1,
        tmuxName: 'coslash-dagama-run-20260809t051500-0a1b2c3d-build-1-1',
        sessionId: '0f9a4d1e-2b3c-4d5e-8f60-112233445566',
        ownership: 'automated',
        status: 'exited',
        exitCode: 1,
        startedAt: '2026-08-09T05:17:01Z',
        finishedAt: '2026-08-09T05:18:30Z',
      },
    },
  }),
};

export const FROZEN_DAGAMA_RUN_PREVIEW: DaGamaRunPreview = {
  projectPath: FROZEN_DAGAMA_PROJECT.path,
  baseBranch: 'main',
  baseSha: 'b'.repeat(40),
  defaultBranch: 'main',
  checkoutBranch: 'main',
  isLinkedWorktree: false,
  remoteUrl: 'https://github.com/example/demo.git',
  runRootParent: '/Users/example/.coslash/dagama/runs',
};

export const FROZEN_DAGAMA_PUBLISH_PREFLIGHT: DaGamaPublishPreflight = {
  ok: true,
  changeRevision: 2,
  patchSha256: 'd'.repeat(64),
  treeOid: 'c'.repeat(40),
  branch: 'dagama/run-20260809t051500-0a1b2c3d',
  baseBranch: 'main',
  baseSha: 'b'.repeat(40),
  remoteUrl: 'https://github.com/example/demo.git',
  checklist: [
    { id: 'verified', label: 'Checks passed for this revision', ok: true, detail: '' },
    { id: 'reviewed', label: 'Review approved this revision', ok: true, detail: '' },
  ],
  draft: true,
};

/** A stored board document, exactly as the board API returns one. */
export const FROZEN_DAGAMA_BOARD_DOCUMENT: DaGamaBoardDocument = {
  schemaVersion: 1,
  id: 'board-1',
  name: 'Logout button',
  revision: 3,
  createdAt: '2026-08-09T05:00:00Z',
  updatedAt: '2026-08-09T05:14:00Z',
  board: defaultDaGamaBoard(),
};

/**
 * The flat board payload the collector stores, including the identity fields
 * the editor never shows. Used to prove those survive an edit round trip.
 */
export const FROZEN_DAGAMA_STORED_BOARD: Record<string, unknown> = {
  ...serializeDaGamaBoard(defaultDaGamaBoard()),
  id: 'board-1',
  name: 'Logout button',
  projectId: FROZEN_DAGAMA_PROJECT.id,
  projectPath: FROZEN_DAGAMA_PROJECT.path,
  revision: 3,
  createdAt: '2026-08-09T05:00:00Z',
  updatedAt: '2026-08-09T05:14:00Z',
};
