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
