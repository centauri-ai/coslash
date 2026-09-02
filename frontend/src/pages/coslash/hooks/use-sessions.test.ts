import { describe, expect, it } from 'vitest';
import {
  decodeSessionsResponse,
  diffRequestPath,
  sessionsRequestPath,
  synthesisRequestPath,
} from '@/pages/coslash/hooks/use-sessions';
import { LOCAL_SOURCE_ID, type Session } from '@/pages/coslash/lib/session';

function sampleSession(id: string, sourceId = LOCAL_SOURCE_ID): Session {
  return {
    sourceId,
    sourceLabel: sourceId === LOCAL_SOURCE_ID ? 'This Mac' : 'gpu-server',
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
      ...legacy
    } = sampleSession('a');
    expect(decodeSessionsResponse([legacy])).toEqual({
      sessions: [sampleSession('a')],
      machines: [],
    });
  });

  it('accepts the source-aware envelope and preserves machines', () => {
    const sessions = [sampleSession('a'), sampleSession('b', 'r_0123456789abcdef')];
    const machines = [{ sourceId: 'local', label: 'This Mac', state: 'ok', complete: true }];
    expect(decodeSessionsResponse({ sessions, machines })).toEqual({ sessions, machines });
  });

  it('treats a null or missing sessions list as empty', () => {
    const machines = [{ sourceId: 'local', label: 'This Mac', state: 'ok', complete: true }];
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
        machines: [{ sourceId: 'local', label: 'This Mac', state: 'nope', complete: true }],
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

describe('local-only request builders', () => {
  it('refuses remote diff and synthesis construction', () => {
    const remote = {
      sourceId: 'r_0123456789abcdef',
      agent: 'codex',
      id: 'abc',
      sessionId: 'abc',
      path: 'a.ts',
    };
    expect(() => diffRequestPath(remote)).toThrow('remote diff unsupported');
    expect(() => synthesisRequestPath(remote)).toThrow('remote synthesis unsupported');
  });
});
