import { describe, expect, it } from 'vitest';
import {
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

  it('keeps Terminal guidance for an unknown host key', () => {
    expect(machineStatusText(machine('authenticate'))).toContain('Terminal guidance');
  });
});
