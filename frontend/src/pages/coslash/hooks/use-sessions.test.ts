import { describe, expect, it } from 'vitest';
import {
  decodeSessionsResponse,
  diffRequestPath,
  sessionDetailRequestPath,
  sessionsRequestPath,
  synthesisRequestPath,
} from '@/pages/coslash/hooks/use-sessions';
import { LOCAL_SOURCE_ID, type Session } from '@/pages/coslash/lib/session';

function sampleSession(id: string, sourceId = LOCAL_SOURCE_ID): Session {
  const local = sourceId === LOCAL_SOURCE_ID;
  return {
    sourceId,
    sourceLabel: local ? 'Local Mac' : 'SSH workspace',
    sourceClass: local ? 'local' : 'ssh_workspace',
    logicalSessionId: `${sourceId}:codex:${id}`,
    revision: 1,
    detailRevision: local ? '1' : 'a'.repeat(64),
    completion: 'complete',
    privacy: 'shareable',
    shareEligibility: 'eligible',
    eligibleForAggregates: true,
    displayStale: false,
    agent: 'codex',
    id,
    name: null,
    summary: null,
    status: null,
    cwd: '/tmp',
    branch: null,
    repo: null,
    repoLocalOnly: false,
    files: 0,
    durationMs: null,
    tokens: {},
    cost: 0,
    unpricedModels: [],
    subagents: [],
    mtime: 1,
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
  };
}

describe('decodeSessionsResponse', () => {
  it('accepts the legacy bare array with local source defaults', () => {
    const {
      sourceId: _s,
      sourceLabel: _l,
      eligibleForAggregates: _e,
      displayStale: _d,
      sourceClass: _sc,
      logicalSessionId: _li,
      revision: _r,
      completion: _c,
      privacy: _p,
      shareEligibility: _se,
      ...legacy
    } = sampleSession('a');
    expect(decodeSessionsResponse([legacy])).toEqual({
      sessions: [sampleSession('a')],
      machines: [],
    });
  });

  it('accepts the source-aware envelope and preserves machines', () => {
    const sessions = [sampleSession('a'), sampleSession('b', 'r_0123456789abcdef')];
    const machines = [{ sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true }];
    expect(decodeSessionsResponse({ sessions, machines })).toEqual({ sessions, machines });
  });

  it('normalizes null remote collections so sparse facts cannot crash the board', () => {
    const sparse = {
      ...sampleSession('remote', 'r_0123456789abcdef'),
      tokens: null,
      unpricedModels: null,
      subagents: null,
      commands: null,
      commits: null,
      todos: null,
      digest: null,
      fileEdits: null,
    };
    const decoded = decodeSessionsResponse({ sessions: [sparse], machines: [] }).sessions[0];
    expect(decoded).toMatchObject({
      tokens: {},
      unpricedModels: [],
      subagents: [],
      commands: [],
      commits: [],
      todos: [],
      digest: [],
      fileEdits: [],
    });
  });

  it('treats a null or missing sessions list as empty', () => {
    const machines = [{ sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true }];
    expect(decodeSessionsResponse({ sessions: null, machines })).toEqual({ sessions: [], machines });
    expect(decodeSessionsResponse({ machines })).toEqual({ sessions: [], machines });
  });

  it('rejects invalid bodies and unknown machine enums', () => {
    expect(() => decodeSessionsResponse(null)).toThrow('Invalid sessions response');
    expect(() => decodeSessionsResponse({ sessions: 'nope', machines: [] })).toThrow(
      'Invalid sessions response',
    );
    expect(() =>
      decodeSessionsResponse({
        sessions: [],
        machines: [{ sourceId: 'local', label: 'Local Mac', state: 'nope', complete: true }],
      }),
    ).toThrow(/Expected one of/);
  });
});

describe('sessionsRequestPath', () => {
  it('sends displayed remoteSince while Hub omits local since', () => {
    expect(sessionsRequestPath({ localSince: null, remoteSince: 1_700_000_000_000 })).toBe(
      '/api/sessions?sourceAware=1&remoteSince=1700000000000',
    );
  });

  it('sends matching cutoffs for ordinary board refresh', () => {
    expect(sessionsRequestPath({ localSince: 10, remoteSince: 10 })).toBe(
      '/api/sessions?sourceAware=1&since=10&remoteSince=10',
    );
  });
});

describe('exact detail request builders', () => {
  it('includes source, agent, session, revision, and ordered opaque changes', () => {
    const remote = {
      sourceId: 'r_0123456789abcdef',
      agent: 'codex',
      id: 'abc',
      sessionId: 'abc',
      detailRevision: 'a'.repeat(64),
      revision: 'a'.repeat(64),
      path: 'a.ts',
      changeIds: ['change-000000-000000', 'change-000000-000001'],
    };
    expect(sessionDetailRequestPath(remote)).toBe(
      `/api/session-detail?source=r_0123456789abcdef&agent=codex&session=abc&revision=${'a'.repeat(64)}`,
    );
    expect(diffRequestPath(remote)).toBe(
      `/api/diff?source=r_0123456789abcdef&agent=codex&session=abc&revision=${'a'.repeat(64)}&change=change-000000-000000&change=change-000000-000001`,
    );
  });

  it('keeps synthesis local-only', () => {
    const remote = { sourceId: 'r_0123456789abcdef', agent: 'codex', id: 'abc' };
    expect(() => synthesisRequestPath(remote)).toThrow('remote synthesis unsupported');
  });
});
