import { Fragment, useState } from 'react';
import { ChevronRight, GitCompareArrows } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { ReviewDialog } from '@/pages/coslash/components/ReviewDialog';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTimeAgo, formatTokens } from '@/pages/coslash/lib/format';
import { type ReviewerOption, type ReviewIndex } from '@/pages/coslash/lib/review';
import {
  getSessionCardSummary,
  getTotalTokens,
  getVendor,
  isLocalSession,
  sessionKey,
  sessionReadiness,
  sessionsForAggregates,
  sumKnown,
  type Session,
  type SessionReadiness,
} from '@/pages/coslash/lib/session';
import {
  groupSessions,
  type BoardGroup,
  type BoardGroupBy,
  type BoardRowGroupBy,
} from '@/pages/coslash/lib/session-grouping';

type SessionReviewProps = {
  index: ReviewIndex<Session>;
  reviewerOptions: readonly ReviewerOption[];
  onStarted: () => void;
  onSelectRelated: (session: Session) => void;
};

const READINESS_TONE: Record<SessionReadiness['key'], { dot: string; label: string }> = {
  resume: { dot: 'bg-coslash-green-dot', label: 'text-coslash-green-ink' },
  review: { dot: 'bg-coslash-amber-dot', label: 'text-coslash-amber-ink' },
  fresh: { dot: 'bg-coslash-clay-dot', label: 'text-coslash-clay' },
  unavailable: { dot: 'border border-coslash-neutral-dot', label: 'text-coslash-muted' },
};

// Only dimensions with a liveness meaning of their own get a dot in the column header.
const COLUMN_DOT: Record<string, string> = {
  busy: 'bg-coslash-green-dot',
  waiting: 'bg-coslash-amber-dot',
  idle: 'bg-coslash-neutral-dot',
  inactive: 'bg-coslash-neutral-dot',
  unknown: 'border border-coslash-neutral-dot',
  resume: 'bg-coslash-green-dot',
  review: 'bg-coslash-amber-dot',
  fresh: 'bg-coslash-clay-dot',
  unavailable: 'border border-coslash-neutral-dot',
};

const pillClass =
  'text-meta max-w-full shrink-0 truncate rounded-full px-[7px] py-0.5 leading-[1.55] font-[550]';

