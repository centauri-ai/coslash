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
    detailRevision: '10',
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

function search(rows: Session[], query: string): Session[] {
  return filterSessionLibrary(rows, {
    search: query,
    repository: ALL_REPOSITORIES,
    source: 'all',
    shareState: 'all',
  });
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
