import { describe, expect, it } from 'vitest';
import { ALL_MACHINES, isMachineFilterValue } from './CoslashTabMenus';
import type { MachineFact } from './lib/machines';

describe('isMachineFilterValue', () => {
  const machines: MachineFact[] = [
    { sourceId: 'r_0123456789abcdef', label: 'agent-box', state: 'ok', complete: true },
  ];

  it('accepts all, local, and an available remote machine', () => {
    expect(isMachineFilterValue(ALL_MACHINES, machines)).toBe(true);
    expect(isMachineFilterValue('local', machines)).toBe(true);
    expect(isMachineFilterValue('r_0123456789abcdef', machines)).toBe(true);
  });

  it('rejects an unknown machine', () => {
    expect(isMachineFilterValue('r_unknown', machines)).toBe(false);
  });
});
