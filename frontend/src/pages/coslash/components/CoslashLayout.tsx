import { lazy, Suspense, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ChevronRight,
  Folder,
  FolderGit2,
  GitCompareArrows,
  LoaderCircle,
  Monitor,
  Moon,
  RefreshCw,
  Rows3,
  Rows4,
  Search,
  Server,
  Settings,
  Sun,
  X,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { Theme } from '@/lib/theme';
import { cn } from '@/lib/utils';
import { LoadingSpinner } from '@/pages/coslash/components/LoadingSpinner';
import { ReviewDialog } from '@/pages/coslash/components/ReviewDialog';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTimeAgo } from '@/pages/coslash/lib/format';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { buildReviewIndex, type ReviewerOption, type ReviewIndex } from '@/pages/coslash/lib/review';
import {
  boardStatusKey,
  getSessionCardSummary,
  getSessionVendors,
  getTotalTokens,
  getVendor,
  isLocalSession,
  LOCAL_SOURCE_ID,
  sessionKey,
  sessionReadiness,
  sessionsForAggregates,
  sumKnown,
  type Session,
} from '@/pages/coslash/lib/session';
import { ALL_REPOSITORIES, filterSessionLibrary } from '@/pages/coslash/lib/session-library';
import {
  loadSessionViewPreferences,
  saveSessionViewPreferences,
  type SessionRange,
  type SessionSort,
  type SessionStatusGroup,
  type SessionViewPreferences,
} from '@/pages/coslash/lib/session-view-preferences';
import { DAY } from '@/pages/coslash/lib/time';
import './coslash-layout.css';

const SessionBoard = lazy(() =>
  import('@/pages/coslash/components/SessionBoard').then((module) => ({ default: module.SessionBoard })),
);

type Group = {
  id: string;
  label: string;
  kind: 'Repository' | 'Folder' | 'No location';
  basis: string;
};
type FacetKey = 'status' | 'machine' | 'agent' | 'group';
type FacetOption = {
  id: string;
  label: string;
  count: number;
  selected: boolean;
  onClick: () => void;
  status?: SessionStatusGroup;
  icon?: ReactNode;
  indicator?: ReactNode;
};
type FacetSection = { id: FacetKey; label: string; options: FacetOption[] };
type SessionReviewProps = {
  index: ReviewIndex<Session>;
  reviewerOptions: readonly ReviewerOption[];
  onStarted: () => void;
  onSelectRelated: (session: Session) => void;
};

const STATUS_META: Record<SessionStatusGroup, { label: string; hint: string }> = {
  needs: { label: 'Needs you', hint: 'Blocked on your input' },
  running: { label: 'Running', hint: 'Working right now' },
  idle: { label: 'Idle', hint: 'Stopped — resume whenever you like' },
};
const STATUS_ORDER: SessionStatusGroup[] = ['needs', 'running', 'idle'];
const RANGE_OPTIONS: { value: SessionRange; label: string }[] = [
  { value: 'today', label: 'Today' },
  { value: 'this-week', label: 'This week' },
  { value: 'week', label: '7 days' },
  { value: 'month', label: '30 days' },
  { value: 'all', label: 'All time' },
];
const GROUP_KINDS: Group['kind'][] = ['Repository', 'Folder', 'No location'];
const GROUP_LABELS: Record<Group['kind'], string> = {
  'Repository': 'Repositories',
  'Folder': 'Folders',
  'No location': 'No location',
};

const styles = {
  shell:
    'coslash-shell min-h-svh bg-[var(--coslash-bg)] text-[13px] leading-[1.45] text-[var(--coslash-ink)] antialiased',
  header:
    'flex h-[60px] items-center justify-between gap-3.5 border-b border-[var(--coslash-line)] bg-[var(--coslash-surface)] px-5 max-[760px]:px-3',
  banner:
    'flex items-center gap-2.5 border-b border-[var(--coslash-clay)] bg-[var(--coslash-clay-bg)] px-5 py-2.5 text-xs text-[var(--coslash-clay)] [&>svg]:size-4',
  sidebar:
    'coslash-sidebar sticky top-0 max-h-[calc(100svh-60px)] min-h-[calc(100svh-60px)] w-[214px] shrink-0 overflow-y-auto border-r border-[var(--coslash-line)] bg-[var(--coslash-surface)] px-3 py-5 max-[1000px]:hidden',
  sideHeading:
    'flex min-h-[30px] w-full cursor-pointer items-center gap-[7px] rounded-[7px] px-2.5 py-1.5 text-left text-[11px] font-[650] tracking-[.09em] text-[var(--coslash-muted)] uppercase hover:bg-[var(--coslash-soft)] hover:text-[var(--coslash-ink)]',
  facet:
    'flex min-h-8 w-full cursor-pointer items-center gap-[9px] rounded-[7px] px-2.5 py-[7px] text-left text-xs leading-[1.45] text-[var(--coslash-muted)] hover:bg-[var(--coslash-soft)] [&>svg]:size-3.5',
  main: 'coslash-main min-w-0 flex-1 px-6 pt-5 pb-[60px] max-[1000px]:w-full max-[1000px]:p-4',
  search:
    'flex h-12 w-full min-w-0 items-center gap-1.5 rounded-[9px] border border-[var(--coslash-line)] bg-[var(--coslash-surface)] px-3.5 focus-within:border-[var(--coslash-accent)] focus-within:shadow-[0_0_0_3px_var(--coslash-tint)]',
  chip: 'inline-flex h-6 shrink-0 items-center gap-1 rounded-md border border-[var(--coslash-tint-line)] bg-[var(--coslash-tint)] pr-1 pl-2 text-[11px] font-[550] whitespace-nowrap text-[var(--coslash-accent-ink)]',
  segmented: 'inline-flex rounded-[10px] bg-[var(--coslash-soft)] p-0.5',
  tableWrap:
    'min-h-[180px] overflow-x-auto rounded-[10px] border border-[var(--coslash-line)] bg-[var(--coslash-surface)]',
  head: 'sticky top-0 z-12 border-b border-[var(--coslash-line)] bg-[var(--coslash-surface)] text-left text-[11px] font-[650] tracking-[.07em] text-[var(--coslash-muted)] uppercase',
  headButton:
    'flex min-h-[34px] w-full cursor-pointer items-center gap-[5px] px-2.5 py-[9px] text-left font-[inherit] tracking-[inherit] uppercase hover:bg-[var(--coslash-soft)] hover:text-[var(--coslash-ink)] [&>svg]:size-3',
  cell: 'overflow-hidden px-2.5 py-2 align-middle text-[12.5px]',
  empty: 'flex min-h-60 flex-col items-center justify-center gap-2 px-6 py-11 text-center',
};

