// Run presentation and control gating.
//
// Every predicate here is a deliberate mirror of the guard the controller
// applies in `collector/internal/plugins/canvas/dagama/`:
//
//   Retry     controller.go  — run not terminal, seat component, status failed
//   Takeover  takeover.go    — attempt running with a known provider session
//   Handback  takeover.go    — live human-controlled attempt
//   Cancel    cancel.go      — run not terminal (a run-level operation)
//   Gate      review_outcome.go — an undecided gate whose revision is not stale
//
// Keeping them here, pure and separately tested, is what makes the exit gate
// "UI controls never claim a transition the backend rejects" checkable: the
// board renders a control only when the matching predicate holds, so a disabled
// button and a rejected request cannot disagree.

import type {
  DaGamaAttemptState,
  DaGamaComponentRunState,
  DaGamaComponentStatus,
  DaGamaRun,
  DaGamaRunStatus,
} from '@/plugins/canvas/dagama/types';
import { hasSeat, type DaGamaComponentId } from '@/plugins/canvas/dagama/vocabulary';

export const DAGAMA_RUN_STATUS_LABEL: Record<DaGamaRunStatus, string> = {
  preparing: 'Preparing',
  running: 'Running',
  awaiting_approval: 'Awaiting approval',
  succeeded: 'Succeeded',
  failed: 'Failed',
  canceled: 'Canceled',
};

export const DAGAMA_COMPONENT_STATUS_LABEL: Record<DaGamaComponentStatus, string> = {
  blocked: 'Blocked',
  ready: 'Ready',
  running: 'Running',
  validating: 'Validating',
  awaiting_approval: 'Awaiting approval',
  succeeded: 'Done',
  failed: 'Failed',
};

/** Statuses after which the controller refuses every further transition. */
export function isTerminalRun(run: DaGamaRun | null): boolean {
  return run != null && (run.status === 'succeeded' || run.status === 'failed' || run.status === 'canceled');
}

/** A run the server can still advance, so the board keeps mirroring it. */
export function isLiveRun(run: DaGamaRun | null): boolean {
  // `awaiting_approval` is live on purpose: the publish and repair gates must
  // keep mirroring the server so a decision taken in another tab, or a slow
  // publish, still lands here.
  return (
    run != null &&
    (run.status === 'preparing' || run.status === 'running' || run.status === 'awaiting_approval')
  );
}

/** An attempt the runtime still owns a process for. */
export function isLiveAttempt(attempt: DaGamaAttemptState | null | undefined): boolean {
  return attempt != null && (attempt.status === 'launch_requested' || attempt.status === 'running');
}

export function isHumanControlled(attempt: DaGamaAttemptState | null | undefined): boolean {
  return attempt?.ownership === 'human_controlled';
}

export function componentOf(run: DaGamaRun | null, id: DaGamaComponentId): DaGamaComponentRunState | null {
  return run?.components?.[id] ?? null;
}

export type DaGamaSeatControls = {
  canRetry: boolean;
  canCancel: boolean;
  canTakeControl: boolean;
  canHandBack: boolean;
  /** True when a compose box may write into the live terminal. */
  canSend: boolean;
};

const NO_CONTROLS: DaGamaSeatControls = {
  canRetry: false,
  canCancel: false,
  canTakeControl: false,
  canHandBack: false,
  canSend: false,
};

/**
 * Which seat controls the backend would currently accept.
 *
 * `connected` reports whether this client actually holds an open terminal for
 * the attempt; without one there is nothing to send into, so `canSend` stays
 * false even when the server would allow a write.
 */
export function seatControls(
  run: DaGamaRun | null,
  componentId: DaGamaComponentId,
  options: { connected?: boolean } = {},
): DaGamaSeatControls {
  if (run == null || !hasSeat(componentId)) return NO_CONTROLS;
  const component = componentOf(run, componentId);
  if (component == null) return NO_CONTROLS;
  const attempt = component.attempt;
  const terminal = isTerminalRun(run);
  const live = isLiveAttempt(attempt);
  const human = isHumanControlled(attempt);

  // Cancel is a run-level operation in the controller, so it is offered while
  // the run itself can still be stopped — including when a seat is `ready` but
  // its attempt has not appeared yet, which is exactly the stranded case an
  // operator needs a way out of.
  const canCancel =
    !terminal &&
    (live ||
      component.status === 'ready' ||
      component.status === 'running' ||
      component.status === 'validating');

  return {
    canRetry: !terminal && component.status === 'failed' && !human,
    canCancel,
    // Takeover resumes the provider session, so it needs a running attempt that
    // has already reported one.
    canTakeControl:
      !terminal &&
      attempt != null &&
      !human &&
      attempt.status === 'running' &&
      (attempt.sessionId ?? '') !== '',
    canHandBack: !terminal && human && attempt != null && attempt.status === 'running',
    canSend: Boolean(options.connected) && human && live,
  };
}

export type DaGamaGateView = {
  /** An undecided gate is open on this component. */
  open: boolean;
  /** The gate is decidable: not stale against the current change revision. */
  decidable: boolean;
  reason: string;
  message: string;
  staleReason: string;
};

/**
 * The gate as this component should present it.
 *
 * The controller refuses a decision whose recorded change revision no longer
 * matches the run's current one. Surfacing that as an explicit, non-decidable
 * state is the difference between an operator seeing why approval is withheld
 * and an operator watching a button fail.
 */
export function gateView(run: DaGamaRun | null, componentId: DaGamaComponentId): DaGamaGateView {
  const gate = run?.gate ?? null;
  const closed: DaGamaGateView = {
    open: false,
    decidable: false,
    reason: '',
    message: '',
    staleReason: '',
  };
  if (run == null || gate == null) return closed;
  if (gate.componentId !== componentId) return closed;
  // Go emits `""` for an undecided gate; the legacy server emitted `null`.
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

/** True when the publish gate should render its preflight and decision controls. */
export function showsPublishGate(run: DaGamaRun | null, component: DaGamaComponentRunState | null): boolean {
  if (run == null || component == null) return false;
  return component.status === 'awaiting_approval' || run.publication != null;
}

/** True when a repair-exhaustion gate is waiting on this component. */
export function showsRepairGate(component: DaGamaComponentRunState | null): boolean {
  return (
    component != null && component.status === 'awaiting_approval' && component.reason === 'waiting_for_repair'
  );
}

/** A component's configuration is editable only while it has not started. */
export function showsConfiguration(component: DaGamaComponentRunState | null): boolean {
  return component == null || component.status === 'blocked';
}

/** Whether the seat terminal surface should be shown at all. */
export function showsSeatTerminal(component: DaGamaComponentRunState | null): boolean {
  if (component == null || component.attempt == null) return false;
  return (
    component.status === 'ready' ||
    component.status === 'running' ||
    component.status === 'validating' ||
    component.status === 'succeeded' ||
    component.status === 'failed'
  );
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

/** Whether starting a run is currently allowed, and why not when it is not. */
export function runBlockedReason(input: {
  hasProject: boolean;
  saveState: 'draft' | 'loading' | 'saving' | 'saved' | 'error' | 'conflict';
}): string | null {
  if (!input.hasProject) return 'Choose a project before starting a run';
  if (input.saveState === 'conflict') return 'Resolve the board conflict before running it';
  if (input.saveState === 'error') return 'The board could not be saved, so a run cannot pin it';
  if (input.saveState === 'saving' || input.saveState === 'loading')
    return 'Wait for the board to save before running it';
  return null;
}
