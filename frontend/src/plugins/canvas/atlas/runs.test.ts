import { describe, expect, it } from 'vitest';
import {
  atlasAttempt,
  FROZEN_ATLAS_COMMITTEE_RUN,
  FROZEN_ATLAS_FAILED_RUN,
  FROZEN_ATLAS_HUMAN_CONTROLLED_RUN,
  FROZEN_ATLAS_PARTIAL_RUN,
  FROZEN_ATLAS_PLAIN_FOLDER_RUN,
  FROZEN_ATLAS_PUBLISH_GATE_RUN,
  FROZEN_ATLAS_REFINING_RUN,
  FROZEN_ATLAS_TRIGGER_GATE_RUN,
} from '@/plugins/canvas/atlas/fixtures';
import {
  attemptById,
  attemptControls,
  attemptsOf,
  committeeProgress,
  elapsedLabel,
  gateView,
  isLiveRun,
  isRefineSeat,
  isTerminalRun,
  isWorkerSeat,
  liveAttempts,
  publicationUnavailableReason,
  runBlockedReason,
  showsConfiguration,
  showsSeatTerminal,
  stageControls,
} from '@/plugins/canvas/atlas/runs';
import { attemptSessionIdentity, type AtlasRun } from '@/plugins/canvas/atlas/types';

// Each expectation names the controller guard it mirrors, so a backend change
// that loosens or tightens a transition fails here first.

describe('Atlas attempt controls', () => {
  it('addresses every sibling of a fan-out by id', () => {
    // A component id is no longer an address once a stage is a committee.
    const attempts = attemptsOf(FROZEN_ATLAS_COMMITTEE_RUN, 'plan');
    expect(attempts).toHaveLength(3);
    for (const attempt of attempts) {
      expect(attemptById(FROZEN_ATLAS_COMMITTEE_RUN, attempt.attemptId)).not.toBeNull();
    }
    expect(attemptById(FROZEN_ATLAS_COMMITTEE_RUN, 'no-such-attempt')).toBeNull();
  });

  it('offers takeover only on a running attempt with a provider session', () => {
    expect(attemptControls(FROZEN_ATLAS_COMMITTEE_RUN, 'a-plan-2').canTakeControl).toBe(true);
    // An exited sibling has nothing to take.
    expect(attemptControls(FROZEN_ATLAS_COMMITTEE_RUN, 'a-plan-1').canTakeControl).toBe(false);

    const withoutSession: AtlasRun = {
      ...FROZEN_ATLAS_COMMITTEE_RUN,
      components: {
        ...FROZEN_ATLAS_COMMITTEE_RUN.components,
        plan: {
          ...FROZEN_ATLAS_COMMITTEE_RUN.components.plan,
          attempts: [atlasAttempt({ attemptId: 'a-plan-2', seatId: 'plan-2', session: null })],
        },
      },
    };
    expect(attemptControls(withoutSession, 'a-plan-2').canTakeControl).toBe(false);
  });

  it('offers handback only while an attempt is human controlled and running', () => {
    expect(attemptControls(FROZEN_ATLAS_HUMAN_CONTROLLED_RUN, 'a-build-1').canHandBack).toBe(true);
    expect(attemptControls(FROZEN_ATLAS_HUMAN_CONTROLLED_RUN, 'a-build-1').canTakeControl).toBe(false);
    expect(attemptControls(FROZEN_ATLAS_COMMITTEE_RUN, 'a-plan-2').canHandBack).toBe(false);
  });

  it('allows a compose send only with an open terminal the operator controls', () => {
    expect(attemptControls(FROZEN_ATLAS_HUMAN_CONTROLLED_RUN, 'a-build-1', { connected: true }).canSend).toBe(
      true,
    );
    // The server would allow it, but this client holds no socket.
    expect(
      attemptControls(FROZEN_ATLAS_HUMAN_CONTROLLED_RUN, 'a-build-1', { connected: false }).canSend,
    ).toBe(false);
    // Socket open, but the turn is still automated.
    expect(attemptControls(FROZEN_ATLAS_COMMITTEE_RUN, 'a-plan-2', { connected: true }).canSend).toBe(false);
  });

  it('offers no attempt control once the run is terminal', () => {
    for (const status of ['succeeded', 'canceled', 'interrupted_migration'] as const) {
      const finished: AtlasRun = { ...FROZEN_ATLAS_COMMITTEE_RUN, status };
      const controls = attemptControls(finished, 'a-plan-2', { connected: true });
      expect(controls).toEqual({ canTakeControl: false, canHandBack: false, canSend: false });
    }
  });

  it('reports every live sibling a cancel has to stop', () => {
    expect(liveAttempts(FROZEN_ATLAS_COMMITTEE_RUN)).toHaveLength(2);
    expect(liveAttempts(FROZEN_ATLAS_PARTIAL_RUN)).toHaveLength(0);
  });
});

