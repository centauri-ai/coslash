import { describe, expect, it } from 'vitest';
import {
  idleLaunch,
  retargetLaunch,
  settleLaunch,
  startLaunch,
  type LaunchView,
} from '@/pages/coslash/hooks/use-launch-terminal';

const key = 'local:grok:session-a';

describe('Grok fresh launch attempts', () => {
  it('starts one launch and ignores a second click until it settles', () => {
    const started = startLaunch(idleLaunch(key), key);
    expect(started).toEqual({ launching: true, error: null, sessionKey: key, attempt: 1 });
    expect(startLaunch(started as LaunchView, key)).toBeNull();
    expect(settleLaunch(started as LaunchView, { sessionKey: key, attempt: 1 }, null)).toMatchObject({
      launching: false,
      error: null,
      attempt: 1,
    });
  });

  it('keeps the error for a retry after a failed launch', () => {
    const started = startLaunch(idleLaunch(key), key) as LaunchView;
    const failed = settleLaunch(started, { sessionKey: key, attempt: 1 }, 'Launch failed (500)');
    expect(failed).toMatchObject({ launching: false, error: 'Launch failed (500)' });
    expect(startLaunch(failed, key)?.attempt).toBe(2);
  });

  it('drops a completion after the inspector moves to another session', () => {
    const started = startLaunch(idleLaunch(key), key) as LaunchView;
    const moved = retargetLaunch(started, 'local:grok:session-b');
    expect(moved).toMatchObject({
      launching: false,
      error: null,
      sessionKey: 'local:grok:session-b',
      attempt: 2,
    });
    expect(settleLaunch(moved, { sessionKey: key, attempt: 1 }, 'Launch failed (500)')).toBe(moved);
  });
});
