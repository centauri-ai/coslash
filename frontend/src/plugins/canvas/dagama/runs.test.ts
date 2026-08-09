import { describe, expect, it } from 'vitest';
import {
  FROZEN_DAGAMA_FAILED_RUN,
  FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN,
  FROZEN_DAGAMA_PUBLISH_GATE_RUN,
  FROZEN_DAGAMA_REPAIR_GATE_RUN,
  FROZEN_DAGAMA_RUNNING_RUN,
} from '@/plugins/canvas/dagama/fixtures';
import {
  elapsedLabel,
  gateView,
  isLiveRun,
  isTerminalRun,
  runBlockedReason,
  seatControls,
  showsConfiguration,
  showsSeatTerminal,
} from '@/plugins/canvas/dagama/runs';
import { attemptSessionIdentity } from '@/plugins/canvas/dagama/types';
import type { DaGamaRun } from '@/plugins/canvas/dagama/types';

// Each expectation below names the controller guard it mirrors, so a backend
// change that loosens or tightens a transition fails here first.

describe('DaGama run control gating', () => {
  it('offers takeover only for a running attempt with a provider session', () => {
    expect(seatControls(FROZEN_DAGAMA_RUNNING_RUN, 'build').canTakeControl).toBe(true);

    const withoutSession: DaGamaRun = {
      ...FROZEN_DAGAMA_RUNNING_RUN,
      components: {
        ...FROZEN_DAGAMA_RUNNING_RUN.components,
        build: {
          ...FROZEN_DAGAMA_RUNNING_RUN.components.build,
          attempt: { ...FROZEN_DAGAMA_RUNNING_RUN.components.build.attempt!, sessionId: '' },
        },
      },
    };
    expect(seatControls(withoutSession, 'build').canTakeControl).toBe(false);

    const launching: DaGamaRun = {
      ...FROZEN_DAGAMA_RUNNING_RUN,
      components: {
        ...FROZEN_DAGAMA_RUNNING_RUN.components,
        build: {
          ...FROZEN_DAGAMA_RUNNING_RUN.components.build,
          attempt: {
            ...FROZEN_DAGAMA_RUNNING_RUN.components.build.attempt!,
            status: 'launch_requested',
          },
        },
      },
    };
    expect(seatControls(launching, 'build').canTakeControl).toBe(false);
  });

  it('offers handback only while the attempt is human-controlled and running', () => {
    expect(seatControls(FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN, 'build').canHandBack).toBe(true);
    expect(seatControls(FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN, 'build').canTakeControl).toBe(false);
    expect(seatControls(FROZEN_DAGAMA_RUNNING_RUN, 'build').canHandBack).toBe(false);
  });

  it('offers retry only for a failed seat on a non-terminal run', () => {
    expect(seatControls(FROZEN_DAGAMA_FAILED_RUN, 'build').canRetry).toBe(true);
    expect(seatControls(FROZEN_DAGAMA_RUNNING_RUN, 'build').canRetry).toBe(false);

    const finished: DaGamaRun = { ...FROZEN_DAGAMA_FAILED_RUN, status: 'failed' };
    expect(seatControls(finished, 'build').canRetry).toBe(false);
  });

  it('never offers a control on a stage that runs no model', () => {
    const controls = seatControls(FROZEN_DAGAMA_RUNNING_RUN, 'verify');
    expect(controls).toEqual({
      canRetry: false,
      canCancel: false,
      canTakeControl: false,
      canHandBack: false,
      canSend: false,
    });
  });

  it('offers cancel while the run can still be stopped, including a stranded ready seat', () => {
    expect(seatControls(FROZEN_DAGAMA_RUNNING_RUN, 'build').canCancel).toBe(true);

    const stranded: DaGamaRun = {
      ...FROZEN_DAGAMA_RUNNING_RUN,
      components: {
        ...FROZEN_DAGAMA_RUNNING_RUN.components,
        build: { ...FROZEN_DAGAMA_RUNNING_RUN.components.build, status: 'ready', attempt: null },
      },
    };
    expect(seatControls(stranded, 'build').canCancel).toBe(true);

    const done: DaGamaRun = { ...FROZEN_DAGAMA_RUNNING_RUN, status: 'succeeded' };
    expect(seatControls(done, 'build').canCancel).toBe(false);
  });

  it('allows a compose send only with an open terminal the operator controls', () => {
    expect(seatControls(FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN, 'build', { connected: true }).canSend).toBe(true);
    // Server would allow it, but this client holds no socket.
    expect(seatControls(FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN, 'build', { connected: false }).canSend).toBe(
      false,
    );
    // Socket is open, but the turn is still automated.
    expect(seatControls(FROZEN_DAGAMA_RUNNING_RUN, 'build', { connected: true }).canSend).toBe(false);
  });
});

