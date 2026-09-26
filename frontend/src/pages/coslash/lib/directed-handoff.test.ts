import { describe, expect, it } from 'vitest';
import {
  handoffLabel,
  handoffSelection,
  newestHandoffs,
  type DirectedHandoff,
} from '@/pages/coslash/lib/directed-handoff';

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

describe('pending target navigation', () => {
  it('does not reopen an old target after another session is selected or the Inspector closes', () => {
    const initial = { selectedSessionKey: 'source', pendingTargetKey: null };
    const waiting = handoffSelection(initial, { type: 'pending', key: 'target' });
    const selected = handoffSelection(waiting, { type: 'select', key: 'other' });
    expect(handoffSelection(selected, { type: 'found', key: 'target' })).toEqual(selected);
    expect(handoffSelection(selected, { type: 'clear-missing', key: 'source' })).toEqual(selected);

    const closed = handoffSelection(waiting, { type: 'select', key: null });
    expect(handoffSelection(closed, { type: 'found', key: 'target' })).toEqual(closed);
  });

  it('opens a target that appears while it is still pending', () => {
    const waiting = { selectedSessionKey: 'source', pendingTargetKey: 'target' };
    expect(handoffSelection(waiting, { type: 'found', key: 'target' })).toEqual({
      selectedSessionKey: 'target',
      pendingTargetKey: null,
    });
  });
});
