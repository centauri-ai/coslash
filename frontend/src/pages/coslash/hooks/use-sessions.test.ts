import { describe, expect, it } from 'vitest';
import { decodeSessionsResponse } from '@/pages/coslash/hooks/use-sessions';
import type { Session } from '@/pages/coslash/lib/session';

function sampleSession(id: string): Session {
  return {
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
  it('accepts the legacy bare array', () => {
    const sessions = [sampleSession('a')];
    expect(decodeSessionsResponse(sessions)).toEqual(sessions);
  });

  it('accepts the source-aware envelope', () => {
    const sessions = [sampleSession('a'), sampleSession('b')];
    expect(
      decodeSessionsResponse({
        sessions,
        machines: [{ sourceId: 'local', label: 'This Mac', state: 'ok', complete: true }],
      }),
    ).toEqual(sessions);
  });

  it('rejects invalid bodies', () => {
    expect(() => decodeSessionsResponse({ machines: [] })).toThrow('Invalid sessions response');
    expect(() => decodeSessionsResponse(null)).toThrow('Invalid sessions response');
  });
});
