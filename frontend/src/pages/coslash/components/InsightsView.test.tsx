import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, expect, it, vi } from 'vitest';
import { useSynthesisCosts } from '@/pages/coslash/hooks/use-synthesis-costs';
import type { Session } from '@/pages/coslash/lib/session';
import { InsightsView } from './InsightsView';

vi.mock('@/pages/coslash/hooks/use-synthesis-costs', () => ({ useSynthesisCosts: vi.fn() }));

const totals = {
  knownCostMicroUsd: 500_000,
  roundCount: 1,
  invocationCount: 2,
  unknownInvocationCount: 1,
  incompleteRoundCount: 1,
};

function render(sessions: Session[] = []) {
  return renderToStaticMarkup(
    <InsightsView
      sessions={sessions}
      isLoading={false}
      loadError={null}
      synthesisCostVersion={null}
      onRetry={() => {}}
    />,
  );
}

afterEach(() => vi.useRealTimers());

it('shows synthesis in a month with no coding sessions and labels both scopes', () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 11, 31, 23));
  vi.mocked(useSynthesisCosts).mockReturnValue({
    data: {
      sourceId: 'local',
      trackingStartedAtMs: new Date(2026, 11, 1).getTime(),
      historicalUnknown: true,
      totals,
      byVendor: [{ vendor: 'claude', totals }],
      rounds: [],
      nextCursor: null,
    },
    isLoading: false,
    error: null,
    retry: () => {},
    refresh: () => {},
  });
  const markup = render();
  expect(useSynthesisCosts).toHaveBeenCalledWith({
    since: new Date(2026, 11, 1).getTime(),
    until: new Date(2027, 0, 1).getTime(),
  });
  expect(markup).toContain('Coding');
  expect(markup).toContain('Synthesis');
  expect(markup).toContain('Combined');
  expect(markup).toContain('calls started this month');
  expect(markup).toContain('sessions last active this month');
  expect(markup).toContain('claude');
  expect(markup).toContain('SSH/Hub');
  expect(markup).toContain('Partial');
  expect(markup).toContain('≈$0.50');
});

it('keeps coding insights and an accounting retry visible on storage failure', () => {
  vi.mocked(useSynthesisCosts).mockReturnValue({
    data: null,
    isLoading: false,
    error: 'Storage unavailable',
    retry: () => {},
    refresh: () => {},
  });
  const markup = render();
  expect(markup).toContain('Agents used');
  expect(markup).toContain('Storage unavailable');
  expect(markup).toContain('Retry synthesis costs');
  expect(markup).toContain('role="alert"');
});

it('shows unknown legacy synthesis and a combined known subtotal', () => {
  vi.mocked(useSynthesisCosts).mockReturnValue({
    data: {
      sourceId: 'local',
      trackingStartedAtMs: 0,
      historicalUnknown: true,
      totals: {
        knownCostMicroUsd: 0,
        roundCount: 0,
        invocationCount: 0,
        unknownInvocationCount: 0,
        incompleteRoundCount: 0,
      },
      byVendor: [],
      rounds: [],
      nextCursor: null,
    },
    isLoading: false,
    error: null,
    retry: () => {},
    refresh: () => {},
  });
  const markup = render();
  expect(markup).toContain('<dt>Synthesis</dt><dd class="tabular-nums">Unknown</dd>');
  expect(markup).toContain('earlier history is unknown');
  expect(markup).toContain('Combined known subtotal');
});

it('keeps the synthesis recovery control present while retrying', () => {
  vi.mocked(useSynthesisCosts).mockReturnValue({
    data: null,
    isLoading: true,
    error: null,
    retry: () => {},
    refresh: () => {},
  });
  const markup = render();
  expect(markup).toContain('aria-disabled="true"');
  expect(markup).toContain('Loading synthesis costs');
});

it('labels combined known subtotal when coding alone has unpriced usage', () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 9, 2));
  const session = {
    sourceId: 'local',
    agent: 'codex',
    id: 'one',
    logicalSessionId: 'local:codex:one',
    revision: 1,
    eligibleForAggregates: true,
    mtime: new Date(2026, 9, 1).getTime(),
    repo: 'app',
    tokens: {},
    observedModels: [],
    model: 'gpt-5',
    cost: 1,
    unpricedModels: ['unpriced'],
  } as unknown as Session;
  const complete = {
    sourceId: 'local',
    trackingStartedAtMs: 0,
    historicalUnknown: false,
    totals: {
      knownCostMicroUsd: 500_000,
      roundCount: 1,
      invocationCount: 1,
      unknownInvocationCount: 0,
      incompleteRoundCount: 0,
    },
    byVendor: [],
    rounds: [],
    nextCursor: null,
  };
  vi.mocked(useSynthesisCosts).mockReturnValue({
    data: complete,
    isLoading: false,
    error: null,
    retry: () => {},
    refresh: () => {},
  });
  const partial = render([session]);
  expect(partial).toContain('Combined known subtotal');
  expect(partial).toContain('≈$1.50');
  expect(partial).toContain('Coding excludes unpriced usage in 1 session');

  expect(render([{ ...session, unpricedModels: [] }])).not.toContain('Combined known subtotal');

  vi.mocked(useSynthesisCosts).mockReturnValue({
    data: null,
    isLoading: false,
    error: 'Storage unavailable',
    retry: () => {},
    refresh: () => {},
  });
  const unavailable = render([session]);
  expect(unavailable).toContain('Unavailable');
  expect(unavailable).not.toContain('Combined known subtotal');
});
