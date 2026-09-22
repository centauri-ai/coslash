import { describe, expect, it } from 'vitest';
import {
  decodeSessionsResponse,
  diffRequestPath,
  exactDiffFailure,
  loadShareCandidatesUntilTerminal,
  remoteRefreshInProgress,
  sessionDetailRequestPath,
  sessionsRequestPath,
  shareCandidatesReducer,
  shareCandidatesRequestPath,
  synthesisRequestPath,
} from '@/pages/coslash/hooks/use-sessions';
import { LOCAL_SOURCE_ID, withLocalSourceDefaults, type Session } from '@/pages/coslash/lib/session';
import { timeWindowStart } from '@/pages/coslash/lib/time-window';

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
      sessions: [withLocalSourceDefaults(legacy)],
      machines: [],
    });
  });

  it('accepts the source-aware envelope and preserves machines', () => {
    const sessions = [sampleSession('a'), sampleSession('b', 'r_0123456789abcdef')];
    const machines = [{ sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true }];
    expect(decodeSessionsResponse({ sessions, machines })).toEqual({
      sessions: sessions.map(withLocalSourceDefaults),
      machines,
    });
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

describe('shareCandidatesRequestPath', () => {
  const now = new Date('2026-09-18T12:00:00-07:00');

  it('does not request candidates while Share is closed', () => {
    expect(shareCandidatesRequestPath({ enabled: false, window: '7d', now })).toBeNull();
  });

  it('requests seven days when Share first opens', () => {
    const since = timeWindowStart('7d', now);
    expect(shareCandidatesRequestPath({ enabled: true, window: '7d', now })).toBe(
      `/api/sessions?sourceAware=1&since=${since}&remoteSince=${since}`,
    );
  });

  it('widens only when the selected Share window widens', () => {
    const since = timeWindowStart('30d', now);
    expect(shareCandidatesRequestPath({ enabled: true, window: '30d', now })).toBe(
      `/api/sessions?sourceAware=1&since=${since}&remoteSince=${since}`,
    );
    expect(shareCandidatesRequestPath({ enabled: true, window: 'all', now })).toBe(
      '/api/sessions?sourceAware=1',
    );
  });
});

describe('shareCandidatesReducer', () => {
  it('marks a same-window reopen as loading while retaining prior candidates', () => {
    const sessions = [sampleSession('existing')];
    const loaded = {
      window: '7d' as const,
      sessions,
      isLoading: false,
      loadError: null,
    };

    expect(shareCandidatesReducer(loaded, { type: 'start', window: '7d' })).toEqual({
      window: '7d',
      sessions,
      isLoading: true,
      loadError: null,
    });
  });
});

describe('remoteRefreshInProgress', () => {
  it('recognizes a broader-history refresh before Share accepts the cached response', () => {
    expect(
      remoteRefreshInProgress([
        {
          sourceId: 'r_0123456789abcdef',
          label: 'SSH workspace',
          state: 'connecting',
          complete: false,
          reason: 'broader_history',
          refreshing: true,
        },
      ]),
    ).toBe(true);
  });

  it('waits through an existing refresh and the broader refresh it was blocking', async () => {
    const refreshingMachine = {
      sourceId: 'r_0123456789abcdef',
      label: 'SSH workspace',
      state: 'connecting' as const,
      complete: false,
      reason: 'broader_history' as const,
      refreshing: true,
    };
    const finalSession = sampleSession('remote', refreshingMachine.sourceId);
    const payloads = [
      { sessions: [], machines: [refreshingMachine] },
      { sessions: [], machines: [refreshingMachine] },
      {
        sessions: [finalSession],
        machines: [{ ...refreshingMachine, state: 'ok' as const, complete: true, refreshing: false }],
      },
    ];
    let fetches = 0;
    let waits = 0;

    const result = await loadShareCandidatesUntilTerminal(
      async () => payloads[fetches++]!,
      async () => {
        waits += 1;
      },
    );

    expect(result.sessions).toEqual([finalSession]);
    expect(fetches).toBe(3);
    expect(waits).toBe(2);
  });
});

describe('exact detail request builders', () => {
  it('qualifies local synthesis requests by agent', () => {
    expect(synthesisRequestPath({ sourceId: 'local', agent: 'codex', id: 'abc' })).toBe(
      '/api/synthesis?agent=codex&id=abc',
    );
  });

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
    expect(sessionDetailRequestPath(remote, 'latest')).toBe(
      '/api/session-detail?source=r_0123456789abcdef&agent=codex&session=abc&revision=latest',
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

describe('exact diff failures', () => {
  it.each([
    ['session_detail_stale', 'stale'],
    ['session_detail_missing', 'missing'],
    ['session_change_missing', 'missing'],
    ['session_detail_corrupt', 'corrupt'],
    ['session_diff_too_large', 'too_large'],
  ] as const)('preserves the structured %s failure as %s', (code, kind) => {
    expect(exactDiffFailure(409, code)).toMatchObject({ kind });
  });

  it('retains the HTTP status in an unknown failure', () => {
    expect(exactDiffFailure(502, 'unknown')).toEqual({
      kind: 'other',
      message: 'Could not load this exact session revision’s file changes (502).',
    });
  });
});
