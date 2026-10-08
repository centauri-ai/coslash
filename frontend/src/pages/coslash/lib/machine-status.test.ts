import { describe, expect, it } from 'vitest';
import {
  connectorReadyNotice,
  machineRetryable,
  machineStatusText,
  machineTone,
  needsSettings,
} from '@/pages/coslash/lib/machine-status';
import type { MachineFact } from '@/pages/coslash/lib/machines';

const machine = (actionRequired: MachineFact['actionRequired']): MachineFact => ({
  sourceId: 'remote',
  label: 'agent-box',
  state: 'stale',
  complete: false,
  actionRequired,
});

describe('SSH action status', () => {
  it('requires host identity verification without offering retry', () => {
    const changed = machine('verify_host_key');

    expect(machineTone(changed)).toBe('failed');
    expect(machineRetryable(changed)).toBe(false);
    expect(needsSettings(changed)).toBe(true);
    expect(machineStatusText(changed)).toContain('Verify its host key in Terminal');
  });

  it('keeps retry and Terminal guidance for authentication', () => {
    const authentication = machine('authenticate');

    expect(machineRetryable(authentication)).toBe(true);
    expect(machineStatusText(authentication)).toContain('Terminal guidance');
  });
});

describe('connector ready notice', () => {
  it('stays a success while the connection is healthy', () => {
    expect(connectorReadyNotice({ ...machine(undefined), state: 'ok', complete: true }).success).toBe(true);
    expect(connectorReadyNotice(null).success).toBe(true);
  });

  it('stops claiming success when the latest refresh failed', () => {
    const notice = connectorReadyNotice({
      ...machine(undefined),
      state: 'error',
      reason: 'partial_agent_data',
    });

    expect(notice.success).toBe(false);
    expect(notice.text).toContain('latest refresh failed');
  });
});
