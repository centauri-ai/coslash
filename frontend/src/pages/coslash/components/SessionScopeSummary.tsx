import { formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';
import {
  getTotalTokens,
  getVendor,
  sessionReadiness,
  sessionsForAggregates,
  type Session,
} from '@/pages/coslash/lib/session';

function countBy(sessions: Session[], label: (session: Session) => string): string {
  const counts = new Map<string, number>();
  for (const session of sessions) {
    const key = label(session);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts.entries()].map(([name, count]) => `${name} ${count}`).join(' · ');
}

function usageSummary(sessions: Session[]): string {
  const aggregateSessions = sessionsForAggregates(sessions);
  const tokens = aggregateSessions.map((session) => getTotalTokens(session.tokens));
  const costs = aggregateSessions.map((session) => session.cost);
  const knownTokens = tokens.filter((value): value is number => value != null);
  const knownCosts = costs.filter((value): value is number => value != null);
  const hasUnavailable = knownTokens.length !== sessions.length || knownCosts.length !== sessions.length;
  const tokenTotal = knownTokens.reduce((sum, value) => sum + value, 0);
  const costTotal = knownCosts.reduce((sum, value) => sum + value, 0);
  const usage = knownTokens.length > 0 ? `${formatTokens(tokenTotal)} tokens` : 'Tokens unavailable';
  const cost = knownCosts.length > 0 ? formatEstimatedCost(costTotal) : 'Cost unavailable';
  return `${usage} · ${cost}${hasUnavailable ? ' + unavailable' : ''}`;
}

function SummaryCell({
  label,
  children,
  className = '',
}: {
  label: string;
  children: string;
  className?: string;
}) {
  return (
    <div className={`bg-card min-w-0 px-3 py-2.5 ${className}`}>
      <span className="text-muted-foreground block text-[0.625rem] font-semibold tracking-wider uppercase">
        {label}
      </span>
      <strong className="block truncate pt-1 text-xs font-semibold" title={children}>
        {children}
      </strong>
    </div>
  );
}

export function SessionScopeSummary({ sessions }: { sessions: Session[] }) {
  const readiness = { resume: 0, review: 0, fresh: 0, unavailable: 0 };
  for (const session of sessions) readiness[sessionReadiness(session).key] += 1;

  return (
    <div className="bg-border grid grid-cols-2 gap-px overflow-hidden rounded-lg border md:grid-cols-4 xl:grid-cols-[1.1fr_1.2fr_1fr_1fr_1.35fr]">
      <SummaryCell
        label="Current view"
        className="col-span-2 xl:col-span-1"
      >{`${sessions.length} ${sessions.length === 1 ? 'session' : 'sessions'}`}</SummaryCell>
      <SummaryCell label="Usage estimate">{usageSummary(sessions)}</SummaryCell>
      <SummaryCell label="Vendors">
        {countBy(sessions, (session) => getVendor(session.agent).label)}
      </SummaryCell>
      <SummaryCell label="Machines">{countBy(sessions, (session) => session.sourceLabel)}</SummaryCell>
      <SummaryCell label="Resume readiness" className="col-span-2 md:col-span-1">
        {`${readiness.resume} resume · ${readiness.review} review · ${readiness.fresh} fresh · ${readiness.unavailable} unavailable`}
      </SummaryCell>
    </div>
  );
}
