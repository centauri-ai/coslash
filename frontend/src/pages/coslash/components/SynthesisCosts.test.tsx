// @vitest-environment jsdom
import { act, type ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';
import { useSynthesisCosts } from '@/pages/coslash/hooks/use-synthesis-costs';
import type { SynthesisCostsResponse, SynthesisRound } from '@/pages/coslash/lib/synthesis-costs';
import {
  focusHistoryAfterLastPage,
  mergeRounds,
  pageCursor,
  SynthesisCosts,
  SynthesisCostsView,
} from './SynthesisCosts';

function costMarkup(props: Partial<ComponentProps<typeof SynthesisCostsView>>) {
  return renderToStaticMarkup(<SynthesisCostsView codingCost={1} {...props} />);
}

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
  const markup = costMarkup({ response, rounds: [round], nextCursor: 'older', expanded: true });
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
  expect(markup).not.toContain('Refresh synthesis costs');
  expect(markup).toContain('Historical usage before');
});

it('renders empty history and a request failure with focusable recovery', () => {
  const empty = costMarkup({
    codingCost: null,
    response: {
      ...response,
      historicalUnknown: false,
      totals: { ...response.totals, roundCount: 0, knownCostMicroUsd: 0 },
      rounds: [],
      nextCursor: null,
    },
    rounds: [],
    expanded: true,
  });
  expect(empty).toContain('No recorded rounds');
  expect(empty).toContain('Tracking started');
  expect(empty).not.toContain('Historical usage before');
  expect(empty).not.toContain('round-1');
  const failed = costMarkup({ codingCost: null, error: 'Storage unavailable' });
  expect(failed).toContain('role="alert"');
  expect(failed).toContain('Storage unavailable');
  expect(failed).toContain('<button');
  expect(failed).toContain('Retry synthesis costs');
});

it('labels legacy empty history unknown while preserving recorded zero and known spend', () => {
  const empty = {
    ...response.totals,
    knownCostMicroUsd: 0,
    roundCount: 0,
    invocationCount: 0,
    unknownInvocationCount: 0,
  };
  const legacy = costMarkup({ response: { ...response, totals: empty, rounds: [], nextCursor: null } });
  expect(legacy).toContain('Synthesis <strong>Unknown');
  expect(legacy).toContain('Combined known subtotal');
  const recorded = costMarkup({
    response: { ...response, totals: { ...empty, knownCostMicroUsd: 500_000, invocationCount: 1 } },
  });
  expect(recorded).toContain('≈$0.50');
  expect(recorded).toContain('Combined known subtotal');
  const zero = costMarkup({ response: { ...response, historicalUnknown: false, totals: empty } });
  expect(zero).toContain('Synthesis <strong>≈$0.00');
  expect(zero).not.toContain('Combined known subtotal');
});

it('keeps a single recovery button present through retry loading and success', () => {
  const failed = costMarkup({ error: 'Storage unavailable' });
  const pending = costMarkup({ loading: true, recovering: true });
  const recovered = costMarkup({ response, recovering: true });
  expect(failed).toContain('Retry synthesis costs');
  expect(pending).toContain('aria-disabled="true"');
  expect(pending).toContain('Loading synthesis costs');
  expect(recovered).toContain('Refresh synthesis costs');
  const firstLoad = costMarkup({ loading: true });
  expect(firstLoad).toContain('Loading synthesis costs');
  expect(firstLoad).not.toContain('<button');
});

it('shows recovery only after this session failed to load costs', () => {
  const session = { sourceId: 'local', agent: 'codex', id: 'recovering' };
  const props = {
    codingCost: 0,
    codingUnpricedModels: [],
    synthesisCostVersion: null,
    synthesisSettledCount: 0,
  };
  const state = {
    data: response,
    error: null as string | null,
    isLoading: false,
    retry: vi.fn(),
    refresh: vi.fn(),
  };
  vi.mocked(useSynthesisCosts).mockImplementation(() => state);
  const mount = document.createElement('div');
  const root = createRoot(mount);
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  try {
    act(() => root.render(<SynthesisCosts session={session} {...props} />));
    expect(mount.textContent).not.toContain('Refresh synthesis costs');
    state.error = 'Storage unavailable';
    act(() => root.render(<SynthesisCosts session={session} {...props} />));
    expect(mount.textContent).toContain('Retry synthesis costs');
    state.error = null;
    act(() => root.render(<SynthesisCosts session={session} {...props} />));
    expect(mount.textContent).toContain('Refresh synthesis costs');
    act(() => root.render(<SynthesisCosts session={{ ...session, id: 'other' }} {...props} />));
    expect(mount.textContent).not.toContain('Refresh synthesis costs');
  } finally {
    act(() => root.unmount());
    vi.unstubAllGlobals();
  }
});

