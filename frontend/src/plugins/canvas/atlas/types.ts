// Wire types for the Atlas run and board API.
//
// These mirror `collector/internal/plugins/canvas/atlas/run.go` rather than
// being derived from it: the Go package cannot be imported into the browser
// bundle, so drift shows up at the first use site instead of at runtime.
//
// The shape that matters most here is `attempts`. A DaGama stage has one
// attempt; an Atlas stage is a committee, so it has several at the same
// instance plus the refine turn that follows them. Anything that addresses an
// attempt does so by id, because a component id is no longer enough.

import type { AtlasComponentID, AtlasVendor } from '@/plugins/canvas/atlas/vocabulary';
import type { CanvasSessionIdentity } from '@/plugins/canvas/contracts';

export type AtlasProject = { id: string; name: string; path: string };

export type AtlasRunStatus =
  | 'preparing'
  | 'running'
  | 'awaiting_approval'
  | 'succeeded'
  | 'failed'
  | 'canceled'
  // A run imported from legacy data. It is history and never resumes.
  | 'interrupted_migration';

export type AtlasComponentStatus =
  'blocked' | 'ready' | 'running' | 'validating' | 'awaiting_approval' | 'succeeded' | 'failed';

export type AtlasOwnership = 'automated' | 'human_controlled';

export type AtlasAttemptStatus = 'launch_requested' | 'running' | 'exited';

/** Stable taxonomy reasons the run cards surface verbatim. */
export const ATLAS_REASON_WAITING_FOR_TRIGGER = 'waiting_for_trigger';
export const ATLAS_REASON_WAITING_FOR_REPAIR = 'waiting_for_repair';
export const ATLAS_REASON_WAITING_FOR_FEEDBACK = 'waiting_for_feedback';
export const ATLAS_REASON_BLOCKED_BY_GATE = 'blocked_by_gate';
export const ATLAS_REASON_GATE_REJECTED = 'gate_rejected';

export type AtlasSourceRecord = {
  kind: string;
  title: string;
  path: string;
  bytes: number;
  sha256: string;
};

export type AtlasSourceInput =
  { kind: 'text'; title: string; text: string } | { kind: 'file'; title: string; path: string };

export type AtlasAttemptState = {
  attemptId: string;
  componentId: AtlasComponentID | string;
  instance: number;
  /** The run-log seat: a committee position, or the refine turn. */
  seatId: string;
  attempt: number;
  tmuxName: string;
  /** The composite {agent,id} identity. A bare id is never enough. */
  session: CanvasSessionIdentity | null;
  ownership: AtlasOwnership;
  status: AtlasAttemptStatus;
  exitCode: number | null;
  startedAt: string | null;
  finishedAt: string | null;
};

export type AtlasComponentRunState = {
  id: AtlasComponentID | string;
  status: AtlasComponentStatus;
  instance: number;
  reason?: string;
  message?: string;
  startedAt: string | null;
  finishedAt: string | null;
  outputs: string[];
  /** The latest attempt. Retries replace it; history stays in the log. */
  attempt: AtlasAttemptState | null;
  /** Every attempt of the current instance, which is what keeps a fan-out addressable. */
  attempts: AtlasAttemptState[];
};

export type AtlasChangedFile = { path: string; status: string };

export type AtlasChangeRecord = {
  changeRevision: number;
  treeOid: string;
  patchSha256: string;
  patchBytes: number;
  insertions: number;
  deletions: number;
  changedFiles: AtlasChangedFile[];
  baseSha: string;
};

export type AtlasGateRecord = {
  componentId: AtlasComponentID | string;
  instance: number;
  reason: string;
  message: string;
  /** Empty string and null both mean undecided; Go emits `""`. */
  decision?: 'approved' | 'rejected' | '' | null;
  changeRevision: number | null;
  openedAt: string;
  decidedAt: string | null;
};

export type AtlasPublicationRecord = {
  changeRevision: number;
  commitSha: string;
  branch: string;
  remote: string;
  prUrl?: string;
  prNumber?: number;
  action: string;
  idempotencyKey: string;
  publishedAt: string;
};

export type AtlasArtifactRecord = {
  artifactId: string;
  kind: string;
  name: string;
  path: string;
  sha256: string;
  bytes: number;
  createdAt: string;
  producer: {
    componentId: AtlasComponentID | string;
    instance: number;
    seatId?: string;
    attempt?: number;
  };
};

export type AtlasFailureRecord = { reason: string; message: string };

export type AtlasRun = {
  schemaVersion: number;
  runId: string;
  projectId: string;
  boardId: string;
  boardRevision: number;
  title: string;
  status: AtlasRunStatus;
  createdAt: string | null;
  updatedAt: string | null;
  finishedAt: string | null;
  source: AtlasSourceRecord | null;
  runRoot?: string;
  branch?: string;
  baseBranch?: string;
  baseSha?: string;
  publishBaseBranch?: string;
  publishBaseSha?: string;
  /** Absent for a plain-folder run, where publication is unavailable. */
  remoteUrl?: string;
  components: Record<string, AtlasComponentRunState>;
  artifacts: AtlasArtifactRecord[];
  /** Every composite identity the run has bound, so a session card can link back. */
  sessions?: CanvasSessionIdentity[];
  change: AtlasChangeRecord | null;
  gate: AtlasGateRecord | null;
  publication: AtlasPublicationRecord | null;
  failure: AtlasFailureRecord | null;
  lastSeq: number;
};

export type AtlasRunSummary = Pick<
  AtlasRun,
  'runId' | 'projectId' | 'boardId' | 'title' | 'status' | 'createdAt' | 'updatedAt' | 'finishedAt'
>;

export type AtlasRunPreview = {
  projectPath: string;
  baseBranch: string;
  baseSha: string;
  defaultBranch: string;
  checkoutBranch: string | null;
  isLinkedWorktree: boolean;
  /** Null for a plain folder, which is supported but cannot publish. */
  remoteUrl: string | null;
  /** False when the project is an ordinary directory rather than a repository. */
  isGitRepository: boolean;
  runRootParent: string;
};

export type AtlasPublishChecklistItem = {
  id: string;
  label: string;
  ok: boolean;
  detail: string;
};

export type AtlasPublishPreflight = {
  ok: boolean;
  changeRevision: number;
  treeOid: string;
  patchSha256: string;
  branch: string;
  baseBranch: string;
  baseSha: string;
  remoteUrl: string | null;
  draft: boolean;
  checklist: AtlasPublishChecklistItem[];
};

/** Terminal handle for a seat attempt. Native PTY/WebSocket, never a URL. */
export type AtlasTerminalHandle = {
  terminalId: string;
  attemptId: string;
  writable?: boolean;
};

export type AtlasBoardLoadError = { file: string; code: string; message: string };

/**
 * The composite Canvas identity of an attempt.
 *
 * The vendor comes from the seat and the id from the attempt. Neither is
 * sufficient alone, which is exactly why CONTRACTS.md forbids keying Canvas
 * state by `id`.
 */
export function attemptSessionIdentity(
  vendor: AtlasVendor | null | undefined,
  attempt: AtlasAttemptState | null | undefined,
): CanvasSessionIdentity | null {
  const session = attempt?.session ?? null;
  if (session === null) return null;
  const id = session.id ?? '';
  const agent = session.agent || vendor || '';
  if (id === '' || agent === '') return null;
  return { agent, id };
}
