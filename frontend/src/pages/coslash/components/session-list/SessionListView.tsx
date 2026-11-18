import { useRef, useState } from 'react';
import {
  ArrowDown,
  ArrowUp,
  ChevronRight,
  Ellipsis,
  GitCompareArrows,
  LoaderCircle,
  ScanSearch,
} from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import { ReviewDialog } from '@/pages/coslash/components/ReviewDialog';
import { statusDot, styles, tokenTotal } from '@/pages/coslash/components/session-list/styles';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { SyncChip } from '@/pages/coslash/features/sync/SyncChip';
import type { Group } from '@/pages/coslash/lib/detected-groups';
import { handoffLabel, type DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import { formatEstimatedCost, formatTimeAgo, formatTokens } from '@/pages/coslash/lib/format';
import {
  availableReviewers,
  reviewActionVisible,
  reviewerOptionsForOrigin,
  type ReviewerOption,
  type ReviewIndex,
} from '@/pages/coslash/lib/review';
import {
  getSessionCardSummary,
  getVendor,
  isLocalSession,
  sessionCost,
  sessionDisplayTitle,
  sessionKey,
  sessionReadiness,
  sessionTotalTokens,
  STATUSES,
  sumKnown,
  type Session,
  type StatusKey,
} from '@/pages/coslash/lib/session';
import type { SessionSort } from '@/pages/coslash/lib/session-view-preferences';

type SessionReviewProps = {
  index: ReviewIndex<Session>;
  reviewerOptions: readonly ReviewerOption[];
  remoteReviewerOptions: readonly ReviewerOption[];
  remoteUnavailableReason?: string;
  canRetryRemoteReviewers: boolean;
  onRetryRemoteReviewers: () => void;
  onStarted: () => void;
  onSelectRelated: (session: Session) => void;
};

const STATUS_HINT: Record<StatusKey, string> = {
  busy: 'Working right now',
  waiting: 'Blocked on your input',
  idle: 'Open, but quiet',
  inactive: 'Not running',
  unknown: 'Live status unavailable',
};

function SortHeader({
  label,
  sortKey,
  sort,
  className,
  onSort,
}: {
  label: string;
  sortKey: SessionSort['key'];
  sort: SessionSort;
  className?: string;
  onSort: (key: SessionSort['key']) => void;
}) {
  return (
    <th className={cn(styles.head, className)}>
      <button type="button" className={styles.headButton} onClick={() => onSort(sortKey)}>
        {label} {sort.key === sortKey && (sort.dir === 'asc' ? <ArrowUp /> : <ArrowDown />)}
      </button>
    </th>
  );
}

function SessionRow({
  session,
  group,
  status,
  selected,
  compact,
  onSelect,
  review,
  handoff,
}: {
  session: Session;
  group: Group;
  status: StatusKey;
  selected: boolean;
  compact: boolean;
  onSelect: () => void;
  review: SessionReviewProps;
  handoff?: DirectedHandoff;
}) {
  const [reviewOpen, setReviewOpen] = useState(false);
  const actionButtonRef = useRef<HTMLButtonElement>(null);
  const openingReviewRef = useRef(false);
  const readiness = sessionReadiness(session);
  const vendor = getVendor(session.agent);
  const key = sessionKey(session);
  const reviewLink = review.index.links.get(key);
  const showReviewAction = reviewActionVisible(session, review.index.reviewSessions.has(key));
  const reviewActive = session.reviewPending || review.index.activeOrigins.has(key);
  const reviewerOptions = reviewerOptionsForOrigin(
    session,
    review.reviewerOptions,
    review.remoteReviewerOptions,
  );
  const unavailableReason = isLocalSession(session) ? undefined : review.remoteUnavailableReason;
  const retryReviewers = !isLocalSession(session) && review.canRetryRemoteReviewers;
  const reviewDisabled =
    reviewActive ||
    unavailableReason != null ||
    availableReviewers(reviewerOptions, session.agent).length === 0;
  const cell = cn(styles.cell, {
    'py-[5px]': compact,
    'border-coslash-line-soft border-b': !compact,
  });
  const hideWhenCompact = { hidden: compact };
  return (
    <tr
      className={cn(
        'group hover:[&>td]:bg-coslash-soft cursor-pointer [&>td]:transition-colors',
        selected &&
          '[&>td]:bg-coslash-tint hover:[&>td]:bg-coslash-tint [&>td:first-child]:shadow-[inset_3px_0_0_var(--coslash-accent)]',
      )}
      onClick={onSelect}
    >
      <td className={cn(cell, 'w-auto')}>
        <div className="flex min-w-0 items-center gap-2">
          <button
            type="button"
            className="text-ui group-hover:text-coslash-accent min-w-0 flex-1 cursor-pointer truncate px-1.5 py-px text-left leading-[1.35] font-semibold transition-colors"
          >
            {sessionDisplayTitle(session)}
          </button>
          {isLocalSession(session) && session.hubSyncChip && (
            <SyncChip state={session.hubSyncChip} failureCode={session.hubSyncFailureCode} />
          )}
        </div>
        <span
          className={cn(
            'text-coslash-muted mt-0.5 block truncate px-1.5 text-[11.5px] leading-[1.4]',
            hideWhenCompact,
          )}
        >
          {getSessionCardSummary(session)}
        </span>
        {handoff && (
          <span className="text-meta text-info-fg block truncate px-1.5">{handoffLabel(handoff)}</span>
        )}
      </td>
      <td className={cn(cell, 'max-compact:hidden w-[204px]')}>
        <div className="text-coslash-muted flex min-w-0 items-baseline gap-1.5">
          <span className="text-cell max-w-full truncate px-1.5 py-[1.5px]">{group.label}</span>
          {session.branch && (
            <code className="min-w-0 truncate font-mono text-[11.5px] text-[inherit]">
              · {session.branch}
            </code>
          )}
        </div>
        <div className={cn('mt-1 flex min-w-0 items-center gap-1', hideWhenCompact)}>
          <span
            className={cn(
              'text-meta max-w-full truncate rounded-full px-[7px] py-0.5 leading-[1.55] font-[550]',
              vendor.bg,
              vendor.fg,
            )}
          >
            {vendor.label}
          </span>
          {!isLocalSession(session) && (
            <span className="bg-coslash-soft text-meta text-coslash-muted max-w-full truncate rounded-full px-[7px] py-0.5 leading-[1.55] font-medium">
              {session.sourceLabel}
            </span>
          )}
        </div>
      </td>
      <td className={cn(cell, 'max-sidebar:w-auto max-compact:hidden w-[168px]')}>
        <span
          className={cn('text-cell flex items-center gap-[7px] whitespace-nowrap', STATUSES[status].fg)}
          title={STATUS_HINT[status]}
        >
          <i className={cn('size-[7px] rounded-full', statusDot(status))} />
          {STATUSES[status].label}
        </span>
        <span
          className={cn(
            'text-meta text-coslash-muted mt-0.5 block truncate',
            { 'text-danger-fg font-[550]': readiness.key === 'fresh' },
            { 'text-success-fg font-[550]': readiness.cacheWarm && readiness.key !== 'fresh' },
            hideWhenCompact,
          )}
        >
          {readiness.detail}
        </span>
      </td>
      <td
        className={cn(cell, 'text-coslash-muted max-compact:hidden w-[92px] whitespace-nowrap tabular-nums')}
      >
        {formatTimeAgo(session.mtime)}
      </td>
      <td className={cn(cell, 'max-cost:hidden w-[94px] whitespace-nowrap tabular-nums')}>
        <div>
          <UnpricedModelWarning unpriced={session.unpricedModels}>
            {formatEstimatedCost(sessionCost(session))}
          </UnpricedModelWarning>
        </div>
        <span className={cn('text-meta text-coslash-muted mt-0.5 block', hideWhenCompact)}>
          {formatTokens(sessionTotalTokens(session))} tokens
        </span>
      </td>
      <td
        className={cn(
          cell,
          'coslash-action-column max-compact:hidden w-12 px-2 text-right whitespace-nowrap',
        )}
        onClick={(event) => event.stopPropagation()}
      >
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              ref={actionButtonRef}
              type="button"
              className="text-coslash-muted hover:bg-coslash-soft hover:text-coslash-ink grid size-7 cursor-pointer place-items-center rounded-lg [&>svg]:size-4"
              aria-label="Session actions"
              title="Session actions"
            >
              <Ellipsis aria-hidden="true" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            className="coslash-shell bg-coslash-surface text-coslash-ink border-coslash-line min-w-40 border"
            onCloseAutoFocus={(event) => {
              if (!openingReviewRef.current) return;
              openingReviewRef.current = false;
              event.preventDefault();
            }}
          >
            {reviewLink != null && (
              <DropdownMenuItem
                className="text-meta cursor-pointer"
                onSelect={() => review.onSelectRelated(reviewLink.target)}
              >
                <GitCompareArrows />
                {reviewLink.kind === 'review' ? 'Open origin' : 'Open review'}
              </DropdownMenuItem>
            )}
            {showReviewAction && (
              <DropdownMenuItem
                className="text-meta cursor-pointer"
                disabled={reviewActive || (reviewDisabled && !retryReviewers)}
                title={unavailableReason}
                onSelect={() => {
                  if (retryReviewers) {
                    review.onRetryRemoteReviewers();
                    return;
                  }
                  openingReviewRef.current = true;
                  setReviewOpen(true);
                }}
              >
                {reviewActive ? <LoaderCircle className="animate-spin" /> : <ScanSearch />}
                <span className="flex flex-col items-start">
                  <span>
                    {reviewActive
                      ? 'Review running'
                      : retryReviewers
                        ? 'Retry reviewer check'
                        : session.reviewError
                          ? 'Retry review'
                          : 'Send for review'}
                  </span>
                  {unavailableReason && (
                    <span className="max-w-64 text-left text-xs whitespace-normal opacity-75">
                      {unavailableReason}
                    </span>
                  )}
                </span>
              </DropdownMenuItem>
            )}
            <DropdownMenuItem className="text-meta cursor-pointer" onSelect={onSelect}>
              <ChevronRight />
              {status === 'busy' ? 'Watch' : readiness.label}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        {showReviewAction && (
          <ReviewDialog
            origin={session}
            reviewerOptions={reviewerOptions}
            unavailableReason={unavailableReason}
            active={reviewActive}
            reviewError={session.reviewError}
            open={reviewOpen}
            onOpenChange={setReviewOpen}
            showTrigger={false}
            returnFocusRef={actionButtonRef}
            onStarted={review.onStarted}
          />
        )}
      </td>
    </tr>
  );
}

export function SessionListView({
  sections,
  groups,
  selectedSessionKey,
  compact,
  sort,
  openSections,
  sectionLimits,
  onSort,
  onToggleSection,
  onShowMore,
  onSelectSession,
  review,
  handoffs,
}: {
  sections: { status: StatusKey; rows: Session[] }[];
  groups: Map<string, Group>;
  selectedSessionKey: string | null;
  compact: boolean;
  sort: SessionSort;
  openSections: Record<StatusKey, boolean>;
  sectionLimits: Record<string, number>;
  onSort: (key: SessionSort['key']) => void;
  onToggleSection: (status: StatusKey) => void;
  onShowMore: (status: StatusKey, limit: number) => void;
  onSelectSession: (session: Session) => void;
  review: SessionReviewProps;
  handoffs: ReadonlyMap<string, DirectedHandoff>;
}) {
  return (
    <table className="w-full table-fixed border-separate border-spacing-0">
      <thead>
        <tr>
          <SortHeader label="Session" sortKey="title" sort={sort} onSort={onSort} className="w-[300px]" />
          <th className={cn(styles.head, 'max-compact:hidden w-[204px] px-2.5 py-[9px]')}>Where</th>
          <th className={cn(styles.head, 'max-sidebar:w-auto max-compact:hidden w-[168px] px-2.5 py-[9px]')}>
            Status / context
          </th>
          <SortHeader
            label="Updated"
            sortKey="recent"
            sort={sort}
            onSort={onSort}
            className="max-compact:hidden w-[92px] [&_button]:justify-start"
          />
          <SortHeader
            label="≈ Cost"
            sortKey="cost"
            sort={sort}
            onSort={onSort}
            className="max-cost:hidden w-[94px] [&_button]:justify-start"
          />
          <th
            className={cn(
              styles.head,
              'coslash-action-column max-compact:hidden w-12 px-2 py-[9px] text-right',
            )}
          >
            <span className="sr-only">Actions</span>
          </th>
        </tr>
      </thead>
      {sections.map(({ status, rows }) => {
        const open = openSections[status];
        const limit = sectionLimits[status] ?? (compact ? 12 : 5);
        const shown = open ? rows.slice(0, limit) : [];
        const aggregate = rows;
        return (
          <tbody key={status}>
            <tr>
              <th colSpan={6} className="border-coslash-line-soft bg-coslash-soft border-y text-left">
                <button
                  type="button"
                  className="hover:bg-coslash-line-soft flex min-h-8 w-full cursor-pointer items-center gap-2 px-2.5 py-[7px] text-xs font-[650] transition-colors"
                  aria-expanded={open}
                  onClick={() => onToggleSection(status)}
                >
                  <ChevronRight className={cn('size-4 transition-transform', { 'rotate-90': open })} />
                  {STATUSES[status].label}{' '}
                  <span className="text-coslash-muted font-medium">{rows.length}</span>
                  <span className="text-coslash-muted ml-auto text-[11.5px] font-medium">
                    {formatTokens(tokenTotal(aggregate))} tokens ·{' '}
                    {formatEstimatedCost(sumKnown(aggregate.map(sessionCost)))}
                  </span>
                </button>
              </th>
            </tr>
            {shown.map((session) => {
              const key = sessionKey(session);
              const group = groups.get(key)!;
              return (
                <SessionRow
                  key={key}
                  session={session}
                  group={group}
                  status={status}
                  selected={key === selectedSessionKey}
                  compact={compact}
                  onSelect={() => onSelectSession(session)}
                  review={review}
                  handoff={handoffs.get(key)}
                />
              );
            })}
            {open && rows.length > shown.length && (
              <tr>
                <td colSpan={6} className="p-0">
                  <button
                    type="button"
                    className="text-coslash-accent hover:bg-coslash-soft w-full cursor-pointer p-[9px] text-xs font-semibold"
                    onClick={() => onShowMore(status, limit)}
                  >
                    Show {Math.min(50, rows.length - shown.length)} more · {rows.length - shown.length}{' '}
                    remaining
                  </button>
                </td>
              </tr>
            )}
          </tbody>
        );
      })}
    </table>
  );
}
