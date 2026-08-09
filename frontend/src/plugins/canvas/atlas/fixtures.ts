// Frozen Atlas fixtures.
//
// Transcribed from the collector's Go JSON tags rather than captured from a
// live server, so a backend field rename shows up here as a failing test rather
// than as an empty card.

import { defaultAtlasBoard, serializeAtlasBoard } from '@/plugins/canvas/atlas/graph';
import type {
  AtlasAttemptState,
  AtlasComponentRunState,
  AtlasProject,
  AtlasPublishPreflight,
  AtlasRun,
  AtlasRunPreview,
} from '@/plugins/canvas/atlas/types';

export const FROZEN_ATLAS_PROJECT: AtlasProject = {
  id: 'demo-3f9a1c2b4d5e',
  name: 'demo',
  path: '/Users/example/code/demo',
};

function blocked(id: string): AtlasComponentRunState {
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
    attempts: [],
  };
}

function components(
  overrides: Record<string, AtlasComponentRunState>,
): Record<string, AtlasComponentRunState> {
  const base: Record<string, AtlasComponentRunState> = {};
  for (const id of ['intake', 'plan', 'build', 'verify', 'review', 'publish']) {
    base[id] = overrides[id] ?? blocked(id);
  }
  return base;
}

/** One committee attempt. */
export function atlasAttempt(overrides: Partial<AtlasAttemptState> = {}): AtlasAttemptState {
  return {
    attemptId: 'run-20260809t070000-0a1b2c3d-plan-1-plan-1-1',
    componentId: 'plan',
    instance: 1,
    seatId: 'plan-1',
    attempt: 1,
    tmuxName: 'coslash_atlas_0a1b2c3d',
    session: { agent: 'claude', id: '0f9a4d1e-2b3c-4d5e-8f60-112233445566' },
    ownership: 'automated',
    status: 'running',
    exitCode: null,
    startedAt: '2026-08-09T07:01:00Z',
    finishedAt: null,
    ...overrides,
  };
}

const BASE_RUN: AtlasRun = {
  schemaVersion: 1,
  runId: 'run-20260809t070000-0a1b2c3d',
  projectId: FROZEN_ATLAS_PROJECT.id,
  boardId: 'board-1',
  boardRevision: 3,
  title: 'Add a logout button',
  status: 'running',
  createdAt: '2026-08-09T07:00:00Z',
  updatedAt: '2026-08-09T07:05:00Z',
  finishedAt: null,
  source: {
    kind: 'text',
    title: 'Add a logout button',
    path: '',
    bytes: 128,
    sha256: 'a'.repeat(64),
  },
  runRoot: '/Users/example/.coslash/atlas/roots/run-20260809t070000-0a1b2c3d',
  branch: 'atlas/run-20260809t070000-0a1b2c3d',
  baseBranch: 'main',
  baseSha: 'b'.repeat(40),
  publishBaseBranch: 'main',
  publishBaseSha: 'b'.repeat(40),
  remoteUrl: 'https://github.com/example/demo.git',
  components: components({}),
  artifacts: [],
  sessions: [],
  change: null,
  gate: null,
  publication: null,
  failure: null,
  lastSeq: 12,
};

/** A three-worker plan committee mid fan-out. */
export const FROZEN_ATLAS_COMMITTEE_RUN: AtlasRun = {
  ...BASE_RUN,
  components: components({
    intake: { ...blocked('intake'), status: 'succeeded', outputs: ['SOURCE.md', 'PROBLEM.md'] },
    plan: {
      ...blocked('plan'),
      status: 'running',
      startedAt: '2026-08-09T07:01:00Z',
      attempts: [
        atlasAttempt({ seatId: 'plan-1', attemptId: 'a-plan-1', status: 'exited', exitCode: 0 }),
        atlasAttempt({ seatId: 'plan-2', attemptId: 'a-plan-2', status: 'running' }),
        atlasAttempt({ seatId: 'plan-3', attemptId: 'a-plan-3', status: 'running' }),
      ],
      attempt: atlasAttempt({ seatId: 'plan-3', attemptId: 'a-plan-3', status: 'running' }),
    },
  }),
  sessions: [{ agent: 'claude', id: '0f9a4d1e-2b3c-4d5e-8f60-112233445566' }],
};

/** The same committee with its refine turn running over the drafts. */
export const FROZEN_ATLAS_REFINING_RUN: AtlasRun = {
  ...BASE_RUN,
  components: components({
    plan: {
      ...blocked('plan'),
      status: 'running',
      attempts: [
        atlasAttempt({ seatId: 'plan-1', attemptId: 'a-plan-1', status: 'exited', exitCode: 0 }),
        atlasAttempt({ seatId: 'plan-2', attemptId: 'a-plan-2', status: 'exited', exitCode: 0 }),
        atlasAttempt({
          seatId: 'plan-main-refine',
          attemptId: 'a-plan-refine',
          status: 'running',
        }),
      ],
    },
  }),
};