function sessionStatusGroup(session: Session): SessionStatusGroup {
  const status = boardStatusKey(session);
  if (status === 'waiting') return 'needs';
  if (status === 'busy') return 'running';
  return 'idle';
}

function statusDot(status: SessionStatusGroup): string {
  if (status === 'needs') return 'bg-[var(--coslash-amber-dot)]';
  if (status === 'running') return 'bg-[var(--coslash-green-dot)]';
  return 'bg-[var(--coslash-neutral-dot)]';
}

function rangeStart(range: SessionRange): number | null {
  const now = new Date();
  if (range === 'all') return null;
  if (range === 'today') {
    now.setHours(0, 0, 0, 0);
    return now.getTime();
  }
  if (range === 'this-week') {
    now.setHours(0, 0, 0, 0);
    now.setDate(now.getDate() - ((now.getDay() + 6) % 7));
    return now.getTime();
  }
  return Date.now() - (range === 'week' ? 7 : 30) * DAY;
}

function detectedGroup(session: Session): Group {
  if (session.repo?.trim() && !session.repoLocalOnly) {
    const label = session.repo.split('/').filter(Boolean).at(-1) ?? session.repo;
    return {
      id: `repo:${label.toLowerCase()}`,
      label,
      kind: 'Repository',
      basis: session.repo,
    };
  }
  if (session.repo?.trim()) {
    const basis = session.cwd.trim() || session.repo;
    return {
      id: `folder:${session.sourceId}:${session.repo.toLowerCase()}`,
      label: session.repo,
      kind: 'Folder',
      basis,
    };
  }
  const cwd = session.cwd.trim();
  if (cwd) {
    const parts = cwd.split('/').filter(Boolean);
    // A root-level cwd has no parent, so it groups under itself rather than an empty shared id.
    const parent = parts.slice(0, -1).join('/') || parts.join('/');
    return {
      id: `folder:${session.sourceId}:${parent}`,
      label: `${session.sourceLabel} · ${parts.at(-2) ?? parts.at(-1) ?? 'Folder'}`,
      kind: 'Folder',
      basis: parent ? `/${parent}` : cwd,
    };
  }
  return {
    id: `unlocated:${session.sourceId}:${session.agent}`,
    label: `${getVendor(session.agent).label} · No location`,
    kind: 'No location',
    basis: 'No repository or folder was recorded',
  };
}

