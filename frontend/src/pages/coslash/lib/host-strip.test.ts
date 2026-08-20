import { describe, expect, it } from 'vitest';
import { SortKey, sortSessions } from '@/pages/coslash/components/SessionSortDropdownMenu';
import {
  firstTimeSshHint,
  formatTestConnectionResult,
  hostStripModel,
  hostStripVisible,
  remoteConfigured,
  remoteLaunchGate,
  remoteMachine,
} from '@/pages/coslash/lib/host-strip';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { formatRemoteDiagnosticsFacts } from '@/pages/coslash/lib/remote-diagnostics';
import {
  boardStatusKey,
  displayStatusLabel,
  LOCAL_SOURCE_ID,
  sessionsForAggregates,
  type Session,
} from '@/pages/coslash/lib/session';

function machine(overrides: Partial<MachineFact> = {}): MachineFact {
  return {
    sourceId: 'r_0123456789abcdef',
    label: 'gpu-server',
    state: 'ok',
    complete: true,
    ...overrides,
  };
}

function session(overrides: Partial<Session> & Pick<Session, 'id'>): Session {
  return {
    sourceId: LOCAL_SOURCE_ID,
    sourceLabel: 'This Mac',
    eligibleForAggregates: true,
    displayStale: false,
    agent: 'codex',
    name: null,
    summary: null,
    status: 'busy',
    cwd: '/tmp',
    branch: null,
    repo: null,
    repoLocalOnly: false,
    files: 0,
    durationMs: 1,
    cost: 1,
    unpricedModels: [],
    mtime: 200,
    entrypoint: null,
    model: null,
    contextTokens: 0,
    contextWindow: 0,
    turns: 0,
    toolUses: 0,
    errors: 0,
    tokens: {},
    firstPrompt: null,
    commands: [],
    fileEdits: [],
    commits: [],
    todos: [],
    prs: 0,
    declaredGoal: null,
    lastEditAt: 0,
    synthesis: null,
    synthesisPending: false,
    subagents: [],
    digest: [],
    git: null,
    compactions: 0,
    ...overrides,
  };
}

describe('host strip', () => {
  it('hides for healthy and disabled monitoring', () => {
    expect(hostStripVisible(machine({ state: 'ok' }))).toBe(false);
    expect(hostStripVisible(machine({ state: 'disabled' }))).toBe(false);
    expect(hostStripVisible(null)).toBe(false);
  });

  it('maps every unhealthy state to copy, role, and actions', () => {
    const cases: Array<{
      state: MachineFact['state'];
      reason?: MachineFact['reason'];
      role: 'status' | 'alert';
      includes: string;
      actions: string[];
    }> = [
      { state: 'connecting', role: 'status', includes: 'connecting…', actions: [] },
      {
        state: 'connecting',
        reason: 'broader_history',
        role: 'status',
        includes: 'refreshing remote history',
        actions: ['retry'],
      },
      {
        state: 'limited',
        reason: 'history_truncated',
        role: 'alert',
        includes: 'too large',
        actions: ['retry', 'diagnostics'],
      },
      {
        state: 'limited',
        reason: 'refresh_timeout',
        role: 'alert',
        includes: 'took too long',
        actions: ['retry', 'diagnostics'],
      },
      {
        state: 'setup_required',
        role: 'alert',
        includes: '~/.local/bin/coslash',
        actions: ['installation', 'retry'],
      },
      {
        state: 'upgrade_required',
        role: 'alert',
        includes: 'needs an update',
        actions: ['installation', 'retry'],
      },
      {
        state: 'stale',
        role: 'alert',
        includes: 'unreachable',
        actions: ['retry', 'diagnostics'],
      },
      {
        state: 'error',
        role: 'alert',
        includes: 'could not load',
        actions: ['retry', 'diagnostics'],
      },
    ];

    for (const tc of cases) {
      const model = hostStripModel(
        machine({
          state: tc.state,
          reason: tc.reason,
          lastSuccessAtMs: Date.now() - 6 * 60_000,
        }),
        { nowMs: Date.now() },
      );
      expect(model.role).toBe(tc.role);
      expect(model.message).toContain(tc.includes);
      expect(model.actions).toEqual(tc.actions);
    }
  });

  it('disables retry while connecting or an in-flight manual retry', () => {
    expect(hostStripModel(machine({ state: 'stale' }), { retryInFlight: true }).retryDisabled).toBe(true);
    expect(hostStripModel(machine({ state: 'connecting' })).retryDisabled).toBe(true);
    expect(hostStripModel(machine({ state: 'stale' })).retryDisabled).toBe(false);
  });

  it('detects configured remotes and launch gates from machine facts', () => {
    const machines = [
      { sourceId: LOCAL_SOURCE_ID, label: 'This Mac', state: 'ok' as const, complete: true },
      machine({
        capabilities: ['remote-session-view/v1', 'remote-launch/v1'],
        launchableAgents: ['claude'],
      }),
    ];
    expect(remoteConfigured(machines)).toBe(true);
    expect(remoteMachine(machines)?.label).toBe('gpu-server');
    expect(remoteLaunchGate(remoteMachine(machines), 'claude')).toEqual({ allowed: true });
    expect(remoteLaunchGate(remoteMachine(machines), 'codex')).toEqual({
      allowed: false,
      reason: 'remote agent unavailable',
    });
    expect(remoteLaunchGate(machine({ capabilities: ['remote-session-view/v1'] }), 'claude')).toEqual({
      allowed: false,
      reason: 'remote upgrade required',
    });
  });

  it('formats test results and first-time SSH hint without classifying stderr', () => {
    expect(
      formatTestConnectionResult(
        machine({
          state: 'ok',
          collectorVersion: 'dev',
          hostOs: 'linux',
          hostArch: 'arm64',
          launchableAgents: ['claude', 'codex'],
        }),
      ),
    ).toContain('Resume and handoff ready');
    expect(firstTimeSshHint('gpu-server')).toContain('ssh gpu-server');
  });
});

