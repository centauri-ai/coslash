// Atlas run presentation and control gating.
//
// Every predicate here mirrors the guard the controller applies in
// `collector/internal/plugins/canvas/atlas/guards.go`:
//
//   CanRetry     — run not terminal, committee stage, status failed
//   CanTakeover  — running attempt with a known provider session
//   CanHandback  — live human-controlled attempt
//   CanCancel    — run not finished
//   CanDecideGate— an undecided gate whose revision is not stale
//   CanStartRun  — no other live run in the project
//
// Atlas differs from DaGama in one structural way: a stage is a committee, so
// takeover and handback address an ATTEMPT rather than a component. Keeping
// these pure and separately tested is what makes "UI controls never claim a
// transition the backend rejects" checkable.

import type {
  AtlasAttemptState,
  AtlasComponentRunState,
  AtlasComponentStatus,
  AtlasRun,
  AtlasRunStatus,
  AtlasRunSummary,
} from '@/plugins/canvas/atlas/types';
import { ATLAS_COMPONENT_IDS, hasSeat } from '@/plugins/canvas/atlas/vocabulary';

export const ATLAS_RUN_STATUS_LABEL: Record<AtlasRunStatus, string> = {
  preparing: 'Preparing',
  running: 'Running',
  awaiting_approval: 'Awaiting approval',
  succeeded: 'Succeeded',
  failed: 'Failed',
  canceled: 'Canceled',
  interrupted_migration: 'Imported (interrupted)',
};

export const ATLAS_COMPONENT_STATUS_LABEL: Record<AtlasComponentStatus, string> = {
  blocked: 'Blocked',
  ready: 'Ready',
  running: 'Running',
  validating: 'Validating',
  awaiting_approval: 'Awaiting approval',
  succeeded: 'Done',
  failed: 'Failed',
};

/** Statuses after which the controller refuses every further transition. */
const TERMINAL_STATUSES: readonly AtlasRunStatus[] = ['succeeded', 'canceled', 'interrupted_migration'];

export function isTerminalRun(run: AtlasRun | null): boolean {
  return run != null && TERMINAL_STATUSES.includes(run.status);
}

/** A run the server can still advance, so the board keeps mirroring it. */
export function isLiveRun(run: AtlasRun | null): boolean {
  // `awaiting_approval` is live on purpose: a gate decided in another tab, or a
  // slow publish, still has to reach this board.
  return (
    run != null &&
    (run.status === 'preparing' || run.status === 'running' || run.status === 'awaiting_approval')
  );
}

export function componentOf(run: AtlasRun | null, id: string): AtlasComponentRunState | null {
  return run?.components?.[id] ?? null;
}

/** Every attempt of a stage's current instance, refine turn included. */
export function attemptsOf(run: AtlasRun | null, componentId: string): AtlasAttemptState[] {
  const component = componentOf(run, componentId);
  if (component == null) return [];
  if (component.attempts.length > 0) return component.attempts;
  return component.attempt == null ? [] : [component.attempt];
}

/** Find one attempt anywhere in the run. A fan-out makes ids the only address. */
export function attemptById(run: AtlasRun | null, attemptId: string): AtlasAttemptState | null {
  if (run == null) return null;
  for (const component of Object.values(run.components ?? {})) {
    for (const attempt of component.attempts ?? []) {
      if (attempt.attemptId === attemptId) return attempt;
    }
    if (component.attempt?.attemptId === attemptId) return component.attempt;
  }
  return null;
}

export function isLiveAttempt(attempt: AtlasAttemptState | null | undefined): boolean {
  return attempt != null && (attempt.status === 'launch_requested' || attempt.status === 'running');
}

/** Every attempt the runtime still owns a process for. */
export function liveAttempts(run: AtlasRun | null): AtlasAttemptState[] {
  if (run == null) return [];
  const live: AtlasAttemptState[] = [];
  for (const component of Object.values(run.components ?? {})) {
    for (const attempt of component.attempts ?? []) {
      if (isLiveAttempt(attempt)) live.push(attempt);
    }
  }
  return live;
}

export type AtlasAttemptControls = {
  canTakeControl: boolean;
  canHandBack: boolean;
  /** True when a compose box may write into this attempt's terminal. */
  canSend: boolean;
};

/**
 * Which controls the backend would accept for one attempt.
 *
 * `connected` reports whether this client actually holds an open terminal;
 * without one there is nothing to send into, so `canSend` stays false even when
 * the server would allow the write.
 */