function formatTokens(value: number | null): string {
  if (value == null) return '—';
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`;
  if (value >= 1_000) return `${Math.round(value / 1_000)}k`;
  return String(value);
}

function formatTableCost(value: number | null): string {
  return formatEstimatedCost(value).replace(/^≈/, '');
}

function totalTokens(sessions: Session[]): number | null {
  const values = sessions.map((session) => getTotalTokens(session.tokens));
  return values.every((value) => value == null)
    ? null
    : values.reduce<number>((total, value) => total + (value ?? 0), 0);
}

function matchesSearch(session: Session, group: Group, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const vendor = getVendor(session.agent).label;
  return words.every((word) => {
    const prefix = word.match(/^(repo|group|machine|agent|status):(.+)$/);
    if (!prefix) {
      return (
        filterSessionLibrary([session], {
          search: word,
          repository: ALL_REPOSITORIES,
          source: 'all',
          shareState: 'all',
        }).length > 0
      );
    }
    const [, kind, value] = prefix;
    if (kind === 'repo') return (session.repo ?? '').toLowerCase().includes(value);
    if (kind === 'group') return group.label.toLowerCase().includes(value);
    if (kind === 'machine') return session.sourceLabel.toLowerCase().includes(value);
    if (kind === 'agent') return vendor.toLowerCase().includes(value);
    return STATUS_META[sessionStatusGroup(session)].label.toLowerCase().includes(value);
  });
}

function matchesFilters(
  session: Session,
  group: Group,
  preferences: SessionViewPreferences,
  skip?: FacetKey,
): boolean {
  const { statusFilters, groupFilters, machineFilters, agentFilters, query } = preferences;
  if (skip !== 'status' && statusFilters.length > 0 && !statusFilters.includes(sessionStatusGroup(session)))
    return false;
  if (skip !== 'group' && groupFilters.length > 0 && !groupFilters.includes(group.id)) return false;
  if (skip !== 'machine' && machineFilters.length > 0 && !machineFilters.includes(session.sourceId))
    return false;
  if (skip !== 'agent' && agentFilters.length > 0 && !agentFilters.includes(session.agent)) return false;
  return matchesSearch(session, group, query);
}

function sortSessions(sessions: Session[], sort: SessionSort): Session[] {
  return [...sessions].sort((a, b) => {
    if (sort.key === 'title') {
      const difference = (a.name ?? 'Untitled session').localeCompare(b.name ?? 'Untitled session');
      return sort.dir === 'asc' ? difference : -difference;
    }
    if (sort.key === 'cost') {
      if (a.cost == null || b.cost == null) return a.cost == null ? (b.cost == null ? 0 : 1) : -1;
      return sort.dir === 'asc' ? a.cost - b.cost : b.cost - a.cost;
    }
    return sort.dir === 'asc' ? a.mtime - b.mtime : b.mtime - a.mtime;
  });
}

function InlineSpinner() {
  return <LoaderCircle className="size-3.5 animate-spin text-[var(--coslash-muted)]" aria-hidden="true" />;
}

function Rollup({ sessions, isLoading }: { sessions: Session[]; isLoading: boolean }) {
  const aggregate = sessionsForAggregates(sessions);
  const unpriced = aggregate.filter((session) => session.cost == null || session.unpricedModels.length > 0);
  return (
    <div className="flex min-w-0 flex-nowrap items-center gap-2 overflow-hidden text-[12.5px] whitespace-nowrap max-[900px]:w-full">
      {isLoading && <span className="sr-only">Refreshing sessions</span>}
      <strong className="flex items-center gap-1 font-[650]">
        {isLoading ? <InlineSpinner /> : sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}
      </strong>
      <span className="text-[var(--coslash-muted)]">active in this scope</span>
      <span className="text-[var(--coslash-muted)]">·</span>
      <span className="flex items-center gap-1 text-[var(--coslash-muted)]">
        {isLoading ? <InlineSpinner /> : formatTokens(totalTokens(aggregate))} tokens
      </span>
      <span className="text-[var(--coslash-muted)]">·</span>
      {isLoading ? (
        <InlineSpinner />
      ) : (
        <UnpricedModelWarning unpriced={unpriced.flatMap((session) => session.unpricedModels)}>
          {formatEstimatedCost(sumKnown(aggregate.map((session) => session.cost)))}
        </UnpricedModelWarning>
      )}
      {!isLoading && unpriced.length > 0 && (
        <span className="text-[var(--coslash-muted)] max-[1250px]:hidden">
          · {unpriced.length} not priced
        </span>
      )}
      <span className="text-[11px] text-[var(--coslash-muted)] max-[1250px]:hidden">at list API prices</span>
    </div>
  );
}

function FacetRow({ label, count, selected, icon, indicator, status, onClick }: FacetOption) {
  return (
    <button
      type="button"
      className={cn(styles.facet, {
        'bg-[var(--coslash-tint)] font-semibold text-[var(--coslash-accent-ink)]': selected,
        'opacity-55': count === 0 && !selected,
      })}
      aria-pressed={selected}
      onClick={onClick}
    >
      {status ? <span className={cn('size-[7px] shrink-0 rounded-full', statusDot(status))} /> : icon}
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate">{label}</span>
        {indicator}
      </span>
      <span className="ml-auto text-[11px] tabular-nums">{count}</span>
    </button>
  );
}

function FacetRows({ options }: { options: FacetOption[] }) {
  return options.map((option) => <FacetRow key={option.id} {...option} />);
}

function SidebarSection({
  section,
  open,
  isLoading,
  onToggle,
}: {
  section: FacetSection;
  open: boolean;
  isLoading: boolean;
  onToggle: () => void;
}) {
  const contentId = `coslash-${section.id}`;
  return (
    <div className="pb-3.5">
      <button
        className={styles.sideHeading}
        aria-expanded={open}
        aria-controls={contentId}
        onClick={onToggle}
      >
        <ChevronRight className={cn('size-[13px] transition-transform', open && 'rotate-90')} />
        <span className="flex flex-1 items-center justify-between">
          {section.label}
          {isLoading && <InlineSpinner />}
        </span>
      </button>
      {open && (
        <div id={contentId}>
          <FacetRows options={section.options} />
        </div>
      )}
    </div>
  );
}

function CoslashHeader({
  machines,
  diagnostics,
  onSettings,
  theme,
  onThemeChange,
  themeDisabled,
  onRetry,
  retrying,
  actions,
}: {
  machines: MachineFact[];
  diagnostics: ReactNode;
  onSettings: () => void;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  themeDisabled: boolean;
  onRetry: () => void;
  retrying: boolean;
  actions?: ReactNode;
}) {
  const problems = machines.filter(
    (machine) => ['stale', 'error'].includes(machine.state) && machine.reason !== 'initial_refresh',
  );
  return (
    <>
      <div className={styles.header}>
        <div className="flex min-w-0 items-center gap-3.5">
          <span aria-label="coSlash">
            <img src="/brand/coslash-logo.svg" alt="" className="w-[104px] dark:hidden" />
            <img src="/brand/coslash-logo-reverse.svg" alt="" className="hidden w-[104px] dark:block" />
          </span>
          <span className="truncate text-[11px] text-[var(--coslash-muted)] max-[1000px]:hidden">
            Run more agents. Lose less context.
          </span>
        </div>
        <div className="flex items-center gap-2">
          {actions}
          <div className="rounded-[10px] bg-[var(--coslash-soft)] p-0.5">{diagnostics}</div>
          <div className="rounded-[10px] bg-[var(--coslash-soft)] p-0.5">
            <Button
              variant="ghost"
              size="sm"
              className="min-h-7 cursor-pointer gap-1.5 rounded-[7px] px-2.5 text-[11px] font-[550] [&>svg]:size-3.5"
              onClick={onSettings}
            >
              <Settings /> Settings
            </Button>
          </div>
          <div
            className="inline-flex items-center gap-0.5 rounded-[10px] bg-[var(--coslash-soft)] p-0.5"
            aria-label="Theme"
          >
            {(
              [
                ['light', Sun],
                ['dark', Moon],
              ] as const
            ).map(([value, Icon]) => (
              <button
                key={value}
                type="button"
                className={cn(
                  'grid size-7 cursor-pointer place-items-center rounded-[7px] text-[var(--coslash-muted)] hover:bg-[var(--coslash-surface)] [&>svg]:size-3.5',
                  theme === value && 'bg-[var(--coslash-surface)] text-[var(--coslash-ink)] shadow-sm',
                )}
                aria-label={`${value === 'light' ? 'Light' : 'Dark'} theme`}
                aria-pressed={theme === value}
                disabled={themeDisabled}
                onClick={() => onThemeChange(value)}
              >
                <Icon aria-hidden="true" />
              </button>
            ))}
          </div>
        </div>
      </div>
      {problems.length > 0 && (
        <div className={styles.banner} role="alert">
          <AlertTriangle />
          <span>
            <strong className="font-[650]">{problems.map((machine) => machine.label).join(', ')}</strong>{' '}
            unreachable — remote sessions show their last recorded context.
          </span>
          <Button
            variant="outline"
            size="sm"
            className="ml-auto border-[var(--coslash-clay)] bg-transparent text-[var(--coslash-clay)]"
            onClick={onRetry}
            disabled={retrying}
          >
            <RefreshCw className={cn(retrying && 'animate-spin')} /> Retry
          </Button>
        </div>
      )}
    </>
  );
}

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
  onToggleGroup,
  review,
}: {
  session: Session;
  group: Group;
  status: SessionStatusGroup;
  selected: boolean;
  compact: boolean;
  onSelect: () => void;
  onToggleGroup: () => void;
  review: SessionReviewProps;
}) {
  const readiness = sessionReadiness(session);
  const vendor = getVendor(session.agent);
  const key = sessionKey(session);
  const reviewLink = review.index.links.get(key);
  const showReviewAction = isLocalSession(session) && !review.index.reviewSessions.has(key);
  const cell = cn(styles.cell, compact && 'py-[5px]');
  const hideWhenCompact = compact && 'hidden';
  return (
    <tr
      className={cn(
        'coslash-row group cursor-pointer hover:[&>td]:bg-[var(--coslash-soft)]',
        !compact && 'border-b border-[var(--coslash-line-soft)]',
        selected &&
          '[&>td]:bg-[var(--coslash-tint)] hover:[&>td]:bg-[var(--coslash-tint)] [&>td:first-child]:shadow-[inset_3px_0_0_var(--coslash-accent)]',
      )}
      onClick={onSelect}
    >
      <td className={cn(cell, 'w-auto')}>
        <button
          type="button"
          className="block w-full truncate px-1.5 py-px text-left text-[13px] leading-[1.35] font-semibold hover:text-[var(--coslash-accent)]"
          onClick={onSelect}
        >
          {session.name ?? session.firstPrompt ?? 'Untitled session'}
        </button>
        <span
          className={cn(
            'mt-0.5 block truncate px-1.5 text-[11.5px] leading-[1.4] text-[var(--coslash-muted)]',
            hideWhenCompact,
          )}
        >
          {getSessionCardSummary(session)}
        </span>
      </td>
      <td className={cn(cell, 'w-[204px] max-[760px]:hidden')}>
        <div className="flex min-w-0 items-baseline gap-1.5 text-[var(--coslash-muted)]">
          <button
            type="button"
            className="max-w-full truncate px-1.5 py-[1.5px] text-left text-[12.5px] hover:text-[var(--coslash-accent)]"
            onClick={(event) => {
              event.stopPropagation();
              onToggleGroup();
            }}
          >
            {group.label}
          </button>
          {session.branch && (
            <code className="min-w-0 truncate font-mono text-[11.5px] text-[inherit]">
              · {session.branch}
            </code>
          )}
        </div>
        <div className={cn('mt-1 flex min-w-0 items-center gap-1', hideWhenCompact)}>
          <span
            className={cn(
              'max-w-full truncate rounded-full px-[7px] py-0.5 text-[11px] leading-[1.55] font-[550]',
              session.agent === 'claude'
                ? 'bg-[#f8ede6] text-[#96552f] dark:bg-[#332318] dark:text-[#e0a483]'
                : session.agent === 'codex'
                  ? 'bg-[#eceffc] text-[#4a5ab8] dark:bg-[#252d47] dark:text-[#a9b6f0]'
                  : 'bg-[var(--coslash-soft)] text-[var(--coslash-muted)]',
            )}
          >
            {vendor.label}
          </span>
          {!isLocalSession(session) && (
            <span className="max-w-full truncate rounded-full bg-[var(--coslash-soft)] px-[7px] py-0.5 text-[11px] leading-[1.55] font-medium text-[var(--coslash-muted)]">
              {session.sourceLabel}
            </span>
          )}
        </div>
      </td>
      <td className={cn(cell, 'w-[168px] max-[1000px]:w-auto max-[760px]:hidden')}>
        <span
          className={cn(
            'flex items-center gap-[7px] text-[12.5px] whitespace-nowrap text-[var(--coslash-muted)]',
            status === 'needs' && 'text-[#8a5a10] dark:text-[#e2b76d]',
            status === 'running' && 'text-[#1b6b4c] dark:text-[#82d3aa]',
          )}
          title={STATUS_META[status].hint}
        >
          <i className={cn('size-[7px] rounded-full', statusDot(status))} />
          {STATUS_META[status].label}
        </span>
        <span
          className={cn(
            'mt-0.5 block truncate text-[11px] text-[var(--coslash-muted)]',
            readiness.key === 'fresh' && 'font-[550] text-[var(--coslash-clay)]',
            hideWhenCompact,
          )}
        >
          {readiness.detail}
        </span>
      </td>
      <td
        className={cn(
          cell,
          'w-[92px] whitespace-nowrap text-[var(--coslash-muted)] tabular-nums max-[760px]:hidden',
        )}
      >
        {formatTimeAgo(session.mtime)}
      </td>
      <td className={cn(cell, 'w-[94px] whitespace-nowrap tabular-nums max-[1120px]:hidden')}>
        <div>
          <UnpricedModelWarning unpriced={session.unpricedModels}>
            {formatTableCost(session.cost)}
          </UnpricedModelWarning>
        </div>
        <span className={cn('mt-0.5 block text-[11px] text-[var(--coslash-muted)]', hideWhenCompact)}>
          {formatTokens(getTotalTokens(session.tokens))} tokens
        </span>
      </td>
      <td
        className={cn(
          cell,
          'coslash-action-column relative w-[96px] overflow-visible pl-0 text-right whitespace-nowrap max-[1250px]:w-10 max-[1250px]:px-0 max-[760px]:hidden',
        )}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="coslash-action-button absolute top-1/2 right-2.5 flex w-max -translate-y-1/2 items-center justify-end gap-[7px] max-[1250px]:hidden [&_[data-slot=button]]:h-auto [&_[data-slot=button]]:min-h-7 [&_[data-slot=button]]:gap-[7px] [&_[data-slot=button]]:rounded-lg [&_[data-slot=button]]:px-[9px] [&_[data-slot=button]]:py-[5px] [&_[data-slot=button]]:text-[11px] [&_[data-slot=button]]:leading-[1.45] [&_[data-slot=button]]:font-[550]">
          {reviewLink != null && (
            <Button
              size="xs"
              variant="ghost"
              title={
                reviewLink.kind === 'review'
                  ? 'Review session — open origin'
                  : 'Has review — open latest review'
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
          <Button
            variant="outline"
            size="sm"
            className="min-h-[28px] min-w-0 gap-[7px] rounded-lg border-[var(--coslash-line)] bg-[var(--coslash-surface)] px-[9px] py-[5px] text-[11px] font-[550]"
            onClick={onSelect}
          >
            {status === 'running' ? 'Watch' : readiness.label}
          </Button>
        </div>
      </td>
    </tr>
  );
}

function SessionListView({
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
  onToggleGroup,
  review,
}: {
  sections: { status: SessionStatusGroup; rows: Session[] }[];
  groups: Map<string, Group>;
  selectedSessionKey: string | null;
  compact: boolean;
  sort: SessionSort;
  openSections: Record<SessionStatusGroup, boolean>;
  sectionLimits: Record<string, number>;
  onSort: (key: SessionSort['key']) => void;
  onToggleSection: (status: SessionStatusGroup) => void;
  onShowMore: (status: SessionStatusGroup, limit: number) => void;
  onSelectSession: (session: Session) => void;
  onToggleGroup: (id: string) => void;
  review: SessionReviewProps;
}) {
  return (
    <table className="w-full table-fixed border-collapse max-[760px]:table-auto">
      <thead>
        <tr>
          <SortHeader label="Session" sortKey="title" sort={sort} onSort={onSort} className="w-[300px]" />
          <th className={cn(styles.head, 'w-[204px] px-2.5 py-[9px] max-[760px]:hidden')}>Where</th>
          <th className={cn(styles.head, 'w-[168px] px-2.5 py-[9px] max-[1000px]:w-auto max-[760px]:hidden')}>
            Status / context
          </th>
          <SortHeader
            label="Updated"
            sortKey="recent"
            sort={sort}
            onSort={onSort}
            className="w-[92px] max-[760px]:hidden [&_button]:justify-start"
          />
          <SortHeader
            label="≈ Cost"
            sortKey="cost"
            sort={sort}
            onSort={onSort}
            className="w-[94px] max-[1120px]:hidden [&_button]:justify-start"
          />
          <th
            className={cn(
              styles.head,
              'coslash-action-column w-[96px] px-2.5 py-[9px] text-right max-[1250px]:w-10 max-[1250px]:px-0 max-[760px]:hidden',
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
        const aggregate = sessionsForAggregates(rows);
        return (
          <tbody key={status}>
            <tr>
              <th
                colSpan={6}
                className="sticky top-[34px] z-11 border-y border-[var(--coslash-line-soft)] bg-[var(--coslash-soft)] text-left"
              >
                <button
                  type="button"
                  className="flex min-h-8 w-full cursor-pointer items-center gap-2 px-2.5 py-[7px] text-xs font-[650]"
                  aria-expanded={open}
                  onClick={() => onToggleSection(status)}
                >
                  <ChevronRight className={cn('size-4 transition-transform', open && 'rotate-90')} />
                  {STATUS_META[status].label}{' '}
                  <span className="font-medium text-[var(--coslash-muted)]">{rows.length}</span>
                  <span className="ml-auto text-[11.5px] font-medium text-[var(--coslash-muted)]">
                    {formatTokens(totalTokens(aggregate))} tokens ·{' '}
                    {formatTableCost(sumKnown(aggregate.map((session) => session.cost)))}
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
                  onToggleGroup={() => onToggleGroup(group.id)}
                  review={review}
                />
              );
            })}
            {open && rows.length > shown.length && (
              <tr>
                <td colSpan={6} className="p-0">
                  <button
                    type="button"
                    className="w-full cursor-pointer p-[9px] text-xs font-semibold text-[var(--coslash-accent)] hover:bg-[var(--coslash-soft)]"
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

export function CoslashLayout({
  sessions,
  machines,
  range,
  onRangeChange,
  selectedSessionKey,
  onSelectSession,
  diagnostics,
  onSettings,
  theme,
  onThemeChange,
  themeDisabled,
  onRetry,
  retrying,
  isLoading,
  loadError,
  emptyContent,
  banner,
  headerActions,
  inspectorOpen = false,
  reviewerOptions,
  onReviewStarted,
}: {
  sessions: Session[];
  machines: MachineFact[];
  range: SessionRange;
  onRangeChange: (range: SessionRange) => void;
  selectedSessionKey: string | null;
  onSelectSession: (session: Session) => void;
  diagnostics: ReactNode;
  onSettings: () => void;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  themeDisabled: boolean;
  onRetry: () => void;
  retrying: boolean;
  isLoading: boolean;
  loadError: string | null;
  emptyContent?: ReactNode;
  banner?: ReactNode;
  headerActions?: ReactNode;
  inspectorOpen?: boolean;
  reviewerOptions: readonly ReviewerOption[];
  onReviewStarted: () => void;
}) {
  const [preferences, setPreferences] = useState(loadSessionViewPreferences);
  const [openSections, setOpenSections] = useState<Record<SessionStatusGroup, boolean>>({
    needs: true,
    running: true,
    idle: true,
  });
  const [sectionLimits, setSectionLimits] = useState<Record<string, number>>({});
  const [groupQuery, setGroupQuery] = useState('');
  const [sideOpen, setSideOpen] = useState<Record<FacetKey, boolean>>({
    status: true,
    machine: true,
    agent: true,
    group: true,
  });
  const patchPreferences = (patch: Partial<SessionViewPreferences>) =>
    setPreferences((current) => ({ ...current, ...patch }));

  useEffect(() => saveSessionViewPreferences({ ...preferences, range }), [preferences, range]);

  const sessionGroups = useMemo(
    () => new Map(sessions.map((session) => [sessionKey(session), detectedGroup(session)])),
    [sessions],
  );
  const groups = useMemo(
    () =>
      [...new Map([...sessionGroups.values()].map((group) => [group.id, group])).values()].sort((a, b) =>
        a.label.localeCompare(b.label),
      ),
    [sessionGroups],
  );
  const agents = useMemo(() => getSessionVendors(sessions), [sessions]);
  const start = rangeStart(range);
  const sessionsInRange = useMemo(
    () => sessions.filter((session) => start == null || session.status != null || session.mtime >= start),
    [sessions, start],
  );
  const visibleSessions = useMemo(
    () =>
      sortSessions(
        sessionsInRange.filter((session) =>
          matchesFilters(session, sessionGroups.get(sessionKey(session))!, preferences),
        ),
        preferences.sort,
      ),
    [preferences, sessionGroups, sessionsInRange],
  );
  const countWith = (predicate: (session: Session) => boolean, skip: FacetKey) =>
    sessionsInRange.filter(
      (session) =>
        matchesFilters(session, sessionGroups.get(sessionKey(session))!, preferences, skip) &&
        predicate(session),
    ).length;
  const toggleStatus = (status: SessionStatusGroup) =>
    patchPreferences({
      statusFilters: preferences.statusFilters.includes(status)
        ? preferences.statusFilters.filter((value) => value !== status)
        : [...preferences.statusFilters, status],
    });
  const toggleGroup = (id: string) =>
    patchPreferences({
      groupFilters: preferences.groupFilters.includes(id)
        ? preferences.groupFilters.filter((value) => value !== id)
        : [...preferences.groupFilters, id],
    });
  const toggleMachine = (id: string) =>
    patchPreferences({
      machineFilters: preferences.machineFilters.includes(id)
        ? preferences.machineFilters.filter((value) => value !== id)
        : [...preferences.machineFilters, id],
    });
  const toggleAgent = (id: string) =>
    patchPreferences({
      agentFilters: preferences.agentFilters.includes(id)
        ? preferences.agentFilters.filter((value) => value !== id)
        : [...preferences.agentFilters, id],
    });
  const clearFacets = () =>
    patchPreferences({ statusFilters: [], groupFilters: [], machineFilters: [], agentFilters: [] });
  const setSortKey = (key: SessionSort['key']) =>
    patchPreferences({
      sort:
        preferences.sort.key === key
          ? { key, dir: preferences.sort.dir === 'asc' ? 'desc' : 'asc' }
          : { key, dir: key === 'title' ? 'asc' : 'desc' },
    });

  const facetSections: FacetSection[] = [
    {
      id: 'status',
      label: 'Status',
      options: STATUS_ORDER.map((status) => ({
        id: status,
        status,
        label: STATUS_META[status].label,
        count: countWith((session) => sessionStatusGroup(session) === status, 'status'),
        selected: preferences.statusFilters.includes(status),
        onClick: () => toggleStatus(status),
      })),
    },
    {
      id: 'machine',
      label: 'Connections',
      options: machines.map((machine) => ({
        id: machine.sourceId,
        label: machine.label,
        icon: machine.sourceId === LOCAL_SOURCE_ID ? <Monitor /> : <Server />,
        indicator:
          machine.sourceId !== LOCAL_SOURCE_ID && ['ok', 'limited'].includes(machine.state) ? (
            <span
              className="size-[7px] shrink-0 rounded-full bg-[var(--coslash-green-dot)]"
              aria-label="Connected"
              title="Connected"
            />
          ) : undefined,
        count: countWith((session) => session.sourceId === machine.sourceId, 'machine'),
        selected: preferences.machineFilters.includes(machine.sourceId),
        onClick: () => toggleMachine(machine.sourceId),
      })),
    },
    {
      id: 'agent',
      label: 'Agent',
      options: agents.map((agent) => ({
        id: agent,
        label: getVendor(agent).label,
        count: countWith((session) => session.agent === agent, 'agent'),
        selected: preferences.agentFilters.includes(agent),
        onClick: () => toggleAgent(agent),
      })),
    },
  ];
  const normalizedGroupQuery = groupQuery.trim().toLowerCase();
  const groupOptions = (kind: Group['kind']): FacetOption[] =>
    groups
      .filter(
        (group) =>
          group.kind === kind && `${group.label} ${group.basis}`.toLowerCase().includes(normalizedGroupQuery),
      )
      .map((group) => ({
        id: group.id,
        label: group.label,
        count: countWith((session) => sessionGroups.get(sessionKey(session))?.id === group.id, 'group'),
        selected: preferences.groupFilters.includes(group.id),
        onClick: () => toggleGroup(group.id),
      }));
  const groupSections = GROUP_KINDS.map((kind) => ({ kind, options: groupOptions(kind) })).filter(
    ({ options }) => options.length > 0,
  );
  const activeChips = [
    ...preferences.statusFilters.map((value) => ({
      kind: 'Status',
      value,
      label: STATUS_META[value].label,
      remove: () =>
        patchPreferences({
          statusFilters: preferences.statusFilters.filter((item) => item !== value),
        }),
    })),
    ...preferences.groupFilters.map((value) => ({
      kind: 'Group',
      value,
      label: groups.find((group) => group.id === value)?.label ?? value,
      remove: () =>
        patchPreferences({ groupFilters: preferences.groupFilters.filter((item) => item !== value) }),
    })),
    ...preferences.machineFilters.map((value) => ({
      kind: 'Machine',
      value,
      label: machines.find((machine) => machine.sourceId === value)?.label ?? value,
      remove: () =>
        patchPreferences({
          machineFilters: preferences.machineFilters.filter((item) => item !== value),
        }),
    })),
    ...preferences.agentFilters.map((value) => ({
      kind: 'Agent',
      value,
      label: getVendor(value).label,
      remove: () =>
        patchPreferences({ agentFilters: preferences.agentFilters.filter((item) => item !== value) }),
    })),
  ];
  const sections = STATUS_ORDER.map((status) => ({
    status,
    rows: visibleSessions.filter((session) => sessionStatusGroup(session) === status),
  })).filter(({ rows }) => rows.length > 0);
  const reviewIndex = useMemo(() => buildReviewIndex(sessions), [sessions]);

  return (
    <div className={cn(styles.shell, inspectorOpen && 'inspector-open')}>
      <CoslashHeader
        machines={machines}
        diagnostics={diagnostics}
        onSettings={onSettings}
        theme={theme}
        onThemeChange={onThemeChange}
        themeDisabled={themeDisabled}
        onRetry={onRetry}
        retrying={retrying}
        actions={headerActions}
      />
      {banner}
      <div className="flex items-start">
        <aside className={styles.sidebar} aria-label="Filters">
          {facetSections.map((section) => (
            <SidebarSection
              key={section.id}
              section={section}
              open={sideOpen[section.id]}
              isLoading={isLoading}
              onToggle={() => setSideOpen((state) => ({ ...state, [section.id]: !state[section.id] }))}
            />
          ))}
          <div className="pb-3.5">
            <div className="sticky top-[-20px] z-20 bg-[var(--coslash-surface)] pb-1">
              <button
                className={styles.sideHeading}
                aria-expanded={sideOpen.group}
                aria-controls="coslash-group"
                onClick={() => setSideOpen((state) => ({ ...state, group: !state.group }))}
              >
                <ChevronRight
                  className={cn('size-[13px] transition-transform', sideOpen.group && 'rotate-90')}
                />
                <span className="flex flex-1 items-center justify-between">
                  Detected groups
                  {isLoading && <InlineSpinner />}
                </span>
              </button>
              {sideOpen.group && (
                <div className="mx-2 mt-1 mb-1.5 flex items-center gap-1.5 rounded-[7px] border border-[var(--coslash-line)] bg-[var(--coslash-bg)] px-2 py-1.5 text-[var(--coslash-muted)] focus-within:border-[var(--coslash-accent)] [&>svg]:size-3.5">
                  <Search />
                  <input
                    type="search"
                    value={groupQuery}
                    onChange={(event) => setGroupQuery(event.target.value)}
                    placeholder="Filter groups"
                    aria-label="Filter detected groups"
                    className="min-w-0 flex-1 bg-transparent text-xs text-[var(--coslash-ink)] outline-none [&::-webkit-search-cancel-button]:hidden"
                  />
                  {groupQuery && (
                    <button
                      type="button"
                      className="grid size-5 place-items-center rounded hover:bg-[var(--coslash-soft)] [&>svg]:size-3"
                      onClick={() => setGroupQuery('')}
                      aria-label="Clear group filter"
                    >
                      <X />
                    </button>
                  )}
                </div>
              )}
            </div>
            {sideOpen.group && (
              <div id="coslash-group">
                {groupSections.map(({ kind, options }) => (
                  <div key={kind}>
                    <div className="flex items-center gap-1.5 px-2.5 pt-2 pb-1 text-[11px] font-semibold text-[var(--coslash-muted)] [&>svg]:size-3.5">
                      {kind === 'Repository' && <FolderGit2 />}
                      {kind === 'Folder' && <Folder />}
                      {GROUP_LABELS[kind]}
                    </div>
                    <FacetRows options={options} />
                  </div>
                ))}
                {groupSections.length === 0 && (
                  <div className="px-2.5 py-2 text-xs text-[var(--coslash-muted)]">
                    No groups match “{groupQuery}”.
                  </div>
                )}
              </div>
            )}
          </div>
        </aside>

        <main className={styles.main}>
          <div className="flex flex-col gap-3">
            <div className={styles.search}>
              {activeChips.length > 0 && (
                <button
                  type="button"
                  className="grid size-6 shrink-0 cursor-pointer place-items-center rounded-md border border-[var(--coslash-tint-line)] bg-[var(--coslash-tint)] text-[var(--coslash-accent-ink)] hover:bg-[var(--coslash-tint-line)] [&>svg]:size-3"
                  onClick={clearFacets}
                  aria-label="Clear all scopes"
                  title="Clear all scopes"
                >
                  <X />
                </button>
              )}
              <div className="flex min-w-0 flex-1 [scrollbar-width:none] items-center gap-1.5 overflow-x-auto [&::-webkit-scrollbar]:hidden">
                {activeChips.map((chip) => (
                  <span className={styles.chip} key={`${chip.kind}:${chip.value}`}>
                    <span className="font-normal opacity-70">{chip.kind}:</span> {chip.label}
                    <button
                      type="button"
                      className="grid size-4 cursor-pointer place-items-center rounded hover:bg-[var(--coslash-tint-line)] [&>svg]:size-2.5"
                      aria-label={`Remove ${chip.label}`}
                      onClick={chip.remove}
                    >
                      <X />
                    </button>
                  </span>
                ))}
                {activeChips.length === 0 && (
                  <Search className="size-4 shrink-0 text-[var(--coslash-muted)]" aria-hidden="true" />
                )}
                <input
                  type="search"
                  value={preferences.query}
                  onChange={(event) => patchPreferences({ query: event.target.value })}
                  placeholder="Search sessions -- title, repo, branch"
                  aria-label="Search titles, outcomes, files and context"
                  className="min-w-32 flex-1 bg-transparent text-[13px] outline-none [&::-webkit-search-cancel-button]:hidden"
                />
              </div>
              {preferences.query && (
                <button
                  type="button"
                  className="grid size-7 cursor-pointer place-items-center rounded-[7px] hover:bg-[var(--coslash-soft)] [&>svg]:size-4"
                  onClick={() => patchPreferences({ query: '' })}
                  aria-label="Clear search"
                >
                  <X />
                </button>
              )}
              <kbd className="rounded-[5px] border border-[var(--coslash-line)] px-1.5 py-1 text-[11px] leading-none text-[var(--coslash-muted)]">
                ⌘ K
              </kbd>
            </div>
            <div className="flex items-center justify-between gap-3 max-[900px]:flex-wrap">
              <Rollup sessions={visibleSessions} isLoading={isLoading} />
              <div className="flex shrink-0 items-center gap-2">
                <div className={styles.segmented} aria-label="Time range">
                  {RANGE_OPTIONS.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      className={cn(
                        'min-h-7 cursor-pointer rounded-[7px] px-2.5 py-1 text-[11px] text-[var(--coslash-muted)]',
                        range === option.value &&
                          'bg-[var(--coslash-surface)] font-semibold text-[var(--coslash-ink)] shadow-sm',
                      )}
                      onClick={() => onRangeChange(option.value)}
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
                <span className="h-7 w-px bg-[var(--coslash-line)]" aria-hidden="true" />
                <div className={styles.segmented} aria-label="View">
                  {(['board', 'list'] as const).map((value) => (
                    <button
                      key={value}
                      type="button"
                      className={cn(
                        'min-h-7 cursor-pointer rounded-[7px] px-2.5 py-1 text-[11px] text-[var(--coslash-muted)]',
                        preferences.view === value &&
                          'bg-[var(--coslash-surface)] font-semibold text-[var(--coslash-ink)] shadow-sm',
                      )}
                      onClick={() => patchPreferences({ view: value })}
                    >
                      {value === 'list' ? 'Table' : 'Board'}
                    </button>
                  ))}
                </div>
                {preferences.view === 'list' && (
                  <button
                    type="button"
                    className="grid min-h-8 min-w-8 cursor-pointer place-items-center rounded-[9px] border border-[var(--coslash-line)] bg-[var(--coslash-surface)] text-[var(--coslash-muted)] hover:bg-[var(--coslash-soft)] [&>svg]:size-3.5"
                    aria-label={
                      preferences.density === 'comfortable' ? 'Use compact rows' : 'Use comfortable rows'
                    }
                    title={
                      preferences.density === 'comfortable' ? 'Use compact rows' : 'Use comfortable rows'
                    }
                    onClick={() =>
                      patchPreferences({
                        density: preferences.density === 'comfortable' ? 'compact' : 'comfortable',
                      })
                    }
                  >
                    {preferences.density === 'comfortable' ? (
                      <Rows3 aria-hidden="true" />
                    ) : (
                      <Rows4 aria-hidden="true" />
                    )}
                  </button>
                )}
              </div>
            </div>
          </div>

          <div className={cn(styles.tableWrap, 'mt-3')}>
            <LoadingSpinner isLoading={isLoading && sessions.length === 0}>
              {loadError ? (
                <div className={styles.empty} role="alert">
                  <AlertTriangle className="size-6 text-[var(--coslash-muted)]" />
                  <h3 className="text-[15px] font-[650]">Sessions could not be loaded</h3>
                  <p className="max-w-[430px] text-[12.5px] leading-[1.6] text-[var(--coslash-muted)]">
                    {loadError}
                  </p>
                  <Button variant="outline" onClick={onRetry}>
                    Try again
                  </Button>
                </div>
              ) : visibleSessions.length === 0 ? (
                (emptyContent ?? (
                  <div className={styles.empty}>
                    <Search className="size-6 text-[var(--coslash-muted)]" />
                    <h3 className="text-[15px] font-[650]">Nothing in this scope</h3>
                    <p className="max-w-[430px] text-[12.5px] leading-[1.6] text-[var(--coslash-muted)]">
                      Nothing matched the recorded titles, goals, outcomes, files or context.
                    </p>
                    <Button
                      variant="outline"
                      onClick={() => {
                        patchPreferences({ query: '' });
                        clearFacets();
                        onRangeChange('all');
                      }}
                    >
                      Clear everything
                    </Button>
                  </div>
                ))
              ) : preferences.view === 'board' ? (
                <Suspense fallback={<div className={styles.empty}>Loading board…</div>}>
                  <div className="min-w-[1120px]">
                    <SessionBoard
                      sessions={visibleSessions}
                      onSelectSession={onSelectSession}
                      showMachineBadge={machines.some((machine) => machine.sourceId !== LOCAL_SOURCE_ID)}
                      review={{
                        index: reviewIndex,
                        reviewerOptions,
                        onStarted: onReviewStarted,
                      }}
                    />
                  </div>
                </Suspense>
              ) : (
                <SessionListView
                  sections={sections}
                  groups={sessionGroups}
                  selectedSessionKey={selectedSessionKey}
                  compact={preferences.density === 'compact'}
                  sort={preferences.sort}
                  openSections={openSections}
                  sectionLimits={sectionLimits}
                  onSort={setSortKey}
                  onToggleSection={(status) =>
                    setOpenSections((state) => ({ ...state, [status]: !state[status] }))
                  }
                  onShowMore={(status, limit) =>
                    setSectionLimits((limits) => ({ ...limits, [status]: limit + 50 }))
                  }
                  onSelectSession={onSelectSession}
                  onToggleGroup={toggleGroup}
                  review={{
                    index: reviewIndex,
                    reviewerOptions,
                    onStarted: onReviewStarted,
                    onSelectRelated: onSelectSession,
                  }}
                />
              )}
            </LoadingSpinner>
          </div>
        </main>
      </div>
    </div>
  );
}