function GroupTotals({ sessions }: { sessions: Session[] }) {
  const aggregate = sessionsForAggregates(sessions);
  const tokens = sumKnown(aggregate.map((session) => getTotalTokens(session.tokens)));
  const cost = sumKnown(aggregate.map((session) => session.cost));

  return (
    <span className="text-meta text-coslash-muted tabular-nums">
      {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'} · {formatTokens(tokens)} tok ·{' '}
      <UnpricedModelWarning unpriced={aggregate.flatMap((session) => session.unpricedModels)}>
        {formatEstimatedCost(cost)}
      </UnpricedModelWarning>
    </span>
  );
}

function ColumnHeader({ group }: { group: BoardGroup }) {
  const dot = COLUMN_DOT[group.key];
  return (
    <div className="border-coslash-line bg-coslash-surface sticky top-0 z-10 flex items-center justify-between gap-2 border-b border-l px-3 py-2">
      <span className="flex min-w-0 items-center gap-[7px] text-[12px] font-[650]">
        {dot && <i className={cn('size-[7px] shrink-0 rounded-full', dot)} />}
        <span className="truncate" title={group.title}>
          {group.label}
        </span>
      </span>
      <span className="text-meta text-coslash-muted shrink-0 tabular-nums">{group.sessions.length}</span>
    </div>
  );
}

function RowGroupHeader({
  group,
  open,
  onToggle,
}: {
  group: BoardGroup;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <div className="border-coslash-line bg-coslash-surface sticky top-[34px] z-9 col-span-full border-b">
      <button
        type="button"
        className="flex w-full cursor-pointer items-center py-[7px] text-left"
        aria-expanded={open}
        onClick={onToggle}
      >
        <span className="sticky left-0 flex items-center gap-2 px-3">
          <ChevronRight className={cn('size-4 shrink-0 transition-transform', { 'rotate-90': open })} />
          <span className="font-mono text-[12px] font-[650]" title={group.title}>
            {group.label}
          </span>
          <GroupTotals sessions={group.sessions} />
        </span>
      </button>
    </div>
  );
}

function CardActions({ session, review }: { session: Session; review: SessionReviewProps }) {
  const key = sessionKey(session);
  const reviewLink = review.index.links.get(key);
  const showReviewAction =
    isLocalSession(session) && session.cwd.trim() !== '' && !review.index.reviewSessions.has(key);
  if (reviewLink == null && !showReviewAction) return null;
  return (
    <div className="flex flex-wrap items-center gap-1 pt-2" onClick={(event) => event.stopPropagation()}>
      {reviewLink != null && (
        <Button
          size="xs"
          variant="ghost"
          title={
            reviewLink.kind === 'review' ? 'Review session — open origin' : 'Has review — open latest review'
          }
          onClick={() => review.onSelectRelated(reviewLink.target)}
        >
          <GitCompareArrows />
          {reviewLink.kind === 'review' ? 'Open origin' : 'Open review'}
        </Button>
      )}
      {showReviewAction && (
        <ReviewDialog
          origin={session}
          reviewerOptions={review.reviewerOptions}
          active={session.reviewPending || review.index.activeOrigins.has(key)}
          reviewError={session.reviewError}
          onStarted={review.onStarted}
        />
      )}
    </div>
  );
}

function BoardCard({
  session,
  selected,
  onSelect,
  review,
}: {
  session: Session;
  selected: boolean;
  onSelect: () => void;
  review: SessionReviewProps;
}) {
  const readiness = sessionReadiness(session);
  const tone = READINESS_TONE[readiness.key];
  const vendor = getVendor(session.agent);
  return (
    <div
      className={cn(
        'border-coslash-line bg-coslash-surface hover:border-coslash-tint-line cursor-pointer rounded-lg border p-3',
        {
          'border-coslash-tint-line shadow-[inset_3px_0_0_var(--coslash-accent)]': selected,
          'opacity-75': session.displayStale,
        },
      )}
      onClick={onSelect}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-1">
          <span className={cn(pillClass, vendor.bg, vendor.fg)}>{vendor.label}</span>
          {!isLocalSession(session) && (
            <span className={cn(pillClass, 'bg-coslash-soft text-coslash-muted font-medium')}>
              {session.sourceLabel}
            </span>
          )}
        </div>
        <span className="text-meta shrink-0 font-[650] whitespace-nowrap tabular-nums">
          <UnpricedModelWarning unpriced={session.unpricedModels}>
            {formatEstimatedCost(session.cost)}
          </UnpricedModelWarning>
        </span>
      </div>
      <button
        type="button"
        className="hover:text-coslash-accent line-clamp-2 w-full pt-2 text-left text-[13px] leading-[1.35] font-semibold break-words"
        onClick={onSelect}
      >
        {session.name ?? session.firstPrompt ?? 'Untitled session'}
      </button>
      <p className="text-coslash-muted line-clamp-2 pt-1 text-[11.5px] leading-[1.45]">
        {getSessionCardSummary(session)}
      </p>
      <div className="text-meta text-coslash-muted flex items-center justify-between gap-2 pt-2">
        <span className="min-w-0 truncate font-mono">
          {session.branch ?? 'No branch'}
          {session.subagents.length > 0 && ` · ${session.subagents.length} subagents`}
        </span>
        <span className="shrink-0 tabular-nums">{formatTimeAgo(session.mtime)}</span>
      </div>
      <div className="border-coslash-line-soft text-meta mt-2 flex items-center justify-between gap-2 border-t pt-2">
        <span className={cn('flex shrink-0 items-center gap-1.5 font-[650]', tone.label)}>
          <i className={cn('size-[6px] rounded-full', tone.dot)} />
          {readiness.label}
        </span>
        <span className="text-coslash-muted min-w-0 truncate text-right">{readiness.detail}</span>
      </div>
      <CardActions session={session} review={review} />
    </div>
  );
}

function BoardCell({
  sessions,
  column,
  selectedSessionKey,
  onSelectSession,
  review,
}: {
  sessions: Session[];
  column: number;
  selectedSessionKey: string | null;
  onSelectSession: (session: Session) => void;
  review: SessionReviewProps;
}) {
  return (
    <div
      className="border-coslash-line bg-coslash-soft flex flex-col gap-2 border-l p-2"
      style={{ gridColumn: column }}
    >
      {sessions.map((session) => (
        <BoardCard
          key={sessionKey(session)}
          session={session}
          selected={selectedSessionKey === sessionKey(session)}
          onSelect={() => onSelectSession(session)}
          review={review}
        />
      ))}
    </div>
  );
}

export function SessionBoard({
  sessions,
  columnGroupBy,
  rowGroupBy,
  selectedSessionKey = null,
  onSelectSession,
  review,
}: {
  sessions: Session[];
  columnGroupBy: BoardGroupBy;
  rowGroupBy: BoardRowGroupBy;
  selectedSessionKey?: string | null;
  onSelectSession: (session: Session) => void;
  review: SessionReviewProps;
}) {
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const columns = groupSessions(sessions, columnGroupBy);
  const showRowHeaders = rowGroupBy !== 'none';
  const rows: BoardGroup[] = showRowHeaders
    ? groupSessions(sessions, rowGroupBy)
    : [{ key: 'all', label: 'All sessions', sessions }];
  const columnPositions = new Map(columns.map((column, index) => [column.key, index + 1]));

  const toggleRow = (key: string) =>
    setCollapsed((current) => {
      const next = new Set(current);
      if (!next.delete(key)) next.add(key);
      return next;
    });

  return (
    <div
      className="bg-coslash-surface grid"
      style={{ gridTemplateColumns: `repeat(${columns.length}, minmax(240px, 1fr))` }}
    >
      {columns.map((column) => (
        <ColumnHeader key={column.key} group={column} />
      ))}
      {rows.map((row) => {
        const collapseKey = `${rowGroupBy}:${row.key}`;
        const open = !collapsed.has(collapseKey);
        return (
          <Fragment key={row.key}>
            {showRowHeaders && (
              <RowGroupHeader group={row} open={open} onToggle={() => toggleRow(collapseKey)} />
            )}
            {open && (
              <div className="border-coslash-line bg-coslash-soft col-span-full grid grid-cols-subgrid border-b">
                {groupSessions(row.sessions, columnGroupBy).map((cell) => (
                  <BoardCell
                    key={cell.key}
                    sessions={cell.sessions}
                    column={columnPositions.get(cell.key)!}
                    selectedSessionKey={selectedSessionKey}
                    onSelectSession={onSelectSession}
                    review={review}
                  />
                ))}
              </div>
            )}
          </Fragment>
        );
      })}
    </div>
  );
}
