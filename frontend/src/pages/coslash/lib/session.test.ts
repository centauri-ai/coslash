import { describe, expect, it } from 'vitest';
import { decodeMachineFact } from '@/pages/coslash/lib/machines';
import {
  boardStatusKey,
  environmentFact,
  getSessionVendors,
  LOCAL_SOURCE_ID,
  sessionKey,
  sessionsForAggregates,
  withLocalSourceDefaults,
  type Session,
} from '@/pages/coslash/lib/session';

describe('getSessionVendors', () => {
  it('returns only vendors represented by loaded sessions in display order', () => {
    const sessions = [
      { agent: 'opencode' },
      { agent: 'claude' },
      { agent: 'claude' },
      { agent: 'unknown' },
    ] satisfies Pick<Session, 'agent'>[];

    expect(getSessionVendors(sessions)).toEqual(['claude', 'opencode']);
  });
});

describe('sessionKey', () => {
  it('keeps the same agent and id distinct across sources', () => {
    expect(sessionKey({ sourceId: LOCAL_SOURCE_ID, agent: 'codex', id: 'abc' })).toBe('local:codex:abc');
    expect(sessionKey({ sourceId: 'r_0123456789abcdef', agent: 'codex', id: 'abc' })).toBe(
      'r_0123456789abcdef:codex:abc',
    );
  });
});

describe('withLocalSourceDefaults', () => {
  it('fills omitted legacy source fields as local', () => {
    expect(withLocalSourceDefaults({ agent: 'codex', id: 'abc' })).toEqual({
      agent: 'codex',
      id: 'abc',
      sourceId: 'local',
      sourceLabel: 'This Mac',
      eligibleForAggregates: true,
      displayStale: false,
    });
  });
});

describe('environmentFact', () => {
  it('renders missing and blank values as an em dash', () => {
    expect(environmentFact(null)).toBe('—');
    expect(environmentFact(undefined)).toBe('—');
    expect(environmentFact('')).toBe('—');
    expect(environmentFact('  ')).toBe('—');
    expect(environmentFact('/home/user/proj')).toBe('/home/user/proj');
  });
});

describe('sessionsForAggregates', () => {
  it('omits ineligible sessions from aggregate counts', () => {
    expect(
      sessionsForAggregates([
        { eligibleForAggregates: true },
        { eligibleForAggregates: false },
        { eligibleForAggregates: true },
      ]),
    ).toEqual([{ eligibleForAggregates: true }, { eligibleForAggregates: true }]);
  });
});

describe('boardStatusKey', () => {
  it('keeps sessions with unavailable or stale remote liveness out of Inactive', () => {
    expect(boardStatusKey({ sourceId: 'r_0123456789abcdef', status: null, displayStale: false })).toBe(
      'unknown',
    );
    expect(boardStatusKey({ sourceId: 'local', status: 'busy', displayStale: true })).toBe('unknown');
  });

  it('uses Inactive only for local sessions with no status', () => {
    expect(boardStatusKey({ sourceId: 'local', status: null, displayStale: false })).toBe('inactive');
  });
});

describe('decodeMachineFact', () => {
  it('accepts a healthy remote machine fact', () => {
    expect(
      decodeMachineFact({
        sourceId: 'r_0123456789abcdef',
        label: 'gpu-server',
        state: 'ok',
        complete: true,
        coverage: [{ agent: 'claude', candidateFiles: 2, selectedFiles: 2, truncated: false }],
      }),
    ).toMatchObject({
      sourceId: 'r_0123456789abcdef',
      state: 'ok',
      coverage: [{ agent: 'claude', candidateFiles: 2 }],
    });
  });

  it('rejects unknown machine states', () => {
    expect(() =>
      decodeMachineFact({ sourceId: 'local', label: 'This Mac', state: 'weird', complete: true }),
    ).toThrow(/Expected one of/);
  });
});