describe('stale session display and aggregates', () => {
  it('places stale cards inactive and labels last-seen status', () => {
    expect(boardStatusKey({ status: 'busy', displayStale: true })).toBe('inactive');
    expect(displayStatusLabel({ status: 'busy', displayStale: true, lastSeenStatus: 'busy' })).toBe(
      'Last seen active',
    );
    expect(displayStatusLabel({ status: 'waiting', displayStale: true, lastSeenStatus: 'waiting' })).toBe(
      'Last seen waiting',
    );
    expect(displayStatusLabel({ status: 'busy', displayStale: false })).toBe('Active');
  });

  it('excludes ineligible sessions from aggregates and sorts them after current cards', () => {
    const current = session({ id: 'a', mtime: 200 });
    const stale = session({
      id: 'b',
      mtime: 300,
      displayStale: true,
      eligibleForAggregates: false,
      sourceId: 'r_0123456789abcdef',
      sourceLabel: 'gpu-server',
    });
    expect(sessionsForAggregates([current, stale])).toEqual([current]);
    expect(sortSessions([stale, current], SortKey.Recency, 'desc').map((s) => s.id)).toEqual(['a', 'b']);
  });
});

describe('remote diagnostics facts', () => {
  it('includes alias, retry timing, and bounded error without paths', () => {
    const lines = formatRemoteDiagnosticsFacts(
      {
        label: 'gpu-server',
        state: 'stale',
        complete: false,
        collectorVersion: 'dev',
        hostOs: 'linux',
        hostArch: 'arm64',
        lastSuccessAtMs: 1_700_000_000_000,
        coverageSinceMs: 1_699_000_000_000,
        roundTripMs: 420,
        nextRetryAtMs: Date.now() + 10 * 60_000,
        error: 'connection failed',
      },
      { sessionCount: 3, nowMs: Date.now() },
    );
    expect(lines[0]).toContain('gpu-server');
    expect(lines.some((line) => line.includes('3 sessions'))).toBe(true);
    expect(lines.some((line) => line.includes('next automatic retry'))).toBe(true);
    expect(lines.some((line) => line.includes('error: connection failed'))).toBe(true);
    expect(lines.join('\n')).not.toContain('/home/');
  });
});
