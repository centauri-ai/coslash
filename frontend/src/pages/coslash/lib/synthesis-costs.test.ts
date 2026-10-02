import { describe, expect, it } from 'vitest';
import {
  combinedKnownCost,
  synthesisCostsPath,
  synthesisCostVersionChanged,
  synthesisCoverage,
} from './synthesis-costs';

describe('synthesis costs', () => {
  it('refreshes for failed or repeated paid attempts, but not an unrelated poll', () => {
    let previous: string | null = 'v1';
    const poll = (next: string | null) => {
      const refresh = synthesisCostVersionChanged(previous, next);
      previous = next;
      return refresh;
    };
    expect(poll('v2')).toBe(true);
    expect(poll('v2')).toBe(false);
    expect(poll('v3')).toBe(true);
    expect(poll('v3')).toBe(false);
    expect(poll(null)).toBe(false);
    expect(poll('v4')).toBe(true);
  });
  it('adds known micro-USD and keeps unknown calls partial', () => {
    expect(combinedKnownCost(2, { knownCostMicroUsd: 500_000, unknownInvocationCount: 1 })).toEqual({
      knownUsd: 2.5,
      partial: true,
    });
    expect(combinedKnownCost(0, { knownCostMicroUsd: 0, unknownInvocationCount: 0 })).toEqual({
      knownUsd: 0,
      partial: false,
    });
    expect(combinedKnownCost(0, { knownCostMicroUsd: null, unknownInvocationCount: 1 })).toEqual({
      knownUsd: 0,
      partial: true,
    });
    expect(combinedKnownCost(0, { knownCostMicroUsd: 1, unknownInvocationCount: 0 }).knownUsd).toBe(0.000001);
    expect(synthesisCoverage({ knownCostMicroUsd: null, unknownInvocationCount: 1 })).toBe('unknown');
    expect(synthesisCoverage({ knownCostMicroUsd: 0, unknownInvocationCount: 0 })).toBe('complete');
    expect(
      synthesisCoverage({ knownCostMicroUsd: 0, unknownInvocationCount: 0, incompleteRoundCount: 1 }),
    ).toBe('partial');
  });

  it('distinguishes missing historical accounting from recorded zero and known spend', () => {
    const empty = { knownCostMicroUsd: 0, invocationCount: 0, unknownInvocationCount: 0 };
    expect(synthesisCoverage(empty, true)).toBe('unknown');
    expect(combinedKnownCost(1, empty, true)).toEqual({ knownUsd: 1, partial: true });
    expect(synthesisCoverage({ ...empty, knownCostMicroUsd: 500_000, invocationCount: 1 }, true)).toBe(
      'partial',
    );
    expect(synthesisCoverage({ ...empty, invocationCount: 1 }, true)).toBe('partial');
    expect(synthesisCoverage(empty, false)).toBe('complete');
    expect(combinedKnownCost(1, empty, false)).toEqual({ knownUsd: 1, partial: false });
  });

  it('builds independent month and composite session queries', () => {
    expect(synthesisCostsPath({ since: 10, until: 20 })).toBe(
      '/api/synthesis-costs?source=local&since=10&until=20',
    );
    expect(synthesisCostsPath({ sourceId: 'local', agent: 'codex', id: 'a/b', cursor: 'x==' })).toBe(
      '/api/synthesis-costs?source=local&agent=codex&id=a%2Fb&cursor=x%3D%3D',
    );
    expect(() => synthesisCostsPath({ sourceId: 'ssh', agent: 'codex', id: 'a' })).toThrow();
  });
});
