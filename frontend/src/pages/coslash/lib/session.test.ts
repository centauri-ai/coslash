import { describe, expect, it } from 'vitest';
import { decodeMachineFact } from '@/pages/coslash/lib/machines';
import {
  boardStatusKey,
  environmentFact,
  getModality,
  getSessionVendors,
  LOCAL_SOURCE_ID,
  resumeDisabled,
  resumeDisabledHint,
  sessionKey,
  sessionsForAggregates,
  withLocalSourceDefaults,
  type Session,
} from '@/pages/coslash/lib/session';

describe('getModality', () => {
  it('labels OpenCode client entrypoints without changing the shared CLI modality', () => {
    expect(getModality('opencode-desktop')).toBe('Desktop');
    expect(getModality('opencode-cli')).toBe('CLI');
    expect(getModality('cli')).toBe('Interactive');
  });
});

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

describe('resumeDisabledHint', () => {
  it('disables Resume for an active local Codex session', () => {
    expect(
      resumeDisabledHint({ sourceId: LOCAL_SOURCE_ID, agent: 'codex', status: 'busy', displayStale: false }),
    ).toBe('This session is already active');
  });

  it('leaves inactive local and non-Codex sessions resumable', () => {
    expect(
      resumeDisabledHint({ sourceId: LOCAL_SOURCE_ID, agent: 'codex', status: 'idle', displayStale: false }),
    ).toBeUndefined();
    expect(
      resumeDisabledHint({ sourceId: LOCAL_SOURCE_ID, agent: 'claude', status: 'busy', displayStale: false }),
    ).toBeUndefined();
  });

  it('preserves remote active and unavailable hints', () => {
    expect(
      resumeDisabledHint(
        { sourceId: 'r_0123456789abcdef', agent: 'codex', status: 'busy', displayStale: false },
        true,
        'remote hint',
      ),
    ).toBe('This session is already active');
    expect(
      resumeDisabledHint(
        { sourceId: 'r_0123456789abcdef', agent: 'codex', status: 'idle', displayStale: false },
        false,
        'Remote is offline',
      ),
    ).toBe('Remote is offline');
  });
});

describe('resumeDisabled', () => {
  it('disables an active local Codex session but preserves remote launch behavior', () => {
    expect(
      resumeDisabled(
        { sourceId: LOCAL_SOURCE_ID, displayStale: false, launchable: true },
        'This session is already active',
      ),
    ).toBe(true);
    expect(
      resumeDisabled(
        {
          sourceId: 'r_0123456789abcdef',
          displayStale: false,
          launchable: true,
        },
        'This session is already active',
      ),
    ).toBe(false);
    expect(
      resumeDisabled(
        {
          sourceId: 'r_0123456789abcdef',
          displayStale: false,
          launchable: false,
        },
        'Remote is offline',
      ),
    ).toBe(true);
  });
});

describe('decodeMachineFact', () => {
  it('accepts the exact local-machine JSON shape without remote-only fields', () => {
    expect(decodeMachineFact({ sourceId: 'local', label: 'This Mac', state: 'ok', complete: true })).toEqual({
      sourceId: 'local',
      label: 'This Mac',
      state: 'ok',
      complete: true,
    });
  });

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
