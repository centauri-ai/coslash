// Wire types for the DaGama run and board API.
//
// These mirror the backend's `dagama.RunState` / `dagama.RunSummary` JSON rather
// than being derived from it, exactly as the legacy client mirrored its dev
// server: the Go package cannot be imported into the browser bundle, so drift is
// caught at the first use site instead of at runtime.
//
// Nullability follows Go's encoding, which is looser than the legacy dev
// server's: a Go string field is `""` where the legacy server wrote `null`, and
// a Go pointer is `null`. Every consumer here therefore treats empty string and
// null as the same "absent", so the UI reads the same against either producer.

import type { CanvasSessionIdentity } from '@/plugins/canvas/contracts';
import type {
  DaGamaComponentId,
  DaGamaSeatComponentId,
  DaGamaVendor,
} from '@/plugins/canvas/dagama/vocabulary';

export type DaGamaProject = { id: string; name: string; path: string };

export type DaGamaRunStatus =
  | 'preparing'
  | 'running'
  | 'awaiting_approval'
  | 'succeeded'
  | 'failed'
  | 'canceled'
  // A run imported from legacy data. It is history and never resumes.
  | 'interrupted_migration';

export type DaGamaComponentStatus =
  'blocked' | 'ready' | 'running' | 'validating' | 'awaiting_approval' | 'succeeded' | 'failed';

export type DaGamaOwnership = 'automated' | 'human_controlled';

export type DaGamaAttemptStatus = 'launch_requested' | 'running' | 'exited';

export type DaGamaRunSource = {
  kind: 'text' | 'file';
  title: string;
  path: string | null;
  bytes: number;
  sha256: string;
};

export type DaGamaRunSourceInput =
  { kind: 'text'; title: string; text: string } | { kind: 'file'; title: string; path: string };

export type DaGamaAttemptState = {
  attemptId: string;
  componentId: DaGamaComponentId;
  instance: number;
  seatId: string;
  attempt: number;
  tmuxName: string;
  /**
   * The agent-side session identifier. Combined with the seat's vendor this is
   * the composite `{agent,id}` Canvas identity — never a key on its own, because
   * Claude and Codex identifiers can collide.
   */
  sessionId: string | null;
  ownership: DaGamaOwnership;
  status: DaGamaAttemptStatus;
  exitCode: number | null;
  startedAt: string | null;
  finishedAt: string | null;
};

export type DaGamaComponentRunState = {
  id: DaGamaComponentId;
  status: DaGamaComponentStatus;
  instance: number;
  /** Taxonomy reason: blocked_by_gate, waiting_for_repair, or a failure reason. */
  reason: string | null;
  message: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  outputs: string[];
  attempt: DaGamaAttemptState | null;
};

export type DaGamaChangedFile = { path: string; status: string };

export type DaGamaChangeRecord = {
  changeRevision: number;
  treeOid: string;
  patchSha256: string;
  patchBytes: number;
  insertions: number;
  deletions: number;
  changedFiles: DaGamaChangedFile[];
  baseSha: string;
};

export type DaGamaGateRecord = {
  componentId: DaGamaComponentId;
  instance: number;
  reason: string;
  message: string;
  /** Empty string and null both mean "undecided"; Go emits `""`, the legacy server emitted `null`. */
  decision: 'approved' | 'rejected' | '' | null;
  changeRevision: number | null;
  openedAt: string;
  decidedAt: string | null;
};

export type DaGamaPublicationRecord = {
  changeRevision: number;
  commitSha: string;
  branch: string;
  remote: string;
  prUrl: string | null;
  prNumber: number | null;
  action: 'created' | 'updated' | 'existing';
  idempotencyKey: string;
  publishedAt: string;
};

export type DaGamaArtifactRecord = {
  artifactId: string;
  kind: string;
  name: string;
  path: string;
  sha256: string;
  bytes: number;
  createdAt: string;
  producer: {
    componentId: DaGamaComponentId;
    instance: number;
    seatId?: string;
    attempt?: number;
  };
};

export type DaGamaRunFailure = { reason: string; message: string };

export type DaGamaRun = {
  schemaVersion: number;
  runId: string;
  projectId: string;
  boardId: string;
  boardRevision: number;
  title: string;
  status: DaGamaRunStatus;
  createdAt: string;
  updatedAt: string;
  finishedAt: string | null;
  source: DaGamaRunSource | null;
  runRoot: string | null;
  branch: string | null;
  baseBranch: string | null;
  baseSha: string | null;
  remoteUrl: string | null;
  components: Record<DaGamaComponentId, DaGamaComponentRunState>;
  artifacts: DaGamaArtifactRecord[];
  change: DaGamaChangeRecord | null;
  gate: DaGamaGateRecord | null;
  publication: DaGamaPublicationRecord | null;
  failure: DaGamaRunFailure | null;
  lastSeq: number;
};

export type DaGamaRunSummary = Pick<
  DaGamaRun,
  'runId' | 'projectId' | 'boardId' | 'title' | 'status' | 'createdAt' | 'updatedAt' | 'finishedAt'
>;

export type DaGamaRunPreview = {
  projectPath: string;
  baseBranch: string;
  baseSha: string;
  defaultBranch: string;
  checkoutBranch: string | null;
  isLinkedWorktree: boolean;
  remoteUrl: string | null;
  runRootParent: string;
};

export type DaGamaPublishChecklistItem = {
  id: string;
  label: string;
  ok: boolean;
  detail: string;
};

export type DaGamaPublishPreflight = {
  ok: boolean;
  changeRevision: number;
  patchSha256: string;
  treeOid: string;
  branch: string;
  baseBranch: string;
  baseSha: string;
  remoteUrl: string | null;
  checklist: DaGamaPublishChecklistItem[];
  draft: boolean;
};

/** Terminal handle returned by a reconnect. Native PTY/WebSocket, never a URL to an unguarded port. */
export type DaGamaTerminalHandle = {
  terminalId: string;
  attemptId: string;
  /** Present when the backend also reports the live terminal's write policy. */
  writable?: boolean;
  reused?: boolean;
};

export type DaGamaBoardLoadError = { file: string; code: string; message: string };

/**
 * The composite Canvas identity of a seat attempt.
 *
 * `agent` comes from the board seat's vendor and `id` from the attempt's
 * session identifier: neither is sufficient alone, which is exactly why
 * CONTRACTS.md forbids keying Canvas state by `id`.
 */
export function attemptSessionIdentity(
  vendor: DaGamaVendor | null | undefined,
  attempt: DaGamaAttemptState | null | undefined,
): CanvasSessionIdentity | null {
  const id = attempt?.sessionId ?? '';
  if (!vendor || id === '') return null;
  return { agent: vendor, id };
}

export type DaGamaSeatComponentIdOf<Id extends DaGamaComponentId> = Id extends DaGamaSeatComponentId
  ? Id
  : never;
