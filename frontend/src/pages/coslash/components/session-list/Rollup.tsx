import { InlineSpinner } from '@/pages/coslash/components/session-list/InlineSpinner';
import { tokenTotal } from '@/pages/coslash/components/session-list/styles';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';
import { sessionCost, sumKnown, type Session } from '@/pages/coslash/lib/session';

export function Rollup({ sessions, isLoading }: { sessions: Session[]; isLoading: boolean }) {
  const unpriced = sessions.filter(
    (session) => sessionCost(session) == null || session.unpricedModels.length > 0,
  );
  return (
    <div className="text-cell max-narrow:w-full flex min-w-0 flex-nowrap items-center gap-2 overflow-hidden whitespace-nowrap">
      {isLoading && <span className="sr-only">Refreshing sessions</span>}
      <strong className="flex items-center gap-1 font-[650]">
        {isLoading ? <InlineSpinner /> : sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}
      </strong>
      <span className="text-coslash-muted">in this scope</span>
      <span className="text-coslash-muted">·</span>
      <span className="text-coslash-muted flex items-center gap-1">
        {isLoading ? <InlineSpinner /> : formatTokens(tokenTotal(sessions))} tokens
      </span>
      <span className="text-coslash-muted">·</span>
      {isLoading ? (
        <InlineSpinner />
      ) : (
        <UnpricedModelWarning unpriced={unpriced.flatMap((session) => session.unpricedModels)}>
          {formatEstimatedCost(sumKnown(sessions.map(sessionCost)))}
        </UnpricedModelWarning>
      )}
      {!isLoading && unpriced.length > 0 && (
        <span className="text-coslash-muted max-actions:hidden">· {unpriced.length} not priced</span>
      )}
      <span className="text-meta text-coslash-muted max-actions:hidden">at list API prices</span>
    </div>
  );
}
