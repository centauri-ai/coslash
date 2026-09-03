import { describe, expect, it } from 'vitest';
import { machinesForSourceFilter, type MachineFact } from './machines';

function machine(overrides: Partial<MachineFact>): MachineFact {
  return {
    sourceId: 'remote',
    label: 'agent-box',
    state: 'ok',
    complete: true,
    ...overrides,
  };
}

describe('machinesForSourceFilter', () => {
  it('shows remote filters only after a connector succeeds or history is cached', () => {
    expect(
      machinesForSourceFilter([
        machine({ sourceId: 'local', label: 'This Mac' }),
        machine({ sourceId: 'pending' }),
        machine({ sourceId: 'ready', helper: { state: 'ready', compatible: true, fallback: false } }),
        machine({ sourceId: 'cached', lastSuccessAtMs: 1 }),
      ]).map((item) => item.sourceId),
    ).toEqual(['ready', 'cached']);
  });
});
