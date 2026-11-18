import { describe, expect, it } from 'vitest';
import { sessionMatchesSearch, sessionMatchesSearchTerm } from './search';
import type { Session } from './session';
import { makeSession } from './session-fixture';

function session(overrides: Partial<Session> = {}): Session {
  return makeSession({
    id: 'one',
    name: 'Fix the compiler',
    cwd: '/private/work/compiler',
    branch: 'main',
    repo: 'compiler',
    ...overrides,
  });
}

function search(rows: Session[], query: string): Session[] {
  return rows.filter((row) => sessionMatchesSearchTerm(row, query));
}

describe('session search', () => {
  it('searches narrative content for local sessions', () => {
    const row = session({
      firstPrompt: 'Investigate the lunar parser',
      summary: 'Vendor cancellation is resolved',
      declaredGoal: 'Ship reliable refresh behavior',
      digest: [
        { turn: 1, category: 'first_prompt', description: 'Trace the cobalt request' },
        { turn: 2, category: 'user', description: 'Preserve the amber response' },
        {
          turn: 3,
          category: 'question',
          description: 'Should retries use jitter?',
          answer: 'Use bounded violet jitter',
        },
        { turn: 4, category: 'recap', description: 'The indigo migration completed' },
        { turn: 5, category: 'plan', description: 'The saffron rollout plan' },
        { turn: 6, category: 'compaction', description: 'Do not index the silver compaction' },
      ],
      synthesis: {
        goals: ['Protect the quartz workflow'],
        outcome: 'The topaz release is stable',
        keyDecisions: ['Keep the ochre cache local'],
        nextStep: 'Measure the scarlet rollout',
      },
    });

    for (const query of [
      'lunar parser',
      'vendor',
      'cobalt request',
      'amber response',
      'retries use jitter',
      'bounded violet jitter',
      'indigo migration',
      'saffron rollout',
      'reliable refresh behavior',
      'quartz workflow',
      'topaz release',
      'ochre cache',
      'scarlet rollout',
    ]) {
      expect(search([row], query), query).toEqual([row]);
    }

    expect(search([row], 'silver compaction')).toEqual([]);
  });

  it('keeps remote search metadata-only even if narrative fields are present', () => {
    const remote = session({
      sourceId: 'r_0123456789abcdef',
      sourceClass: 'ssh_workspace',
      logicalSessionId: 'r_0123456789abcdef:codex:remote',
      name: 'Remote atlas repair',
      repo: 'satellite',
      branch: 'repair/telemetry',
      agent: 'codex',
      firstPrompt: 'remote-private-prompt',
      summary: 'remote-private-summary',
      declaredGoal: 'remote-private-goal',
      digest: [{ turn: 1, category: 'recap', description: 'remote-private-recap' }],
      synthesis: {
        goals: ['remote-private-synthesis'],
        outcome: '',
        keyDecisions: [],
        nextStep: '',
      },
    });

    for (const query of ['atlas', 'satellite', 'repair/telemetry', 'CODEX']) {
      expect(search([remote], query), query).toEqual([remote]);
    }
    for (const query of [
      'remote-private-prompt',
      'remote-private-summary',
      'remote-private-goal',
      'remote-private-recap',
      'remote-private-synthesis',
    ]) {
      expect(search([remote], query), query).toEqual([]);
    }
  });

  it('does not search private paths, source IDs, commands, or file paths', () => {
    const row = session({
      cwd: '/private/obsidian-vault',
      commands: ['launch-saffron-daemon'],
      fileEdits: [{ path: 'src/teal-secret.ts', adds: 1, dels: 0, edits: 1, isNew: true }],
    });

    expect(search([row], 'compiler')).toEqual([row]);
    for (const query of ['obsidian-vault', 'local', 'launch-saffron-daemon', 'teal-secret.ts']) {
      expect(search([row], query), query).toEqual([]);
    }
  });

  it('normalizes the query and reuses the document for the same session object', () => {
    const row = session({ summary: 'Original Marigold Summary' });

    expect(search([row], '  MARIGOLD  ')).toEqual([row]);
    row.summary = 'Replacement Cerulean Summary';

    expect(search([row], 'marigold')).toEqual([row]);
    expect(search([row], 'cerulean')).toEqual([]);
  });

  it('requires every word and matches one field for a prefixed word', () => {
    const row = session({ agent: 'claude', status: 'busy' });

    expect(sessionMatchesSearch(row, 'compiler', '')).toBe(true);
    expect(sessionMatchesSearch(row, 'compiler', 'fix compiler')).toBe(true);
    expect(sessionMatchesSearch(row, 'compiler', 'fix website')).toBe(false);
    for (const query of ['repo:comp', 'group:compiler', 'machine:local', 'agent:claude', 'status:active']) {
      expect(sessionMatchesSearch(row, 'compiler', query), query).toBe(true);
    }
    for (const query of ['repo:main', 'group:website', 'machine:ssh', 'agent:codex', 'status:idle']) {
      expect(sessionMatchesSearch(row, 'compiler', query), query).toBe(false);
    }
  });
});
