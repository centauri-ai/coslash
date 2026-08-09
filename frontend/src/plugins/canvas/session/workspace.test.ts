import { describe, expect, it } from 'vitest';
import { sessionCanvasFixture } from '@/plugins/canvas/session/fixtures';
import {
  autoArrangeSessionLayout,
  createCheckpoint,
  DEFAULT_SESSION_LAYOUT,
  defaultSessionWorkspace,
  normalizeSessionWorkspace,
  reduceSessionWorkspace,
  reduceWorkspaceForIdentity,
  sessionAttention,
  sessionKey,
  sessionPinCandidates,
  UnsupportedSessionWorkspaceError,
} from '@/plugins/canvas/session/workspace';

describe('Session Canvas workspace', () => {
  it('normalizes bounds and preserves unknown forward-compatible fields', () => {
    const workspace = normalizeSessionWorkspace({
      version: 1,
      futureRoot: { mode: 'kept' },
      layout: {
        session: { ...DEFAULT_SESSION_LAYOUT.session, x: -500, width: 20, futureNode: true },
        futureNode: { x: 3, mode: 'keep' },
      },
      checkpoints: [
        {
          id: 'checkpoint',
          name: 'Checkpoint',
          createdAt: 1,
          snapshot: { futureMetric: 42 },
          experiments: [],
        },
      ],
      pinIds: ['goal', 'goal', 3],
      note: 'remember this',
    });
    expect(workspace.futureRoot).toEqual({ mode: 'kept' });
    expect(workspace.layout.session.futureNode).toBe(true);
    expect(workspace.layout.futureNode).toEqual({ x: 3, mode: 'keep' });
    expect(workspace.checkpoints[0].snapshot.futureMetric).toBe(42);
    expect(workspace.layout.session.x).toBe(0);
    expect(workspace.layout.session.width).toBe(240);
    expect(workspace.pinIds).toEqual(['goal']);
    expect(workspace.note).toBe('remember this');
    expect(Object.keys(workspace.layout)).toHaveLength(10);
  });

  it('rejects unsupported workspace versions instead of replacing future data', () => {
    expect(() =>
      normalizeSessionWorkspace({ version: 2, futureState: { mustRemainOnServer: true } }),
    ).toThrow(UnsupportedSessionWorkspaceError);
  });

  it('keeps duplicate vendor ids distinct', () => {
    expect(sessionKey({ agent: 'claude', id: 'shared' })).not.toBe(
      sessionKey({ agent: 'codex', id: 'shared' }),
    );
  });

  it('rejects stale workspace actions after the composite session changes', () => {
    const workspace = defaultSessionWorkspace();
    expect(
      reduceWorkspaceForIdentity('codex\0shared', 'claude\0shared', workspace, {
        type: 'note',
        value: 'stale',
      }),
    ).toBeNull();
    expect(
      reduceWorkspaceForIdentity('claude\0shared', 'claude\0shared', workspace, {
        type: 'note',
        value: 'current',
      })?.note,
    ).toBe('current');
  });

  it('auto-arranges known nodes without dropping future layout data', () => {
    const layout = normalizeSessionWorkspace({
      version: 1,
      layout: {
        session: { ...DEFAULT_SESSION_LAYOUT.session, x: 999, future: true },
        futureNode: { x: 8 },
      },
    }).layout;
    const arranged = autoArrangeSessionLayout(layout);
    expect(arranged.session.x).toBe(DEFAULT_SESSION_LAYOUT.session.x);
    expect(arranged.session.future).toBe(true);
    expect(arranged.futureNode).toEqual({ x: 8 });
  });

  it('runs checkpoint, pin, experiment, and promotion flows immutably', () => {
    const detail = sessionCanvasFixture();
    const checkpoint = createCheckpoint(detail, 'checkpoint-1', 100);
    let workspace = reduceSessionWorkspace(defaultSessionWorkspace(), {
      type: 'add-checkpoint',
      checkpoint,
    });
    workspace = reduceSessionWorkspace(workspace, { type: 'toggle-pin', id: 'goal' });
    workspace = reduceSessionWorkspace(workspace, {
      type: 'add-experiment',
      checkpointId: checkpoint.id,
      experiment: { id: 'experiment-1', prompt: 'Try another route', createdAt: 101, status: 'launching' },
    });
    workspace = reduceSessionWorkspace(workspace, {
      type: 'finish-experiment',
      checkpointId: checkpoint.id,
      experimentId: 'experiment-1',
      patch: { status: 'launched', childSession: { agent: 'claude', id: 'child' } },
    });
    workspace = reduceSessionWorkspace(workspace, {
      type: 'promote-experiment',
      checkpointId: checkpoint.id,
      experimentId: 'experiment-1',
      promotedAt: 102,
    });
    expect(workspace.pinIds).toEqual(['goal']);
    expect(workspace.checkpoints[0].snapshot.modifiedFiles).toBe(1);
    expect(workspace.checkpoints[0].experiments[0]).toMatchObject({
      status: 'launched',
      childSession: { agent: 'claude', id: 'child' },
      promotedAt: 102,
    });
  });

  it('derives attention and pinnable facts from partial detail', () => {
    const detail = sessionCanvasFixture();
    expect(sessionAttention(detail).map((item) => item.id)).toEqual([
      'waiting',
      'errors',
      'tasks',
      'context',
    ]);
    expect(sessionPinCandidates(detail).map((pin) => pin.kind)).toEqual(
      expect.arrayContaining(['Goal', 'Outcome', 'Task', 'Context', 'Change', 'Decision']),
    );
  });
});