export function attemptControls(
  run: AtlasRun | null,
  attemptId: string,
  options: { connected?: boolean } = {},
): AtlasAttemptControls {
  const closed = { canTakeControl: false, canHandBack: false, canSend: false };
  if (run == null || isTerminalRun(run)) return closed;
  const attempt = attemptById(run, attemptId);
  if (attempt == null) return closed;

  const human = attempt.ownership === 'human_controlled';
  const running = attempt.status === 'running';
  const hasSession = (attempt.session?.id ?? '') !== '';

  return {
    // Takeover resumes the provider session, so it needs one to resume.
    canTakeControl: running && hasSession && !human,
    canHandBack: running && human,
    canSend: Boolean(options.connected) && human && isLiveAttempt(attempt),
  };
}

export type AtlasStageControls = {
  canRetry: boolean;
  canCancel: boolean;
};

/**
 * Which stage-level controls the backend would accept.
 *
 * A committee retries whole: retrying one sibling would leave the refine turn
 * reconciling drafts from two different instances.
 */
export function stageControls(run: AtlasRun | null, componentId: string): AtlasStageControls {
  if (run == null) return { canRetry: false, canCancel: false };
  const terminal = isTerminalRun(run) || run.status === 'failed';
  const component = componentOf(run, componentId);
  return {
    canRetry: !isTerminalRun(run) && hasSeat(componentId) && component?.status === 'failed',
    // Cancel is a run-level operation, offered wherever the run can still stop.
    canCancel: !terminal,
  };
}

export type AtlasGateView = {
  open: boolean;
  decidable: boolean;
  reason: string;
  message: string;
  staleReason: string;
};

/**
 * The gate as one component should present it.
 *
 * The controller refuses a decision whose recorded change revision no longer
 * matches the run's. Surfacing that as an explicit, non-decidable state is the
 * difference between seeing why approval is withheld and watching a button fail.
 */
export function gateView(run: AtlasRun | null, componentId: string): AtlasGateView {
  const closed: AtlasGateView = {
    open: false,
    decidable: false,
    reason: '',
    message: '',
    staleReason: '',
  };
  const gate = run?.gate ?? null;
  if (run == null || gate == null || gate.componentId !== componentId) return closed;
  if (gate.decision != null && gate.decision !== '') return closed;

  const current = run.change?.changeRevision ?? null;
  const stale = gate.changeRevision != null && current != null && gate.changeRevision !== current;
  return {
    open: true,
    decidable: !stale && !isTerminalRun(run),
    reason: gate.reason,
    message: gate.message,
    staleReason: stale
      ? `This gate was opened for revision ${gate.changeRevision} and the run is now at revision ${current}. Reload the run before deciding.`
      : '',
  };
}

/** A stage's configuration is editable only while it has not started. */
export function showsConfiguration(component: AtlasComponentRunState | null): boolean {
  return component == null || component.status === 'blocked';
}

/** Whether a seat terminal surface should be shown at all. */
export function showsSeatTerminal(component: AtlasComponentRunState | null): boolean {
  if (component == null || attemptCount(component) === 0) return false;
  return (
    component.status === 'ready' ||
    component.status === 'running' ||
    component.status === 'validating' ||
    component.status === 'succeeded' ||
    component.status === 'failed'
  );
}

function attemptCount(component: AtlasComponentRunState): number {
  if (component.attempts.length > 0) return component.attempts.length;
  return component.attempt == null ? 0 : 1;
}

/**
 * How a committee is doing, in the terms the operator asked for.
 *
 * A committee that silently reports "2 of 3" as success would hide exactly the
 * thing the fan-out exists to reveal, so a partial result is named as partial.
 */
export type AtlasCommitteeProgress = {
  workers: number;
  finished: number;
  failed: number;
  running: number;
  /** True once some sibling failed but the stage continued. */
  partial: boolean;
  refining: boolean;
};

export function committeeProgress(
  run: AtlasRun | null,
  componentId: string,
  configuredWorkers: number,
): AtlasCommitteeProgress {
  const attempts = attemptsOf(run, componentId);
  const workerAttempts = attempts.filter((attempt) => !isRefineSeat(componentId, attempt.seatId));
  const refine = attempts.find((attempt) => isRefineSeat(componentId, attempt.seatId)) ?? null;

  const finished = workerAttempts.filter((attempt) => attempt.status === 'exited').length;
  const running = workerAttempts.filter(isLiveAttempt).length;
  const component = componentOf(run, componentId);
  // A sibling that produced nothing is a failure the operator has to see, and
  // the run log records it as an exited attempt with no promoted output.
  const failed = Math.max(0, workerAttempts.length - finished - running);

  return {
    workers: Math.max(configuredWorkers, workerAttempts.length),
    finished,
    failed,
    running,
    partial: component?.status === 'succeeded' && workerAttempts.length < configuredWorkers,
    refining: refine != null && isLiveAttempt(refine),
  };
}

