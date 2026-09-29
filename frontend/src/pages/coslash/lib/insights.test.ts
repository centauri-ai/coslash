import { describe, expect, it } from 'vitest';
import { buildInsights } from './insights';
import type { Session } from './session';

const usage: Session['tokens'][string] = {
  input_tokens: 1,
  output_tokens: 0,
  cache_creation_input_tokens: 0,
  cache_creation_1h_input_tokens: 0,
  cache_read_input_tokens: 0,
};

function sample(id: string, day: number, overrides: Partial<Session> = {}): Session {
  return {
    sourceId: 'local',
    agent: 'codex',
    id,
    logicalSessionId: `local:codex:${id}`,
    revision: day,
    eligibleForAggregates: true,
    mtime: new Date(2026, 7, day, 12).getTime(),
    repo: 'github.com/team/app',
    tokens: {},
    observedModels: [],
    model: 'gpt-5',
    cost: 1,
    unpricedModels: [],
    ...overrides,
  } as Session;
}

describe('buildInsights', () => {
  it('counts latest logical sessions in their last-active month and collapses model provider aliases', () => {
    const sessions = [
      sample('one', 5, {
        tokens: { 'openai/gpt-5': usage, 'gpt-5': usage },
      }),
      sample('one', 6, {
        tokens: { 'openai/gpt-5': usage, 'gpt-5': usage },
      }),
      sample('two', 6, {
        sourceId: 'remote',
        logicalSessionId: 'remote:claude:two',
        agent: 'claude',
        tokens: { 'anthropic/claude-opus-5': usage },
        cost: null,
      }),
      sample('july', 1, { mtime: new Date(2026, 6, 31, 23).getTime() }),
      sample('september', 1, { mtime: new Date(2026, 8, 1).getTime() }),
      sample('incomplete', 6, { eligibleForAggregates: false }),
    ];

    const result = buildInsights(sessions, new Date(2026, 7, 1));

    expect(result.sessionCount).toBe(2);
    expect(result.agents).toEqual([
      { name: 'Claude Code', count: 1 },
      { name: 'Codex', count: 1 },
    ]);
    expect(result.models).toEqual([
      { name: 'claude-opus-5', count: 1 },
      { name: 'gpt-5', count: 1 },
    ]);
    expect(result.days[5]).toEqual({ day: 6, count: 2 });
    expect(result.knownCost).toBe(1);
    expect(result.unknownCostCount).toBe(1);
  });

  it('orders the top five repositories by session count, then name', () => {
    const repos = ['z', 'z', 'a', 'a', 'b', 'c', 'd', 'e'];
    const result = buildInsights(
      repos.map((repo, index) => sample(String(index), 10, { repo })),
      new Date(2026, 7, 1),
    );

    expect(result.repositories).toEqual([
      { name: 'a', count: 2 },
      { name: 'z', count: 2 },
      { name: 'b', count: 1 },
      { name: 'c', count: 1 },
      { name: 'd', count: 1 },
    ]);
    expect(result.days).toHaveLength(31);
  });
});
