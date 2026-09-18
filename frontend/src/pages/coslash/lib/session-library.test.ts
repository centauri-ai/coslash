import { describe, expect, it } from 'vitest';
import type { Session } from './session';
import {
  ALL_REPOSITORIES,
  eligibleSessionCandidates,
  filterSessionLibrary,
  latestLogicalSessions,
} from './session-library';

function session(overrides: Partial<Session> = {}): Session {
  return {
    sourceId: 'local',
    sourceLabel: 'Local Mac',
    sourceClass: 'local',
    logicalSessionId: 'local:codex:one',
    revision: 10,
    completion: 'complete',
    privacy: 'shareable',
    shareEligibility: 'eligible',
    eligibleForAggregates: true,
    displayStale: false,
    agent: 'codex',
    id: 'one',
    name: 'Fix the compiler',
    summary: null,
    status: null,
    cwd: '/private/work/compiler',
    branch: 'main',
    repo: 'compiler',
    repoLocalOnly: false,
    files: 0,
    durationMs: null,
    tokens: {},
    cost: 0,
    unpricedModels: [],
    subagents: [],
    mtime: 10,
    entrypoint: null,
    synthesis: null,
    synthesisPending: false,
    declaredGoal: null,
    model: null,
    contextTokens: null,
    contextWindow: null,
    turns: 0,
    toolUses: 0,
    errors: 0,
    compactions: 0,
    firstPrompt: null,
    commands: [],
    commits: [],
    prs: 0,
    todos: [],
    digest: [],
    fileEdits: [],
    git: null,
    lastEditAt: null,
    ...overrides,
  };
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

  it('filters 47 rows by repository and eligibility without searching private paths', () => {
    const rows = Array.from({ length: 47 }, (_, index) =>
      session({
        id: String(index),
        logicalSessionId: `local:codex:${index}`,
        repo: index % 2 === 0 ? 'compiler' : 'website',
        shareEligibility: index === 0 ? 'private' : 'eligible',
        cwd: `/private/secret-${index}`,
      }),
    );
    const visible = filterSessionLibrary(rows, {
      search: 'compiler',
      repository: 'compiler',
      source: 'all',
      shareState: 'eligible',
    });
    expect(visible).toHaveLength(23);
    expect(
      filterSessionLibrary(rows, {
        search: 'secret-2',
        repository: ALL_REPOSITORIES,
        source: 'all',
        shareState: 'all',
      }),
    ).toEqual([]);
  });

  it('exports only eligible local and SSH sessions to the LB-04 handoff', () => {
    const local = session();
    const ssh = session({
      sourceId: 'r_0123456789abcdef',
      sourceLabel: 'SSH workspace',
      sourceClass: 'ssh_workspace',
      logicalSessionId: 'r_0123456789abcdef:claude:two',
      agent: 'claude',
      id: 'two',
    });
    const privateRemote = session({
      sourceId: 'r_0123456789abcdef',
      sourceClass: 'ssh_workspace',
      logicalSessionId: 'r_0123456789abcdef:codex:private',
      shareEligibility: 'private',
    });
    const offlineRemote = session({
      sourceId: 'r_0123456789abcdef',
      sourceClass: 'ssh_workspace',
      logicalSessionId: 'r_0123456789abcdef:codex:offline',
      shareEligibility: 'offline',
    });
    expect(eligibleSessionCandidates([local, ssh, privateRemote, offlineRemote])).toEqual([local, ssh]);
  });
});
