import { useEffect, useMemo, useRef, useState } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { useSynthesisCosts } from '@/pages/coslash/hooks/use-synthesis-costs';
import { formatEstimatedCost } from '@/pages/coslash/lib/format';
import { buildInsights } from '@/pages/coslash/lib/insights';
import type { Session } from '@/pages/coslash/lib/session';
import {
  combinedKnownCost,
  synthesisCostVersionChanged,
  synthesisCoverage,
} from '@/pages/coslash/lib/synthesis-costs';

const COLORS = [
  'var(--brand)',
  'var(--opencode)',
  'var(--claude)',
  'var(--info)',
  'var(--warning)',
  'var(--coslash-muted)',
];
const AGENT_COLORS: Record<string, string> = {
  'Claude Code': 'var(--claude)',
  'Codex': 'var(--codex)',
  'OpenCode': 'var(--opencode)',
  'Cursor': 'var(--cursor)',
  'Pi': 'var(--pi)',
};

function PieChart({
  title,
  entries,
  colors = COLORS,
  caption,
}: {
  title: string;
  entries: { name: string; count: number }[];
  colors?: string[];
  caption: string;
}) {
  const top = entries.slice(0, 5);
  const rest = entries.slice(5).reduce((sum, entry) => sum + entry.count, 0);
  const slices = rest ? [...top, { name: 'Other', count: rest }] : top;
  const total = entries.reduce((sum, entry) => sum + entry.count, 0);
  const gradient = slices.reduce<{ cursor: number; stops: string[] }>(
    ({ cursor, stops }, entry, index) => {
      const end = cursor + (entry.count / total) * 100;
      return {
        cursor: end,
        stops: [...stops, `${colors[index % colors.length]} ${cursor}% ${end}%`],
      };
    },
    { cursor: 0, stops: [] },
  ).stops;

  return (
    <div className="border-coslash-line bg-coslash-surface min-w-0 rounded-xl border p-5">
      <h2 className="text-ui font-semibold">{title}</h2>
      {total === 0 ? (
        <p className="text-coslash-muted flex min-h-52 items-center justify-center text-sm">
          No sessions this month
        </p>
      ) : (
        <div className="flex min-h-52 flex-wrap items-center justify-center gap-8 pt-5">
          <div
            className="size-40 shrink-0 rounded-full sm:size-44"
            style={{ background: `conic-gradient(${gradient.join(', ')})` }}
            role="img"
            aria-label={`${title} distribution`}
          />
          <ul className="min-w-0 space-y-2 text-sm">
            {slices.map((entry, index) => (
              <li key={entry.name} className="flex min-w-0 items-center gap-2">
                <span
                  className="size-2.5 shrink-0 rounded-full"
                  style={{ background: colors[index % colors.length] }}
                />
                <span className="min-w-0 truncate" title={entry.name}>
                  {entry.name}
                </span>
                <span className="text-coslash-muted tabular-nums">
                  {Math.round((entry.count / total) * 100)}%
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      <p className="text-coslash-muted pt-3 text-xs">{caption}</p>
    </div>
  );
}

export function InsightsView({
  sessions,
  isLoading,
  loadError,
  synthesisCostVersion,
  onRetry,
  inPageFlow = false,
}: {
  sessions: Session[];
  isLoading: boolean;
  loadError: string | null;
  synthesisCostVersion: string | null;
  onRetry: () => void;
  inPageFlow?: boolean;
}) {
  const [month, setMonth] = useState(() => new Date(new Date().getFullYear(), new Date().getMonth(), 1));
  const current = new Date();
  const nextDisabled =
    month.getFullYear() === current.getFullYear() && month.getMonth() === current.getMonth();
  const insights = useMemo(() => buildInsights(sessions, month), [sessions, month]);
  const query = useMemo(
    () => ({
      since: month.getTime(),
      until: new Date(month.getFullYear(), month.getMonth() + 1, 1).getTime(),
    }),
    [month],
  );
  const costs = useSynthesisCosts(query);
  const refreshCosts = costs.refresh;
  const previousCostVersion = useRef(synthesisCostVersion);
  useEffect(() => {
    if (synthesisCostVersionChanged(previousCostVersion.current, synthesisCostVersion)) {
      refreshCosts();
    }
    previousCostVersion.current = synthesisCostVersion;
  }, [synthesisCostVersion, refreshCosts]);
  const synthesis = costs.data?.totals;
  const combined = synthesis ? combinedKnownCost(insights.knownCost, synthesis) : null;
  const synthesisStatus = synthesis ? synthesisCoverage(synthesis) : null;
  const maxDay = Math.max(1, ...insights.days.map(({ count }) => count));
  const maxRepo = insights.repositories[0]?.count ?? 1;

  return (
    <div
      className={cn('flex min-h-0 flex-col gap-4 pb-4', {
        'flex-none overflow-visible': inPageFlow,
        'flex-1 overflow-y-auto': !inPageFlow,
      })}
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">Insights</h1>
          <p className="text-coslash-muted text-xs">Sessions are grouped by their last active date</p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="Previous month"
            onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}
          >
            <ChevronLeft aria-hidden="true" />
          </Button>
          <span className="min-w-32 text-center text-sm font-semibold" aria-live="polite">
            {month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })}
          </span>
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="Next month"
            disabled={nextDisabled}
            onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}
          >
            <ChevronRight aria-hidden="true" />
          </Button>
        </div>
      </div>

      {loadError ? (
        <div role="alert" className="border-coslash-line bg-coslash-surface rounded-xl border p-6 text-sm">
          <p>Sessions could not be loaded: {loadError}</p>
          <Button variant="outline" className="mt-3" onClick={onRetry}>
            Try again
          </Button>
        </div>
      ) : isLoading ? (
        <p className="text-coslash-muted p-6 text-sm" role="status">
          Loading insights...
        </p>
      ) : (
        <>
          <div className="grid gap-4 lg:grid-cols-2">
            <PieChart
              title="Agents used"
              entries={insights.agents}
              colors={insights.agents.map(({ name }) => AGENT_COLORS[name] ?? 'var(--coslash-muted)')}
              caption="Share of sessions by agent."
            />
            <PieChart
              title="Models used"
              entries={insights.models}
              caption="Share of model uses, counting each model once per session."
            />
          </div>
          <div className="grid gap-4 lg:grid-cols-[2fr_1fr]">
            <div className="border-coslash-line bg-coslash-surface min-w-0 rounded-xl border p-5">
              <h2 className="text-ui font-semibold">Top repositories</h2>
              {insights.repositories.length === 0 ? (
                <p className="text-coslash-muted py-8 text-sm">No repositories this month</p>
              ) : (
                <div className="mt-4 space-y-2">
                  {insights.repositories.map((repo, index) => (
                    <div
                      key={repo.name}
                      className="grid grid-cols-[1.5rem_minmax(0,1fr)_3rem] items-center gap-3 text-sm"
                    >
                      <span className="text-coslash-muted tabular-nums">{index + 1}</span>
                      <div className="min-w-0">
                        <span className="block truncate" title={repo.name}>
                          {repo.name}
                        </span>
                        <div className="bg-coslash-soft mt-1 h-1.5 overflow-hidden rounded-full">
                          <div
                            className="bg-brand h-full rounded-full"
                            style={{ width: `${(repo.count / maxRepo) * 100}%` }}
                          />
                        </div>
                      </div>
                      <span className="text-right tabular-nums">{repo.count}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
            <div className="border-coslash-line bg-coslash-surface rounded-xl border p-5">
              <h2 className="text-ui font-semibold">Estimated cost for selected month</h2>
              <dl className="space-y-3 pt-4 text-sm">
                <div className="flex items-center justify-between gap-3">
                  <dt>Coding</dt>
                  <dd className="tabular-nums">
                    {insights.sessionCount ? formatEstimatedCost(insights.knownCost) : 'No sessions'}
                  </dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt>Synthesis</dt>
                  <dd className="tabular-nums">
                    {costs.isLoading
                      ? 'Loading'
                      : synthesis?.knownCostMicroUsd == null
                        ? 'Unknown'
                        : formatEstimatedCost(synthesis.knownCostMicroUsd / 1_000_000)}
                  </dd>
                </div>
                <div className="border-coslash-line flex items-center justify-between gap-3 border-t pt-3 font-semibold">
                  <dt>Combined</dt>
                  <dd className="tabular-nums">
                    {combined ? formatEstimatedCost(combined.knownUsd) : 'Unavailable'}
                  </dd>
                </div>
              </dl>
              <p className="text-coslash-muted pt-3 text-xs">
                Coding is lifetime cost of sessions last active this month. Synthesis is recorded cost of
                calls started this month. Combined adds these two scopes; it is not billable coding activity
                during this month.
              </p>
              {insights.unknownCostCount > 0 && (
                <p className="text-warning-fg pt-2 text-xs">
                  Coding excludes unpriced usage in {insights.unknownCostCount}{' '}
                  {insights.unknownCostCount === 1 ? 'session' : 'sessions'}.
                </p>
              )}
              {costs.error && (
                <div role="alert" className="text-warning-fg pt-3 text-xs">
                  Synthesis and combined costs unavailable: {costs.error}{' '}
                  <Button variant="outline" size="sm" onClick={costs.retry}>
                    Retry synthesis costs
                  </Button>
                </div>
              )}
              {costs.data && (
                <>
                  <p className="text-coslash-muted pt-2 text-xs">
                    Local synthesis tracking started{' '}
                    {new Date(costs.data.trackingStartedAtMs).toLocaleString()}. SSH/Hub synthesis is not
                    tracked here.
                  </p>
                  {(synthesisStatus !== 'complete' ||
                    costs.data.historicalUnknown ||
                    insights.unknownCostCount > 0) && (
                    <p className="text-warning-fg pt-2 text-xs">
                      {synthesisStatus === 'unknown' ? 'Unknown synthesis cost' : 'Partial coverage'}
                      {synthesis && synthesis.unknownInvocationCount > 0
                        ? `: ${synthesis.unknownInvocationCount} unknown or partially priced calls`
                        : ''}
                      {synthesis && synthesis.incompleteRoundCount > 0
                        ? `; ${synthesis.incompleteRoundCount} incomplete rounds`
                        : ''}
                      {costs.data.historicalUnknown ? '; earlier history is unknown' : ''}.
                    </p>
                  )}
                  {costs.data.byVendor.length > 0 && (
                    <div className="pt-3 text-xs">
                      <h3 className="font-semibold">Synthesis by vendor</h3>
                      <ul className="space-y-1 pt-2">
                        {costs.data.byVendor.map(({ vendor, totals }) => (
                          <li key={vendor} className="flex justify-between gap-2">
                            <span className="capitalize">{vendor}</span>
                            <span>
                              {totals.knownCostMicroUsd == null
                                ? 'Unknown'
                                : formatEstimatedCost(totals.knownCostMicroUsd / 1_000_000)}
                              {synthesisCoverage(totals) !== 'complete' ? ' (partial)' : ''}
                            </span>
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </>
              )}
              <Button variant="outline" size="sm" className="mt-3" onClick={costs.refresh}>
                Refresh synthesis costs
              </Button>
            </div>
          </div>
          <div className="border-coslash-line bg-coslash-surface min-w-0 rounded-xl border p-5">
            <h2 className="text-ui font-semibold">Sessions last active per day</h2>
            <div className="mt-6 overflow-x-auto">
              <div
                className="flex min-w-[600px] items-end gap-1"
                role="img"
                aria-label={`Sessions last active per day in ${month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })}: ${insights.days.map(({ day, count }) => `${day}: ${count}`).join(', ')}`}
              >
                {insights.days.map(({ day, count }) => (
                  <div key={day} className="flex min-w-0 flex-1 flex-col items-center gap-1 text-center">
                    <span className="text-coslash-muted text-[10px] tabular-nums">{count || ''}</span>
                    <div className="flex h-40 w-full items-end">
                      <div
                        className="bg-brand w-full rounded-t"
                        style={{ height: count ? `${(count / maxDay) * 100}%` : 0 }}
                        title={`${count} sessions on day ${day}`}
                      />
                    </div>
                    <span className="text-coslash-muted text-[10px] tabular-nums">{day}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