/** The refine turn's run-log seat, matching the backend's naming exactly. */
export function refineSeatId(componentId: string): string {
  return `${componentId}-main-refine`;
}

export function isRefineSeat(componentId: string, seatId: string): boolean {
  return seatId === refineSeatId(componentId);
}

export function isWorkerSeat(componentId: string, seatId: string): boolean {
  const suffix = seatId.startsWith(`${componentId}-`) ? seatId.slice(componentId.length + 1) : '';
  return suffix !== '' && /^[0-9]+$/.test(suffix);
}

/** Human-readable elapsed time for an attempt, or null when it never started. */
export function elapsedLabel(
  startedAt: string | null | undefined,
  finishedAt: string | null | undefined,
  now: number,
): string | null {
  if (!startedAt) return null;
  const start = Date.parse(startedAt);
  const end = finishedAt ? Date.parse(finishedAt) : now;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return null;
  const seconds = Math.floor((end - start) / 1000);
  const minutes = Math.floor(seconds / 60);
  return minutes > 0 ? `${minutes}m${(seconds % 60).toString().padStart(2, '0')}s` : `${seconds}s`;
}

/**
 * Whether starting a run is allowed, and why not when it is not.
 *
 * One live run per project is a product rule, not a resource limit: two runs
 * against the same board would race for the same publication target.
 */
export function runBlockedReason(input: {
  hasProject: boolean;
  graphReason: string;
  saveState: 'draft' | 'loading' | 'saving' | 'saved' | 'error' | 'conflict';
  runs: readonly AtlasRunSummary[];
}): string | null {
  if (!input.hasProject) return 'Choose a project before starting a run';
  if (input.graphReason !== '') return input.graphReason;
  if (input.saveState === 'conflict') return 'Resolve the board conflict before running it';
  if (input.saveState === 'error') return 'The board could not be saved, so a run cannot pin it';
  if (input.saveState === 'saving' || input.saveState === 'loading')
    return 'Wait for the board to save before running it';
  for (const summary of input.runs) {
    if (!TERMINAL_STATUSES.includes(summary.status) && summary.status !== 'failed') {
      return 'This project already has a run in progress';
    }
  }
  return null;
}

/** Publication is unavailable for a plain folder, and the UI must say why. */
export function publicationUnavailableReason(run: AtlasRun | null): string {
  if (run == null) return '';
  if ((run.remoteUrl ?? '') === '') {
    return 'This project has no git remote, so this run can be approved but not published';
  }
  return '';
}

// ---------------------------------------------------------------------------
// Stages the graph does not own
// ---------------------------------------------------------------------------

/**
 * The run stages no board component owns.
 *
 * Intake, Verify, and Publish are deterministic: the controller runs them, but
 * the v2 graph has no seat to configure and so no node to render them on. They
 * are surfaced on the run rail instead. Dropping them would hide the publish
 * gate — the one decision the operator is required to make.
 */
export function offBoardStages(
  run: AtlasRun | null,
  boardComponentIds: Iterable<string>,
): AtlasComponentRunState[] {
  if (run == null) return [];
  const owned = new Set(boardComponentIds);
  const order = (id: string) => {
    const index = (ATLAS_COMPONENT_IDS as readonly string[]).indexOf(id);
    return index === -1 ? ATLAS_COMPONENT_IDS.length : index;
  };
  return Object.values(run.components ?? {})
    .filter((component) => !owned.has(component.id))
    .sort((a, b) => order(a.id) - order(b.id) || a.id.localeCompare(b.id));
}

/**
 * The open gate that belongs to no board component, if there is one.
 *
 * A gate is rendered exactly once: on its own seat when the graph has one, and
 * on the run rail when it does not. Rendering it in both places would offer the
 * same irreversible decision twice.
 */
export function offBoardGate(run: AtlasRun | null, boardComponentIds: Iterable<string>): AtlasGateView {
  const componentId = run?.gate?.componentId ?? null;
  if (componentId == null) return gateView(run, '');
  const owned = new Set(boardComponentIds);
  return owned.has(componentId) ? gateView(run, '') : gateView(run, componentId);
}
