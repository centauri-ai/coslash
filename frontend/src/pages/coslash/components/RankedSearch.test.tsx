/// <reference types="node" />
import { type ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { buildReviewIndex } from '@/pages/coslash/lib/review';
import type { SearchResults } from '@/pages/coslash/lib/search';
import { type Session } from '@/pages/coslash/lib/session';

const props: ComponentProps<typeof CoslashLayout> = {
  sessions: [],
  reviewIndex: buildReviewIndex([]),
  latestHandoffs: new Map(),
  onOpenHandoffTarget: () => {},
  machines: [
    { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
    { sourceId: 'remote', label: 'agent-box', state: 'ok', complete: true },
  ],
  range: 'all',
  onRangeChange: () => {},
  onViewChange: () => {},
  selectedSessionKey: null,
  onSelectSession: () => {},
  diagnostics: null,
  onSettings: () => {},
  theme: 'light',
  onThemeChange: () => {},
  themeDisabled: false,
  onRetry: () => {},
  onRetrySessions: () => {},
  retrying: false,
  isLoading: false,
  loadError: null,
  synthesisCostVersion: null,
  reviewerOptions: [
    { id: 'claude', label: 'Claude Code', available: true },
    { id: 'codex', label: 'Codex', available: false },
    { id: 'opencode', label: 'OpenCode', available: false },
  ],
  remoteReviewerOptions: [],
  canRetryRemoteReviewers: false,
  onRetryRemoteReviewers: () => {},
  onReviewStarted: () => {},
};

function renderLayout(overrides: Partial<ComponentProps<typeof CoslashLayout>> = {}) {
  const sessions = overrides.sessions ?? props.sessions;
  return renderToStaticMarkup(
    <CoslashLayout {...props} {...overrides} reviewIndex={buildReviewIndex(sessions)} />,
  );
}

function session(overrides: Partial<Session>): Session {
  return {
    sourceId: 'remote',
    agent: 'codex',
    cwd: '/workspace/app',
    status: null,
    mtime: 0,
    tokens: {
      'gpt-5': {
        input_tokens: 1000,
        output_tokens: 0,
        cache_creation_input_tokens: 0,
        cache_creation_1h_input_tokens: 0,
        cache_read_input_tokens: 0,
      },
    },
    cost: 1,
    unpricedModels: [],
    eligibleForAggregates: true,
    displayStale: false,
    name: null,
    firstPrompt: null,
    ...overrides,
  } as Session;
}

const state = vi.hoisted(() => ({ results: null as SearchResults | null, state: 'ready' }));
vi.mock('@/pages/coslash/lib/search', async (original) => ({
  ...(await original<object>()),
  useRankedSearch: () => state,
}));
afterEach(() => {
  vi.unstubAllGlobals();
});
function scope(agentFilters: string[] = []) {
  vi.stubGlobal('sessionStorage', {
    getItem: () => JSON.stringify({ query: 'queue', agentFilters }),
    setItem: () => {},
  });
}
function hit(sessionId: string, agent: string, score: number) {
  return {
    sourceId: 'local',
    agent,
    sessionId,
    score,
    coverage: 'full',
    match: { kind: 'prompt', turn: 12, snippet: 'Use queue', ranges: [[4, 9]] as [number, number][] },
  };
}
describe('ranked list', () => {
  it('applies facets before display limits and keeps old matches and decisions', () => {
    scope(['codex']);
    const sessions = Array.from({ length: 60 }, (_, i) =>
      session({
        sourceId: 'local',
        id: String(i),
        agent: i === 59 ? 'codex' : 'claude',
        name: `Answer ${i}`,
        mtime: 1,
        digest: [],
      }),
    );
    state.results = {
      indexState: 'ready',
      truncated: false,
      sessions: sessions.map((s, i) => hit(s.id, s.agent, 100 - i)),
      decisions: [
        {
          sourceId: 'local',
          agent: 'codex',
          sessionId: '59',
          kind: 'decision',
          status: 'decided',
          text: 'Keep queue isolation',
          turns: [],
          cited: false,
          score: 1,
        },
      ],
    };
    const html = renderLayout({ sessions, range: 'today' });
    expect(html).toContain('Answer 59');
    expect(html).not.toContain('Answer 58');
    expect(html).toContain('Keep queue isolation');
    expect(html).toContain('prompt, turn 12');
    expect(html).toContain('<mark>queue</mark>');
    expect(html).toContain('All time (search)');
    scope(['claude']);
    expect(renderLayout({ sessions, range: 'today' })).not.toContain('Keep queue isolation');
  });
  it('keeps rank order across status groups and adds only unindexed SSH metadata matches', () => {
    scope();
    const sessions = [
      session({ id: 'older', sourceId: 'local', name: 'Older answer', mtime: 1, digest: [] }),
      session({ id: 'newer', sourceId: 'local', name: 'New answer', mtime: 2, digest: [] }),
      session({
        id: 'remote',
        sourceId: 'ssh',
        name: 'queue on SSH',
        sourceLabel: 'Remote machine',
        digest: [],
      }),
    ];
    state.results = {
      indexState: 'ready',
      truncated: false,
      sessions: [hit('older', 'codex', 9), hit('newer', 'codex', 2)],
      decisions: [],
      indexed: [],
    };
    let html = renderLayout({ sessions });
    expect(html.indexOf('Older answer')).toBeLessThan(html.indexOf('New answer'));
    expect(html).toContain('queue on SSH');
    state.results.indexed = [{ sourceId: 'ssh', agent: 'codex', sessionId: 'remote' }];
    html = renderLayout({ sessions });
    expect(html).not.toContain('queue on SSH');
  });
});
