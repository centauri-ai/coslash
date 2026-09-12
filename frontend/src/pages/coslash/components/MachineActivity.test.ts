import { describe, expect, it } from 'vitest';
import { machineActivityCopy } from '@/pages/coslash/lib/machine-activity';

describe('MachineActivity progressive copy', () => {
  it('explains that saved sessions remain available while earlier history loads', () => {
    const copy = machineActivityCopy(
      {
        sourceId: 'r_0123456789abcdef',
        label: 'SSH workspace',
        state: 'ok',
        complete: false,
        refreshing: true,
        reason: 'broader_history',
        coverageSinceMs: Date.UTC(2026, 8, 4),
      },
      true,
    );
    expect(copy).toContain('Loading earlier history');
    expect(copy).toContain('Displayed sessions are available now');
  });
});