describe('DaGama gate presentation', () => {
  it('reports an undecided publish gate as decidable', () => {
    const gate = gateView(FROZEN_DAGAMA_PUBLISH_GATE_RUN, 'publish');
    expect(gate.open).toBe(true);
    expect(gate.decidable).toBe(true);
    expect(gate.staleReason).toBe('');
  });

  it('reports a gate opened for an older revision as open but not decidable', () => {
    const stale: DaGamaRun = {
      ...FROZEN_DAGAMA_PUBLISH_GATE_RUN,
      change: { ...FROZEN_DAGAMA_PUBLISH_GATE_RUN.change!, changeRevision: 3 },
    };
    const gate = gateView(stale, 'publish');
    expect(gate.open).toBe(true);
    expect(gate.decidable).toBe(false);
    expect(gate.staleReason).toContain('revision');
  });

  it('treats an empty decision as undecided and a set decision as closed', () => {
    expect(gateView(FROZEN_DAGAMA_PUBLISH_GATE_RUN, 'publish').open).toBe(true);
    const decided: DaGamaRun = {
      ...FROZEN_DAGAMA_PUBLISH_GATE_RUN,
      gate: { ...FROZEN_DAGAMA_PUBLISH_GATE_RUN.gate!, decision: 'approved' },
    };
    expect(gateView(decided, 'publish').open).toBe(false);
  });

  it('binds a gate to its own component only', () => {
    expect(gateView(FROZEN_DAGAMA_REPAIR_GATE_RUN, 'verify').reason).toBe('waiting_for_repair');
    expect(gateView(FROZEN_DAGAMA_REPAIR_GATE_RUN, 'publish').open).toBe(false);
  });
});

describe('DaGama run presentation', () => {
  it('treats awaiting_approval as live so a gate keeps mirroring the server', () => {
    expect(isLiveRun(FROZEN_DAGAMA_PUBLISH_GATE_RUN)).toBe(true);
    expect(isTerminalRun(FROZEN_DAGAMA_PUBLISH_GATE_RUN)).toBe(false);
    expect(isTerminalRun({ ...FROZEN_DAGAMA_RUNNING_RUN, status: 'canceled' })).toBe(true);
    expect(isLiveRun(null)).toBe(false);
  });

  it('keeps configuration editable only before a stage starts', () => {
    expect(showsConfiguration(null)).toBe(true);
    expect(showsConfiguration(FROZEN_DAGAMA_RUNNING_RUN.components.verify)).toBe(true);
    expect(showsConfiguration(FROZEN_DAGAMA_RUNNING_RUN.components.build)).toBe(false);
  });

  it('shows a seat terminal only once the stage has an attempt', () => {
    expect(showsSeatTerminal(FROZEN_DAGAMA_RUNNING_RUN.components.build)).toBe(true);
    expect(showsSeatTerminal(FROZEN_DAGAMA_RUNNING_RUN.components.review)).toBe(false);
  });

  it('formats elapsed time and refuses impossible ranges', () => {
    const start = '2026-08-09T05:00:00Z';
    expect(elapsedLabel(start, '2026-08-09T05:00:42Z', 0)).toBe('42s');
    expect(elapsedLabel(start, '2026-08-09T05:02:05Z', 0)).toBe('2m05s');
    expect(elapsedLabel(null, null, Date.parse(start))).toBeNull();
    expect(elapsedLabel(start, '2026-08-09T04:00:00Z', 0)).toBeNull();
  });

  it('explains exactly why a run cannot start', () => {
    expect(runBlockedReason({ hasProject: false, saveState: 'saved' })).toContain('project');
    expect(runBlockedReason({ hasProject: true, saveState: 'conflict' })).toContain('conflict');
    expect(runBlockedReason({ hasProject: true, saveState: 'saving' })).toContain('save');
    expect(runBlockedReason({ hasProject: true, saveState: 'saved' })).toBeNull();
  });
});

// The collector encodes an absent string as "" where the legacy dev server
// encoded null. Every consumer must read both as "absent", or a seat whose
// provider session has not been reported yet would look ready to take over.
describe('collector encoding conventions', () => {
  const withSession = (sessionId: string | null): DaGamaRun => ({
    ...FROZEN_DAGAMA_RUNNING_RUN,
    components: {
      ...FROZEN_DAGAMA_RUNNING_RUN.components,
      build: {
        ...FROZEN_DAGAMA_RUNNING_RUN.components.build,
        attempt: { ...FROZEN_DAGAMA_RUNNING_RUN.components.build.attempt!, sessionId },
      },
    },
  });

  it('treats an empty session id the same as a missing one', () => {
    expect(seatControls(withSession(''), 'build').canTakeControl).toBe(false);
    expect(seatControls(withSession(null), 'build').canTakeControl).toBe(false);
    expect(seatControls(withSession('0f9a4d1e-2b3c-4d5e-8f60-112233445566'), 'build').canTakeControl).toBe(
      true,
    );
  });

  it('refuses to compose a session identity without both halves', () => {
    const attempt = FROZEN_DAGAMA_RUNNING_RUN.components.build.attempt!;
    expect(attemptSessionIdentity('claude', attempt)).toEqual({
      agent: 'claude',
      id: attempt.sessionId,
    });
    // An id alone is never a Canvas identity: Claude and Codex ids can collide.
    expect(attemptSessionIdentity(null, attempt)).toBeNull();
    expect(attemptSessionIdentity('claude', { ...attempt, sessionId: '' })).toBeNull();
  });

  it('treats an empty run root or branch as absent rather than as a value', () => {
    const bare: DaGamaRun = {
      ...FROZEN_DAGAMA_RUNNING_RUN,
      status: 'preparing',
      runRoot: '',
      branch: '',
      baseSha: '',
      remoteUrl: '',
    };
    // A run that has not been prepared yet is still live and still mirrored.
    expect(isLiveRun(bare)).toBe(true);
    expect(isTerminalRun(bare)).toBe(false);
  });
});
