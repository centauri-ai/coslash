import { useEffect, useRef, useState } from 'react';
import { ChevronDownIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { TokenBreakdown } from '@/pages/coslash/components/SessionCard';
import { loadSynthesisCosts, useSynthesisCosts } from '@/pages/coslash/hooks/use-synthesis-costs';
import { formatEstimatedCost } from '@/pages/coslash/lib/format';
import { isLocalSession, sessionKey, type SessionIdentity } from '@/pages/coslash/lib/session';
import {
  combinedKnownCost,
  synthesisCostVersionChanged,
  synthesisCoverage,
  type SynthesisCostsResponse,
  type SynthesisRound,
} from '@/pages/coslash/lib/synthesis-costs';

// oxlint-disable-next-line react/only-export-components -- pure pagination merge used by focused tests
export function mergeRounds(previous: SynthesisRound[], next: SynthesisRound[]): SynthesisRound[] {
  const seen = new Set(previous.map(({ id }) => id));
  return [
    ...previous,
    ...next.filter(({ id }) => {
      if (seen.has(id)) return false;
      seen.add(id);
      return true;
    }),
  ];
}

// oxlint-disable-next-line react/only-export-components -- pure cursor boundary used by focused tests
export function pageCursor(
  page: Pick<SynthesisCostsResponse, 'nextCursor'> | null,
  first: Pick<SynthesisCostsResponse, 'nextCursor'> | null,
): string | null {
  return page ? page.nextCursor : (first?.nextCursor ?? null);
}

// oxlint-disable-next-line react/only-export-components -- focus boundary used by focused tests
export function focusHistoryAfterLastPage(
  button: HTMLButtonElement,
  history: HTMLDivElement,
  activeElement: Element | null,
) {
  if (button.isConnected && history.isConnected && activeElement === button) history.focus();
}

function CostAmount({ microUsd }: { microUsd: number | null }) {
  return <>{microUsd == null ? 'Unknown cost' : formatEstimatedCost(microUsd / 1_000_000)}</>;
}

export function SynthesisCostsView({
  codingCost,
  codingUnpricedModels = [],
  response,
  rounds = response?.rounds ?? [],
  nextCursor = response?.nextCursor ?? null,
  expanded = false,
  loading = false,
  loadingMore = false,
  error = null,
  pageError = null,
  recovering = false,
  onRetry,
  onLoadMore,
  onToggle,
}: {
  codingCost: number | null;
  codingUnpricedModels?: string[];
  response?: SynthesisCostsResponse | null;
  rounds?: SynthesisRound[];
  nextCursor?: string | null;
  expanded?: boolean;
  loading?: boolean;
  loadingMore?: boolean;
  error?: string | null;
  pageError?: string | null;
  recovering?: boolean;
  onRetry?: () => void;
  onLoadMore?: (button: HTMLButtonElement, history: HTMLDivElement) => void;
  onToggle?: (expanded: boolean) => void;
}) {
  const historyRef = useRef<HTMLDivElement>(null);
  const totals = response?.totals;
  const combined =
    totals == null || codingCost == null
      ? null
      : combinedKnownCost(codingCost, totals, response?.historicalUnknown);
  const coverage = totals == null ? null : synthesisCoverage(totals, response?.historicalUnknown);
  const codingPartial = codingUnpricedModels.length > 0;
  const detailsAvailable = response != null || codingPartial;
  return (
    <div className="bg-coslash-soft min-w-0 rounded-lg border px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap gap-x-5 gap-y-1 font-mono">
          <span>
            Coding <strong>{codingCost == null ? 'Unknown' : formatEstimatedCost(codingCost)}</strong>
          </span>
          <span>
            Synthesis{' '}
            <strong>
              {coverage === 'unknown' ? (
                'Unknown'
              ) : totals ? (
                <CostAmount microUsd={totals.knownCostMicroUsd} />
              ) : (
                'Unknown'
              )}
            </strong>
          </span>
          <span>
            {combined?.partial || codingPartial ? 'Combined known subtotal' : 'Combined'}{' '}
            <strong>{combined ? formatEstimatedCost(combined.knownUsd) : 'Unknown'}</strong>
          </span>
        </div>
        {detailsAvailable && (
          <button
            type="button"
            aria-expanded={expanded}
            className="text-brand flex cursor-pointer items-center gap-1"
            onClick={() => onToggle?.(!expanded)}
          >
            Details
            <ChevronDownIcon className={cn('size-3 transition-transform', { 'rotate-180': expanded })} />
          </button>
        )}
      </div>
      {loading && (
        <p role="status" className="text-coslash-muted pt-2">
          Loading synthesis costs...
        </p>
      )}
      {error && (
        <p role="alert" className="pt-2">
          Synthesis costs unavailable: {error}
        </p>
      )}
      {(error || recovering) && (
        <div className="pt-2">
          <Button variant="outline" size="sm" aria-disabled={loading} onClick={loading ? undefined : onRetry}>
            {loading
              ? 'Loading synthesis costs...'
              : error
                ? 'Retry synthesis costs'
                : 'Refresh synthesis costs'}
          </Button>
        </div>
      )}
      {expanded && codingPartial && (
        <p className="text-coslash-muted">
          Coding known subtotal excludes unpriced models: {codingUnpricedModels.join(', ')}.
        </p>
      )}
      {expanded && response && (
        <div className="flex flex-col gap-1 pt-2">
          <p className="text-coslash-muted">
            {totals?.roundCount} {totals?.roundCount === 1 ? 'round' : 'rounds'} recorded
            {coverage !== 'complete' && ` · ${coverage === 'partial' ? 'Partial cost' : 'Unknown cost'}`}
          </p>
          <p className="text-coslash-muted">
            Tracking started {new Date(response.trackingStartedAtMs).toLocaleDateString()}.
            {response.historicalUnknown && ' Historical usage before tracking is unavailable.'}
          </p>
          <div
            ref={historyRef}
            role="region"
            aria-label="Synthesis round history"
            tabIndex={0}
            className="max-h-48 min-w-0 overflow-y-auto overscroll-contain pt-2 sm:max-h-64"
          >
            {totals?.roundCount === 0 ? (
              <p className="text-coslash-muted">No recorded rounds</p>
            ) : (
              <>
                <p className="text-coslash-muted pb-2">
                  {rounds.length} of {totals?.roundCount} {totals?.roundCount === 1 ? 'round' : 'rounds'}{' '}
                  loaded
                </p>
                <div className="flex flex-col gap-2">
                  {rounds.map((round) => (
                    <div key={round.id} className="border-coslash-line min-w-0 rounded border p-2">
                      <div className="flex flex-wrap justify-between gap-1">
                        <span>
                          {new Date(round.startedAtMs).toLocaleString()} · {round.outcome}
                        </span>
                        <strong>
                          <CostAmount microUsd={round.totals.knownCostMicroUsd} />
                        </strong>
                      </div>
                      <p className="text-coslash-muted min-w-0 pt-1 wrap-break-word">
                        {round.vendorModels.map(({ vendor, model }) => `${vendor} / ${model}`).join(', ') ||
                          'Unknown vendor / model'}
                        {' · '}
                        {round.totals.invocationCount}{' '}
                        {round.totals.invocationCount === 1 ? 'invocation' : 'invocations'}
                        {synthesisCoverage(round.totals) !== 'complete' &&
                          ` · ${synthesisCoverage(round.totals)} cost`}
                      </p>
                      {Object.keys(round.tokens).length > 0 && <TokenBreakdown tokens={round.tokens} />}
                    </div>
                  ))}
                </div>
                {pageError && (
                  <p role="alert" className="pt-2">
                    Could not load more rounds: {pageError}
                  </p>
                )}
                {nextCursor && (
                  <Button
                    variant="outline"
                    size="sm"
                    className="mt-2"
                    onClick={(event) => {
                      if (!loadingMore && historyRef.current)
                        onLoadMore?.(event.currentTarget, historyRef.current);
                    }}
                    aria-disabled={loadingMore}
                  >
                    {loadingMore ? 'Loading more...' : pageError ? 'Retry load more' : 'Load more'}
                  </Button>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export function SynthesisCosts({
  session,
  codingCost,
  codingUnpricedModels,
  synthesisCostVersion,
  synthesisSettledCount,
}: {
  session: SessionIdentity;
  codingCost: number | null;
  codingUnpricedModels: string[];
  synthesisCostVersion: string | null;
  synthesisSettledCount: number;
}) {
  const local = isLocalSession(session);
  const costs = useSynthesisCosts(local ? session : null);
  const refreshCosts = costs.refresh;
  const identity = sessionKey(session);
  // Costs refresh on their own; the button appears only after a failure and
  // then stays, so recovery does not drop keyboard focus.
  const [recoveryFor, setRecoveryFor] = useState<string | null>(null);
  if (costs.error && recoveryFor !== identity) setRecoveryFor(identity);
  const [expanded, setExpanded] = useState(false);
  const [page, setPage] = useState<{
    response: SynthesisCostsResponse;
    rounds: SynthesisRound[];
    nextCursor: string | null;
    error: string | null;
    loading: boolean;
  } | null>(null);
  const pageRequest = useRef<AbortController | null>(null);
  const previousRefresh = useRef({ version: synthesisCostVersion, settled: synthesisSettledCount });

  useEffect(() => {
    const previous = previousRefresh.current;
    if (
      local &&
      (synthesisCostVersionChanged(previous.version, synthesisCostVersion) ||
        previous.settled !== synthesisSettledCount)
    ) {
      pageRequest.current?.abort();
      refreshCosts();
    }
    previousRefresh.current = { version: synthesisCostVersion, settled: synthesisSettledCount };
  }, [local, synthesisCostVersion, synthesisSettledCount, refreshCosts]);

  useEffect(() => {
    pageRequest.current?.abort();
    return () => pageRequest.current?.abort();
  }, [session.sourceId, session.agent, session.id, costs.data]);

  if (!local) {
    return <p className="text-coslash-muted pt-2 text-xs">Non-local synthesis accounting is unavailable.</p>;
  }

  const current = page?.response === costs.data ? page : null;
  const retry = () => {
    pageRequest.current?.abort();
    costs.retry();
  };
  const loadMore = async (button: HTMLButtonElement, history: HTMLDivElement) => {
    const cursor = pageCursor(current, costs.data);
    if (!cursor || !costs.data || (pageRequest.current && !pageRequest.current.signal.aborted)) return;
    const controller = new AbortController();
    pageRequest.current = controller;
    setPage({
      response: costs.data,
      rounds: current?.rounds ?? costs.data.rounds,
      nextCursor: cursor,
      error: null,
      loading: true,
    });
    try {
      const next = await loadSynthesisCosts({ ...session, cursor }, controller.signal);
      if (controller.signal.aborted) return;
      if (next.nextCursor === null && pageRequest.current === controller)
        focusHistoryAfterLastPage(button, history, document.activeElement);
      setPage((previous) =>
        previous?.response === costs.data
          ? {
              response: costs.data,
              rounds: mergeRounds(previous.rounds, next.rounds),
              nextCursor: next.nextCursor,
              error: null,
              loading: false,
            }
          : previous,
      );
    } catch (error: unknown) {
      if (!controller.signal.aborted)
        setPage((previous) =>
          previous?.response === costs.data
            ? {
                ...previous,
                error: error instanceof Error ? error.message : 'Request failed',
                loading: false,
              }
            : previous,
        );
    } finally {
      if (pageRequest.current === controller) pageRequest.current = null;
    }
  };

  return (
    <SynthesisCostsView
      codingCost={codingCost}
      codingUnpricedModels={codingUnpricedModels}
      response={costs.data}
      rounds={current?.rounds ?? costs.data?.rounds}
      nextCursor={pageCursor(current, costs.data)}
      expanded={expanded}
      loading={costs.isLoading}
      loadingMore={current?.loading}
      error={costs.error}
      pageError={current?.error}
      recovering={recoveryFor === identity}
      onRetry={retry}
      onLoadMore={(button, history) => void loadMore(button, history)}
      onToggle={setExpanded}
    />
  );
}
