import { describe, expect, it } from 'vitest';
import {
  firstTimeSshHint,
  formatTestConnectionResult,
  hostStripModel,
  hostStripVisible,
} from '@/pages/coslash/lib/host-strip';
import type { MachineFact, MachineReason, MachineState } from '@/pages/coslash/lib/machines';
import { displayStatusLabel } from '@/pages/coslash/lib/session';

function machine(state: MachineState, reason?: MachineReason): MachineFact {
  return {
    sourceId: 'r_0123456789abcdef',
    label: 'gpu-server',
    state,
    complete: state === 'ok',
    reason,
  };
}

describe('Mac-only remote health copy', () => {
  it.each([
    ['connecting', 'initial_refresh'],
    ['limited', 'history_truncated'],
    ['limited', 'partial_agent_data'],
    ['error', 'authentication_failed'],
    ['error', 'host_key_failed'],
    ['stale', 'connection_failed'],
    ['error', 'sftp_unavailable'],
    ['error', 'permission_denied'],
    ['limited', 'no_supported_data'],
    ['error', 'invalid_remote_data'],
  ] as const)('renders %s / %s without Linux installation guidance', (state, reason) => {
    const model = hostStripModel(machine(state, reason), { nowMs: 10_000 });
    expect(model.message).not.toMatch(/collector|install|upgrade/i);
    expect(model.actions).not.toContain('installation');
  });

  it('only hides healthy or disabled machine strips', () => {
    expect(hostStripVisible(machine('ok'))).toBe(false);
    expect(hostStripVisible(machine('disabled', 'disabled'))).toBe(false);
    expect(hostStripVisible(machine('limited', 'partial_agent_data'))).toBe(true);
  });

  it('describes SFTP-only setup and first-use SSH guidance', () => {
    expect(formatTestConnectionResult(machine('ok'))).toContain('no Linux coSlash installation needed');
    expect(firstTimeSshHint('gpu-server')).toContain('ssh gpu-server');
  });
});

describe('remote liveness', () => {
  it('keeps unknown liveness distinct from recency and stale host data', () => {
    const remote = { sourceId: 'r_0123456789abcdef', status: null, displayStale: false };
    expect(displayStatusLabel(remote)).toBe('Liveness unknown');
    expect(displayStatusLabel({ ...remote, displayStale: true })).toBe('Liveness unknown · stale view');
  });
});
