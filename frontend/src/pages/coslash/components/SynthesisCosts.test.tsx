import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';
import { useSynthesisCosts } from '@/pages/coslash/hooks/use-synthesis-costs';
import type { SynthesisCostsResponse, SynthesisRound } from '@/pages/coslash/lib/synthesis-costs';
import { mergeRounds, pageCursor, SynthesisCosts, SynthesisCostsView } from './SynthesisCosts';

vi.mock('@/pages/coslash/hooks/use-synthesis-costs', () => ({ useSynthesisCosts: vi.fn() }));

const round: SynthesisRound = {
  id: 'round-1',
  sourceId: 'local',
  agent: 'codex',
  sessionId: 'same',
  sourceRevision: 1,
  startedAtMs: Date.UTC(2026, 9, 1),
  finishedAtMs: Date.UTC(2026, 9, 1, 0, 1),
  outcome: 'failed',
  vendorModels: [{ vendor: 'cursor', model: 'auto' }],
  totals: {
    knownCostMicroUsd: null,
    roundCount: 1,
    invocationCount: 2,
    unknownInvocationCount: 2,
    incompleteRoundCount: 0,
  },
  tokens: {},
};
const response: SynthesisCostsResponse = {
  sourceId: 'local',
  trackingStartedAtMs: Date.UTC(2026, 8, 1),
  historicalUnknown: true,
  totals: {
    knownCostMicroUsd: 500_000,
    roundCount: 42,
    invocationCount: 50,
    unknownInvocationCount: 2,
    incompleteRoundCount: 0,
  },
  byVendor: [],
  rounds: [round],
  nextCursor: 'older',
};

it('renders lifetime spend and round details without treating unknown Cursor usage as zero', () => {
  const markup = renderToStaticMarkup(
    <SynthesisCostsView codingCost={1} response={response} rounds={[round]} nextCursor="older" expanded />,
  );
  expect(markup).toContain('Coding');
  expect(markup).toContain('Synthesis');
  expect(markup).toContain('Combined');
  expect(markup).toContain('42 rounds');
  expect(markup).toContain('≈$1.50');
  expect(markup).toContain('cursor / auto');
  expect(markup).toContain('failed');
  expect(markup).toContain('2 invocations');
  expect(markup).toContain('Unknown cost');
  expect(markup).not.toContain('≈$0.00');
  expect(markup).toContain('Load more');
  expect(markup).toContain('Refresh synthesis costs');
  expect(markup).toContain('Historical usage before');
});

it('renders empty history and a request failure with focusable recovery', () => {
  const empty = renderToStaticMarkup(
    <SynthesisCostsView
      codingCost={null}
      response={{
        ...response,
        historicalUnknown: false,
        totals: { ...response.totals, roundCount: 0, knownCostMicroUsd: 0 },
        rounds: [],
        nextCursor: null,
      }}
      rounds={[]}
      expanded
    />,
  );
  expect(empty).toContain('No recorded rounds');
  expect(empty).toContain('Tracking started');
  expect(empty).not.toContain('Historical usage before');
  expect(empty).not.toContain('round-1');
  const failed = renderToStaticMarkup(<SynthesisCostsView codingCost={null} error="Storage unavailable" />);
  expect(failed).toContain('role="alert"');
  expect(failed).toContain('Storage unavailable');
  expect(failed).toContain('<button');
  expect(failed).toContain('Retry synthesis costs');
});

it('bounds expanded history in a keyboard-scrollable region', () => {
  const markup = renderToStaticMarkup(
    <SynthesisCostsView
      codingCost={1}
      response={response}
      rounds={Array.from({ length: 20 }, (_, index) => ({ ...round, id: `round-${index}` }))}
      expanded
    />,
  );
  expect(markup).toContain('aria-label="Synthesis round history"');
  expect(markup).toContain('tabindex="0"');
  expect(markup).toContain('overflow-y-auto');
  expect(markup).toContain('max-h-48');
  expect(markup).toContain('Load more');
});

it('marks combined cost as a subtotal when coding has unpriced models', () => {
  const complete = { ...response, totals: { ...response.totals, unknownInvocationCount: 0 } };
  const partial = renderToStaticMarkup(
    <SynthesisCostsView codingCost={1} codingUnpricedModels={['unknown-model']} response={complete} />,
  );
  expect(partial).toContain('unknown-model');
  expect(partial).toContain('Combined known subtotal');
  const known = renderToStaticMarkup(<SynthesisCostsView codingCost={1} response={complete} />);
  expect(known).not.toContain('Combined known subtotal');
  const unknown = renderToStaticMarkup(<SynthesisCostsView codingCost={null} response={complete} />);
  expect(unknown).toContain('Combined <strong>Unknown</strong>');
});

it('preserves terminal null cursors after the final page', () => {
  expect(pageCursor(null, response)).toBe('older');
  expect(pageCursor({ nextCursor: 'last' }, response)).toBe('last');
  expect(pageCursor({ nextCursor: null }, response)).toBeNull();
  const markup = renderToStaticMarkup(
    <SynthesisCostsView codingCost={1} response={response} rounds={[round]} nextCursor={null} expanded />,
  );
  expect(markup).not.toContain('Load more');
});

it('keeps lifetime totals separate from deduplicated page rows', () => {
  const interrupted = {
    ...round,
    id: 'round-2',
    outcome: 'interrupted',
    tokens: {
      'claude-sonnet': {
        input_tokens: 1000,
        output_tokens: 2000,
        cache_creation_input_tokens: 0,
        cache_creation_1h_input_tokens: 0,
        cache_read_input_tokens: 0,
      },
    },
  } satisfies SynthesisRound;
  expect(mergeRounds([round], [round, interrupted, interrupted]).map(({ id }) => id)).toEqual([
    'round-1',
    'round-2',
  ]);
  const markup = renderToStaticMarkup(
    <SynthesisCostsView codingCost={0} response={response} rounds={[round, interrupted]} expanded />,
  );
  expect(markup).toContain('42 rounds');
  expect(markup).toContain('2 of 42 rounds');
  expect(markup).toContain('interrupted');
  expect(markup).toContain('in 1k');
});

it('queries composite identity and explains non-local accounting', () => {
  vi.mocked(useSynthesisCosts).mockReturnValue({
    data: response,
    error: null,
    isLoading: false,
    retry: vi.fn(),
    refresh: vi.fn(),
  });
  renderToStaticMarkup(
    <SynthesisCosts
      session={{ sourceId: 'local', agent: 'codex', id: 'same' }}
      codingCost={0}
      codingUnpricedModels={[]}
      synthesisCostVersion={null}
      synthesisSettledCount={0}
    />,
  );
  expect(useSynthesisCosts).toHaveBeenCalledWith({ sourceId: 'local', agent: 'codex', id: 'same' });
  const remote = renderToStaticMarkup(
    <SynthesisCosts
      session={{ sourceId: 'remote', agent: 'codex', id: 'same' }}
      codingCost={0}
      codingUnpricedModels={[]}
      synthesisCostVersion={null}
      synthesisSettledCount={0}
    />,
  );
  expect(remote).toContain('Non-local synthesis accounting is unavailable');
});
