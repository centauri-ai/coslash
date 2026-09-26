import { describe, expect, it } from 'vitest';
import { handoffLabel, newestHandoffs, type DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';

const base: DirectedHandoff = {
  id: 'older',
  sourceId: 'local',
  sourceAgent: 'claude',
  sourceSessionId: 'source',
  targetAgent: 'codex',
  kind: 'custom',
  status: 'running',
  createdAt: 1,
};

describe('directed handoff presentation', () => {
  it('keeps the newest outbound handoff visible when an older one finishes', () => {
    const newer = { ...base, id: 'newer', createdAt: 1, activity: 'needs_input' as const };
    const visible = newestHandoffs([newer, { ...base, status: 'completed' }]);
    expect(visible.get('local:claude:source')).toEqual(newer);
    expect(handoffLabel(newer)).toBe('Codex needs input');
  });

  it('keeps the same session ID on different sources distinct', () => {
    expect(newestHandoffs([base, { ...base, id: 'remote', sourceId: 'ssh:box' }]).size).toBe(2);
  });
});