describe('Atlas stage controls', () => {
  it('offers retry only for a failed committee stage', () => {
    expect(stageControls(FROZEN_ATLAS_FAILED_RUN, 'plan').canRetry).toBe(true);
    expect(stageControls(FROZEN_ATLAS_COMMITTEE_RUN, 'plan').canRetry).toBe(false);
    // A deterministic stage has no committee to retry.
    expect(stageControls(FROZEN_ATLAS_FAILED_RUN, 'verify').canRetry).toBe(false);
    const finished: AtlasRun = { ...FROZEN_ATLAS_FAILED_RUN, status: 'canceled' };
    expect(stageControls(finished, 'plan').canRetry).toBe(false);
  });

  it('offers cancel while the run can still be stopped', () => {
    expect(stageControls(FROZEN_ATLAS_COMMITTEE_RUN, 'plan').canCancel).toBe(true);
    expect(stageControls(FROZEN_ATLAS_PUBLISH_GATE_RUN, 'publish').canCancel).toBe(true);
    const finished: AtlasRun = { ...FROZEN_ATLAS_COMMITTEE_RUN, status: 'succeeded' };
    expect(stageControls(finished, 'plan').canCancel).toBe(false);
  });
});

describe('Atlas committee progress', () => {
  it('counts a fan-out in the terms the operator configured', () => {
    const progress = committeeProgress(FROZEN_ATLAS_COMMITTEE_RUN, 'plan', 3);
    expect(progress.workers).toBe(3);
    expect(progress.finished).toBe(1);
    expect(progress.running).toBe(2);
    expect(progress.refining).toBe(false);
  });

  it('reports the refine turn distinctly from its workers', () => {
    const progress = committeeProgress(FROZEN_ATLAS_REFINING_RUN, 'plan', 2);
    expect(progress.refining).toBe(true);
    expect(progress.finished).toBe(2);
  });

  it('names a partial committee as partial rather than as success', () => {
    // Silently reporting "2 of 3" as done would hide exactly what the fan-out
    // exists to reveal.
    const progress = committeeProgress(FROZEN_ATLAS_PARTIAL_RUN, 'plan', 3);
    expect(progress.partial).toBe(true);
    expect(progress.workers).toBe(3);
  });

  it('separates a worker seat from the refine seat', () => {
    expect(isWorkerSeat('plan', 'plan-1')).toBe(true);
    expect(isWorkerSeat('plan', 'plan-main-refine')).toBe(false);
    expect(isRefineSeat('plan', 'plan-main-refine')).toBe(true);
    expect(isRefineSeat('plan', 'plan-1')).toBe(false);
  });
});

describe('Atlas gates', () => {
  it('reports an undecided publish gate as decidable', () => {
    const gate = gateView(FROZEN_ATLAS_PUBLISH_GATE_RUN, 'publish');
    expect(gate.open).toBe(true);
    expect(gate.decidable).toBe(true);
    expect(gate.staleReason).toBe('');
  });

  it('withholds a gate opened against a superseded revision', () => {
    const stale: AtlasRun = {
      ...FROZEN_ATLAS_PUBLISH_GATE_RUN,
      change: { ...FROZEN_ATLAS_PUBLISH_GATE_RUN.change!, changeRevision: 5 },
    };
    const gate = gateView(stale, 'publish');
    expect(gate.open).toBe(true);
    expect(gate.decidable).toBe(false);
    expect(gate.staleReason).toContain('revision');
  });

  it('treats an empty decision as undecided and a set decision as closed', () => {
    expect(gateView(FROZEN_ATLAS_PUBLISH_GATE_RUN, 'publish').open).toBe(true);
    const decided: AtlasRun = {
      ...FROZEN_ATLAS_PUBLISH_GATE_RUN,
      gate: { ...FROZEN_ATLAS_PUBLISH_GATE_RUN.gate!, decision: 'approved' },
    };
    expect(gateView(decided, 'publish').open).toBe(false);
  });

  it('binds a trigger gate to the stage that is waiting', () => {
    const gate = gateView(FROZEN_ATLAS_TRIGGER_GATE_RUN, 'build');
    expect(gate.open).toBe(true);
    expect(gate.reason).toBe('waiting_for_trigger');
    expect(gateView(FROZEN_ATLAS_TRIGGER_GATE_RUN, 'plan').open).toBe(false);
  });
});

