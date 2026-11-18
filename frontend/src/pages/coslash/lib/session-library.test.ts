import { describe, expect, it } from 'vitest';
import type { Session } from './session';
import { makeSession } from './session-fixture';
import { latestLogicalSessions } from './session-library';

function session(overrides: Partial<Session> = {}): Session {
  return makeSession({
    sourceClass: 'local',
    logicalSessionId: 'local:codex:one',
    revision: 10,
    detailRevision: '10',
    completion: 'complete',
    privacy: 'shareable',
    shareEligibility: 'eligible',
    id: 'one',
    name: 'Fix the compiler',
    cwd: '/private/work/compiler',
    branch: 'main',
    repo: 'compiler',
    cost: 0,
    mtime: 10,
    ...overrides,
  });
}

describe('session library', () => {
  it('keeps the newest revision without merging same-named SSH and local sessions', () => {
    const older = session();
    const newest = session({ revision: 20, mtime: 20, name: 'New compiler fix' });
    const ssh = session({
      sourceId: 'r_0123456789abcdef',
      sourceLabel: 'SSH workspace',
      sourceClass: 'ssh_workspace',
      logicalSessionId: 'r_0123456789abcdef:codex:one',
      name: 'Remote compiler fix',
    });
    expect(latestLogicalSessions([older, newest, ssh])).toEqual([newest, ssh]);
  });
});