/** A committee where one sibling failed and the stage continued anyway. */
export const FROZEN_ATLAS_PARTIAL_RUN: AtlasRun = {
  ...BASE_RUN,
  components: components({
    plan: {
      ...blocked('plan'),
      status: 'succeeded',
      outputs: ['PLAN.md'],
      attempts: [
        atlasAttempt({ seatId: 'plan-1', attemptId: 'a-plan-1', status: 'exited', exitCode: 0 }),
        atlasAttempt({ seatId: 'plan-2', attemptId: 'a-plan-2', status: 'exited', exitCode: 1 }),
      ],
    },
  }),
};

/** An attempt the operator has taken control of. */
export const FROZEN_ATLAS_HUMAN_CONTROLLED_RUN: AtlasRun = {
  ...BASE_RUN,
  components: components({
    build: {
      ...blocked('build'),
      status: 'running',
      attempts: [
        atlasAttempt({
          componentId: 'build',
          seatId: 'build-1',
          attemptId: 'a-build-1',
          ownership: 'human_controlled',
          status: 'running',
        }),
      ],
    },
  }),
};

/** A failed committee stage, the only state a retry is accepted in. */
export const FROZEN_ATLAS_FAILED_RUN: AtlasRun = {
  ...BASE_RUN,
  components: components({
    plan: {
      ...blocked('plan'),
      status: 'failed',
      reason: 'missing_output',
      message: 'No committee member produced a draft.',
      attempts: [atlasAttempt({ seatId: 'plan-1', attemptId: 'a-plan-1', status: 'exited', exitCode: 1 })],
    },
  }),
};

/** The publish gate, open and decidable. */
export const FROZEN_ATLAS_PUBLISH_GATE_RUN: AtlasRun = {
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
    reason: 'blocked_by_gate',
    message: 'the change is verified and reviewed and is waiting for your approval',
    decision: '',
    changeRevision: 2,
    openedAt: '2026-08-09T07:20:00Z',
    decidedAt: null,
  },
  components: components({
    publish: { ...blocked('publish'), status: 'awaiting_approval', reason: 'blocked_by_gate' },
  }),
};

/** A manual trigger edge waiting for the operator's go. */
export const FROZEN_ATLAS_TRIGGER_GATE_RUN: AtlasRun = {
  ...BASE_RUN,
  status: 'awaiting_approval',
  gate: {
    componentId: 'build',
    instance: 1,
    reason: 'waiting_for_trigger',
    message: 'build waits for an explicit go from plan',
    decision: '',
    changeRevision: null,
    openedAt: '2026-08-09T07:10:00Z',
    decidedAt: null,
  },
  components: components({
    plan: { ...blocked('plan'), status: 'succeeded', outputs: ['PLAN.md'] },
    build: { ...blocked('build'), status: 'awaiting_approval', reason: 'waiting_for_trigger' },
  }),
};

/** A plain-folder run: supported, but publication is unavailable. */
export const FROZEN_ATLAS_PLAIN_FOLDER_RUN: AtlasRun = {
  ...FROZEN_ATLAS_PUBLISH_GATE_RUN,
  remoteUrl: '',
};

export const FROZEN_ATLAS_RUN_PREVIEW: AtlasRunPreview = {
  projectPath: FROZEN_ATLAS_PROJECT.path,
  baseBranch: 'main',
  baseSha: 'b'.repeat(40),
  defaultBranch: 'main',
  checkoutBranch: 'main',
  isLinkedWorktree: false,
  remoteUrl: 'https://github.com/example/demo.git',
  isGitRepository: true,
  runRootParent: '/Users/example/.coslash/atlas/roots',
};

export const FROZEN_ATLAS_PUBLISH_PREFLIGHT: AtlasPublishPreflight = {
  ok: true,
  changeRevision: 2,
  treeOid: 'c'.repeat(40),
  patchSha256: 'd'.repeat(64),
  branch: 'atlas/run-20260809t070000-0a1b2c3d',
  baseBranch: 'main',
  baseSha: 'b'.repeat(40),
  remoteUrl: 'https://github.com/example/demo.git',
  draft: true,
  checklist: [
    { id: 'verified', label: 'Checks passed for this revision', ok: true, detail: '' },
    { id: 'reviewed', label: 'Review approved this revision', ok: true, detail: '' },
  ],
};

/** The stored board payload, exactly as the board API returns one. */
export const FROZEN_ATLAS_STORED_BOARD: Record<string, unknown> = serializeAtlasBoard(defaultAtlasBoard());