describe('Atlas run presentation', () => {
  it('treats awaiting_approval as live and an import as terminal', () => {
    expect(isLiveRun(FROZEN_ATLAS_PUBLISH_GATE_RUN)).toBe(true);
    expect(isTerminalRun(FROZEN_ATLAS_PUBLISH_GATE_RUN)).toBe(false);
    // An imported run is history and never resumes.
    const imported: AtlasRun = { ...FROZEN_ATLAS_COMMITTEE_RUN, status: 'interrupted_migration' };
    expect(isTerminalRun(imported)).toBe(true);
    expect(isLiveRun(imported)).toBe(false);
  });

  it('keeps configuration editable only before a stage starts', () => {
    expect(showsConfiguration(null)).toBe(true);
    expect(showsConfiguration(FROZEN_ATLAS_COMMITTEE_RUN.components.build)).toBe(true);
    expect(showsConfiguration(FROZEN_ATLAS_COMMITTEE_RUN.components.plan)).toBe(false);
  });

  it('shows a seat terminal only once the stage has an attempt', () => {
    expect(showsSeatTerminal(FROZEN_ATLAS_COMMITTEE_RUN.components.plan)).toBe(true);
    expect(showsSeatTerminal(FROZEN_ATLAS_COMMITTEE_RUN.components.review)).toBe(false);
  });

  it('says why a plain folder cannot publish instead of hiding the gate', () => {
    expect(publicationUnavailableReason(FROZEN_ATLAS_PLAIN_FOLDER_RUN)).toContain('no git remote');
    expect(publicationUnavailableReason(FROZEN_ATLAS_PUBLISH_GATE_RUN)).toBe('');
  });

  it('composes a session identity only from both halves', () => {
    const attempt = attemptsOf(FROZEN_ATLAS_COMMITTEE_RUN, 'plan')[0];
    expect(attemptSessionIdentity('claude', attempt)).toEqual({
      agent: 'claude',
      id: attempt.session!.id,
    });
    expect(attemptSessionIdentity('claude', atlasAttempt({ session: null }))).toBeNull();
    expect(
      attemptSessionIdentity('claude', atlasAttempt({ session: { agent: 'claude', id: '' } })),
    ).toBeNull();
  });

  it('formats elapsed time and refuses impossible ranges', () => {
    expect(elapsedLabel('2026-08-09T07:00:00Z', '2026-08-09T07:00:42Z', 0)).toBe('42s');
    expect(elapsedLabel('2026-08-09T07:00:00Z', '2026-08-09T07:02:05Z', 0)).toBe('2m05s');
    expect(elapsedLabel(null, null, 0)).toBeNull();
    expect(elapsedLabel('2026-08-09T07:00:00Z', '2026-08-09T06:00:00Z', 0)).toBeNull();
  });

  it('explains exactly why a run cannot start', () => {
    const base = { hasProject: true, graphReason: '', saveState: 'saved' as const, runs: [] };
    expect(runBlockedReason({ ...base, hasProject: false })).toContain('project');
    // A custom graph reports the product's own explanation, not a generic one.
    expect(runBlockedReason({ ...base, graphReason: 'Custom graph runtime coming' })).toContain(
      'Custom graph',
    );
    expect(runBlockedReason({ ...base, saveState: 'conflict' })).toContain('conflict');
    expect(runBlockedReason({ ...base, saveState: 'saving' })).toContain('save');
    expect(
      runBlockedReason({
        ...base,
        runs: [{ ...FROZEN_ATLAS_COMMITTEE_RUN, status: 'running' }],
      }),
    ).toContain('already has a run');
    // A finished run never blocks the next one.
    expect(
      runBlockedReason({
        ...base,
        runs: [{ ...FROZEN_ATLAS_COMMITTEE_RUN, status: 'succeeded' }],
      }),
    ).toBeNull();
    expect(runBlockedReason(base)).toBeNull();
  });
});