it('focuses history only while the completed page control still owns focus', () => {
  const buttonNode = { isConnected: true };
  const button = buttonNode as HTMLButtonElement;
  const focus = vi.fn();
  const history = { isConnected: true, focus } as unknown as HTMLDivElement;
  focusHistoryAfterLastPage(button, history, button);
  expect(focus).toHaveBeenCalledTimes(1);
  focusHistoryAfterLastPage(button, history, {} as Element);
  buttonNode.isConnected = false;
  focusHistoryAfterLastPage(button, history, button);
  expect(focus).toHaveBeenCalledTimes(1);
});

it('keeps load-more focusable while pending and after a failed page', () => {
  const pending = costMarkup({ response, expanded: true, loadingMore: true });
  expect(pending).toContain('Loading more...');
  expect(pending).toContain('aria-disabled="true"');
  const failed = costMarkup({ response, expanded: true, pageError: 'Storage unavailable' });
  expect(failed).toContain('Retry load more');
  expect(failed).toContain('role="alert"');
});

it('uses singular invocation wording for one call and plural for other counts', () => {
  const one = costMarkup({
    response,
    rounds: [{ ...round, totals: { ...round.totals, invocationCount: 1 } }],
    expanded: true,
  });
  const many = costMarkup({ response, rounds: [round], expanded: true });
  expect(one).toContain('1 invocation');
  expect(one).not.toContain('1 invocations');
  expect(many).toContain('2 invocations');
});

it.each([
  [0, ['0 rounds recorded']],
  [1, ['1 round recorded', '1 of 1 round loaded']],
  [2, ['2 rounds recorded', '2 of 2 rounds loaded']],
] as const)('uses correct wording for %i recorded rounds', (count, expected) => {
  const markup = costMarkup({
    codingCost: 0,
    response: { ...response, historicalUnknown: false, totals: { ...response.totals, roundCount: count } },
    rounds: Array.from({ length: count }, (_, index) => ({ ...round, id: `round-${index + 1}` })),
    expanded: true,
  });
  for (const text of expected) expect(markup).toContain(text);
  if (count === 1) expect(markup).not.toContain('1 rounds');
});

it('bounds expanded history in a keyboard-scrollable region', () => {
  const markup = costMarkup({
    response,
    rounds: Array.from({ length: 20 }, (_, index) => ({ ...round, id: `round-${index}` })),
    expanded: true,
  });
  expect(markup).toContain('aria-label="Synthesis round history"');
  expect(markup).toContain('tabindex="0"');
  expect(markup).toContain('overflow-y-auto');
  expect(markup).toContain('max-h-48');
  expect(markup).toContain('Load more');
});

it('marks combined cost as a subtotal when coding has unpriced models', () => {
  const complete = {
    ...response,
    historicalUnknown: false,
    totals: { ...response.totals, unknownInvocationCount: 0 },
  };
  const partial = costMarkup({ codingUnpricedModels: ['unknown-model'], response: complete });
  expect(partial).not.toContain('unknown-model');
  expect(partial).toContain('Combined known subtotal');
  expect(
    costMarkup({ codingUnpricedModels: ['unknown-model'], response: complete, expanded: true }),
  ).toContain('unknown-model');
  const known = costMarkup({ response: complete });
  expect(known).not.toContain('Combined known subtotal');
  const unknown = costMarkup({ codingCost: null, response: complete });
  expect(unknown).toContain('Combined <strong>Unknown</strong>');
});

it('preserves terminal null cursors after the final page', () => {
  expect(pageCursor(null, response)).toBe('older');
  expect(pageCursor({ nextCursor: 'last' }, response)).toBe('last');
  expect(pageCursor({ nextCursor: null }, response)).toBeNull();
  const markup = costMarkup({ response, rounds: [round], nextCursor: null, expanded: true });
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
  const markup = costMarkup({
    codingCost: 0,
    response,
    rounds: [round, interrupted],
    expanded: true,
  });
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
