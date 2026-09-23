import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ChevronDown,
  ChevronRight,
  Ellipsis,
  Folder,
  FolderGit2,
  GitCompareArrows,
  LoaderCircle,
  Monitor,
  Moon,
  RefreshCw,
  Rows3,
  Rows4,
  ScanSearch,
  Search,
  Server,
  Settings,
  Sun,
  X,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import type { Theme } from '@/lib/theme';
import { cn } from '@/lib/utils';
import { LoadingSpinner } from '@/pages/coslash/components/LoadingSpinner';
import { ReviewDialog } from '@/pages/coslash/components/ReviewDialog';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTimeAgo } from '@/pages/coslash/lib/format';
import {
  MACHINE_TONE_DOT,
  machineRetryable,
  machineStatusText,
  machineTone,
  needsBanner,
  needsSettings,
} from '@/pages/coslash/lib/machine-status';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  availableReviewers,
  buildReviewIndex,
  type ReviewerOption,
  type ReviewIndex,
} from '@/pages/coslash/lib/review';
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
  STATUS_ORDER,
  STATUSES,
  sumKnown,
  type Session,
  type StatusKey,
} from '@/pages/coslash/lib/session';
import {
  BOARD_GROUP_BY_OPTIONS,
  BOARD_ROW_GROUP_BY_OPTIONS,
  boardGroupByLabel,
  type BoardGroupBy,
  type BoardRowGroupBy,
} from '@/pages/coslash/lib/session-grouping';
import { ALL_REPOSITORIES, filterSessionLibrary } from '@/pages/coslash/lib/session-library';
import {
  loadSessionViewPreferences,
  saveSessionViewPreferences,
  type SessionRange,
  type SessionSort,
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
  status?: StatusKey;
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

const STATUS_HINT: Record<StatusKey, string> = {
  busy: 'Working right now',
  waiting: 'Blocked on your input',
  idle: 'Open, but quiet',
  inactive: 'Not running',
};
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
    'coslash-shell flex h-svh flex-col overflow-hidden bg-coslash-bg text-ui leading-[1.45] text-coslash-ink antialiased',
  header:
    'flex h-[60px] items-center justify-between gap-3.5 border-b border-coslash-line bg-coslash-surface px-5 max-compact:px-3',
  banner:
    'flex items-center gap-2.5 border-b border-danger-fg bg-danger-bg px-5 py-2.5 text-xs text-danger-fg [&>svg]:size-4',
  sidebar:
    'coslash-sidebar w-[214px] shrink-0 overflow-y-auto border-r border-coslash-line bg-coslash-surface px-3 py-5 [scrollbar-width:none] max-sidebar:hidden [&::-webkit-scrollbar]:hidden',
  sideHeading:
    'flex min-h-[30px] w-full cursor-pointer items-center gap-[7px] rounded-[7px] px-2.5 py-1.5 text-left text-meta font-[650] tracking-[.09em] text-coslash-muted uppercase transition-colors hover:bg-coslash-soft hover:text-coslash-ink',
  facet:
    'flex min-h-8 w-full cursor-pointer items-center gap-[9px] rounded-[7px] px-2.5 py-[7px] text-left text-xs leading-[1.45] text-coslash-muted transition-colors hover:bg-coslash-soft [&>svg]:size-3.5',
  main: 'coslash-main flex min-h-0 min-w-0 flex-1 flex-col px-6 pt-5 pb-5 max-sidebar:w-full max-sidebar:p-4',
  search:
    'flex h-12 w-full min-w-0 items-center gap-1.5 rounded-[9px] border border-coslash-line bg-coslash-surface px-3.5 focus-within:border-coslash-accent focus-within:shadow-[0_0_0_3px_var(--coslash-tint)]',
  chip: 'inline-flex h-6 shrink-0 items-center gap-1 rounded-md border border-coslash-tint-line bg-coslash-tint pr-1 pl-2 text-meta font-[550] whitespace-nowrap text-coslash-accent-ink',
  segmented: 'inline-flex rounded-[10px] bg-coslash-soft p-0.5',
  tableWrap: 'min-h-0 flex-1 overflow-auto rounded-[10px] border border-coslash-line bg-coslash-surface',
  head: 'sticky top-0 z-12 border-b border-coslash-line bg-coslash-surface text-left text-meta font-[650] tracking-[.07em] text-coslash-muted uppercase',
  headButton:
    'flex min-h-[34px] w-full cursor-pointer items-center gap-[5px] px-2.5 py-[9px] text-left font-[inherit] tracking-[inherit] uppercase transition-colors hover:bg-coslash-soft hover:text-coslash-ink [&>svg]:size-3',
  cell: 'overflow-hidden px-2.5 py-2 align-middle text-cell',
  empty: 'flex min-h-60 flex-col items-center justify-center gap-2 px-6 py-11 text-center',
};

function statusDot(status: StatusKey): string {
  return status === 'inactive' ? 'bg-coslash-neutral-dot' : STATUSES[status].dot;
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

/** The collector reports only a basename for a local-only repository, merging same-named folders. */
function localRepositoryRoot(cwd: string, repo: string): string {
  const parts = cwd.trim().split('/');
  const depth = parts.lastIndexOf(repo.trim());
  return depth === -1 ? cwd.trim() || repo : parts.slice(0, depth + 1).join('/');
}

function detectedGroup(session: Session): Group {
  if (session.repo?.trim() && !session.repoLocalOnly) {
    const label = session.repo.split('/').filter(Boolean).at(-1) ?? session.repo;
    return {
      id: `repo:${session.repo.toLowerCase()}`,
      label,
      kind: 'Repository',
      basis: session.repo,
    };
  }
  if (session.repo?.trim()) {
    const root = localRepositoryRoot(session.cwd, session.repo);
    return {
      id: `folder:${session.sourceId}:${root.toLowerCase()}`,
      label: root.split('/').filter(Boolean).slice(-2).join('/'),
      kind: 'Folder',
      basis: root,
    };
  }
  const cwd = session.cwd.trim();
  if (cwd) {
    const parts = cwd.split('/').filter(Boolean);
    const parent = parts.slice(0, -1).join('/') || parts.join('/');
    return {
      id: `folder:${session.sourceId}:${parent}`,
      label: `${session.sourceLabel} · ${parts.at(-2) ?? parts.at(-1) ?? 'Folder'}`,
      kind: 'Folder',
      basis: parent ? `/${parent}` : cwd,
    };
  }
  return {
    // One bucket, so the sidebar shows a single row rather than one per agent.
    id: 'unlocated',
    label: 'No location',
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
    return STATUSES[boardStatusKey(session)].label.toLowerCase().includes(value);
  });
}

function matchesFacets(
  session: Session,
  group: Group,
  preferences: SessionViewPreferences,
  skip?: FacetKey,
): boolean {
  const { statusFilters, groupFilters, machineFilters, agentFilters } = preferences;
  if (skip !== 'status' && statusFilters.length > 0 && !statusFilters.includes(boardStatusKey(session)))
    return false;
  if (skip !== 'group' && groupFilters.length > 0 && !groupFilters.includes(group.id)) return false;
  if (skip !== 'machine' && machineFilters.length > 0 && !machineFilters.includes(session.sourceId))
    return false;
  if (skip !== 'agent' && agentFilters.length > 0 && !agentFilters.includes(session.agent)) return false;
  return true;
}

/** Unlike cost, a session with no recorded tokens contributes nothing rather than voiding the total. */
function tokenTotal(sessions: Session[]): number | null {
  const values = sessions.map((session) => getTotalTokens(session.tokens));
  return values.every((value) => value == null)
    ? null
    : values.reduce<number>((total, value) => total + (value ?? 0), 0);
}

function sessionTitle(session: Session): string {
  return session.name ?? session.firstPrompt ?? 'Untitled session';
}

function sortSessions(sessions: Session[], sort: SessionSort): Session[] {
  return [...sessions].sort((a, b) => {
    // Rows whose liveness is unknown stay below current rows whatever the sort key.
    if (a.displayStale !== b.displayStale) return a.displayStale ? 1 : -1;
    if (sort.key === 'title') {
      const difference = sessionTitle(a).localeCompare(sessionTitle(b));
      return sort.dir === 'asc' ? difference : -difference;
    }
    if (sort.key === 'cost') {
      if (a.cost == null || b.cost == null) return a.cost == null ? (b.cost == null ? 0 : 1) : -1;
      return sort.dir === 'asc' ? a.cost - b.cost : b.cost - a.cost;
    }
    return sort.dir === 'asc' ? a.mtime - b.mtime : b.mtime - a.mtime;
  });
}

function MachineDot({
  machine,
  onRetry,
  retrying,
}: {
  machine: MachineFact;
  onRetry: () => void;
  retrying: boolean;
}) {
  const tone = machineTone(machine);
  const retryable = machineRetryable(machine) && !retrying;
  const status = machineStatusText(machine);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn('size-[7px] shrink-0 rounded-full', MACHINE_TONE_DOT[tone])}
          role="img"
          aria-label={status}
        />
      </TooltipTrigger>
      {/* Portaled outside the shell, so the tokens have to be re-scoped here. */}
      <TooltipContent className="coslash-shell bg-coslash-surface text-coslash-ink border-coslash-line [&_svg]:bg-coslash-surface [&_svg]:fill-coslash-surface flex-col items-start gap-1 border">
        <span>{status}</span>
        {retrying && <span className="text-coslash-muted">Retrying…</span>}
        {retryable && (
          <button
            type="button"
            className="cursor-pointer font-semibold underline underline-offset-2"
            onClick={onRetry}
          >
            Retry the connection
          </button>
        )}
      </TooltipContent>
    </Tooltip>
  );
}

function InlineSpinner() {
  return <LoaderCircle className="text-coslash-muted size-3.5 animate-spin" aria-hidden="true" />;
}

function Rollup({ sessions, isLoading }: { sessions: Session[]; isLoading: boolean }) {
  const aggregate = sessionsForAggregates(sessions);
  const unpriced = aggregate.filter((session) => session.cost == null || session.unpricedModels.length > 0);
  return (
    <div className="text-cell max-narrow:w-full flex min-w-0 flex-nowrap items-center gap-2 overflow-hidden whitespace-nowrap">
      {isLoading && <span className="sr-only">Refreshing sessions</span>}
      <strong className="flex items-center gap-1 font-[650]">
        {isLoading ? <InlineSpinner /> : sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}
      </strong>
      <span className="text-coslash-muted">active in this scope</span>
      <span className="text-coslash-muted">·</span>
      <span className="text-coslash-muted flex items-center gap-1">
        {isLoading ? <InlineSpinner /> : formatTokens(tokenTotal(aggregate))} tokens
      </span>
      <span className="text-coslash-muted">·</span>
      {isLoading ? (
        <InlineSpinner />
      ) : (
        <UnpricedModelWarning unpriced={unpriced.flatMap((session) => session.unpricedModels)}>
          {formatEstimatedCost(sumKnown(aggregate.map((session) => session.cost)))}
        </UnpricedModelWarning>
      )}
      {!isLoading && unpriced.length > 0 && (
        <span className="text-coslash-muted max-actions:hidden">· {unpriced.length} not priced</span>
      )}
      <span className="text-meta text-coslash-muted max-actions:hidden">at list API prices</span>
    </div>
  );
}

function FacetRow({ label, count, selected, icon, indicator, status, onClick }: FacetOption) {
  return (
    <div
      className={cn(styles.facet, 'relative gap-1.5', {
        'bg-coslash-tint text-coslash-accent-ink font-semibold': selected,
        'opacity-55': count === 0 && !selected,
      })}
    >
      {/* Stretched over the row, so the whole row still toggles the facet. */}
      <button
        type="button"
        className="flex min-w-0 cursor-pointer items-center gap-[9px] text-left after:absolute after:inset-0 [&>svg]:size-3.5"
        aria-pressed={selected}
        onClick={onClick}
      >
        {status ? <span className={cn('size-[7px] shrink-0 rounded-full', statusDot(status))} /> : icon}
        <span className="truncate">{label}</span>
      </button>
      {indicator && <span className="relative flex">{indicator}</span>}
      <span className="text-meta ml-auto tabular-nums">{count}</span>
    </div>
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
      <div className="flex items-center">
        <button
          className={styles.sideHeading}
          aria-expanded={open}
          aria-controls={contentId}
          onClick={onToggle}
        >
          <ChevronRight className={cn('size-[13px] transition-transform', { 'rotate-90': open })} />
          <span className="flex flex-1 items-center justify-between">
            {section.label}
            {isLoading && <InlineSpinner />}
          </span>
        </button>
      </div>
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
  const problems = machines.filter(needsBanner);
  return (
    <>
      <div className={styles.header}>
        <div className="flex min-w-0 items-center gap-3.5">
          <span aria-label="coSlash">
            <img src="/brand/coslash-logo.svg" alt="" className="w-[104px] dark:hidden" />
            <img src="/brand/coslash-logo-reverse.svg" alt="" className="hidden w-[104px] dark:block" />
          </span>
          <span className="text-meta text-coslash-muted max-sidebar:hidden truncate">
            Run more agents. Lose less context.
          </span>
        </div>
        <div className="flex items-center gap-2">
          {actions}
          <div className="bg-coslash-soft rounded-[10px] p-0.5">{diagnostics}</div>
          <div className="bg-coslash-soft rounded-[10px] p-0.5">
            <Button
              variant="ghost"
              size="sm"
              className="text-meta min-h-7 cursor-pointer gap-1.5 rounded-[7px] px-2.5 font-[550] [&>svg]:size-3.5"
              onClick={onSettings}
            >
              <Settings /> Settings
            </Button>
          </div>
          <div
            className="bg-coslash-soft inline-flex items-center gap-0.5 rounded-[10px] p-0.5"
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
                  'text-coslash-muted hover:bg-coslash-surface grid size-7 cursor-pointer place-items-center rounded-[7px] [&>svg]:size-3.5',
                  { 'bg-coslash-surface text-coslash-ink shadow-sm': theme === value },
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
            {problems.length === 1
              ? machineStatusText(problems[0])
              : 'need attention — open Settings for guidance.'}
          </span>
          {problems.some(needsSettings) ? (
            <Button
              variant="outline"
              size="sm"
              className="border-danger-fg text-danger-fg ml-auto bg-transparent"
              onClick={onSettings}
            >
              <Settings /> Open Settings
            </Button>
          ) : (
            <Button
              variant="outline"
              size="sm"
              className="border-danger-fg text-danger-fg ml-auto bg-transparent"
              onClick={onRetry}
              disabled={retrying}
            >
              <RefreshCw className={cn({ 'animate-spin': retrying })} /> Retry
            </Button>
          )}
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
  status: StatusKey;
  selected: boolean;
  compact: boolean;
  onSelect: () => void;
  onToggleGroup: () => void;
  review: SessionReviewProps;
}) {
  const [reviewOpen, setReviewOpen] = useState(false);
  const actionButtonRef = useRef<HTMLButtonElement>(null);
  const openingReviewRef = useRef(false);
  const readiness = sessionReadiness(session);
  const vendor = getVendor(session.agent);
  const key = sessionKey(session);
  const reviewLink = review.index.links.get(key);
  const showReviewAction =
    isLocalSession(session) && session.cwd.trim() !== '' && !review.index.reviewSessions.has(key);
  const reviewActive = session.reviewPending || review.index.activeOrigins.has(key);
  const reviewDisabled =
    reviewActive || availableReviewers(review.reviewerOptions, session.agent).length === 0;
  const cell = cn(styles.cell, { 'py-[5px]': compact });
  const hideWhenCompact = { hidden: compact };
  return (
    <tr
      className={cn(
        'group hover:[&>td]:bg-coslash-soft cursor-pointer [&>td]:transition-colors',
        { 'border-coslash-line-soft border-b': !compact },
        selected &&
          '[&>td]:bg-coslash-tint hover:[&>td]:bg-coslash-tint [&>td:first-child]:shadow-[inset_3px_0_0_var(--coslash-accent)]',
      )}
      onClick={onSelect}
    >
      <td className={cn(cell, 'w-auto')}>
        <button
          type="button"
          className="text-ui group-hover:text-coslash-accent block w-full cursor-pointer truncate px-1.5 py-px text-left leading-[1.35] font-semibold transition-colors"
          onClick={onSelect}
        >
          {sessionTitle(session)}
        </button>
        <span
          className={cn(
            'text-coslash-muted mt-0.5 block truncate px-1.5 text-[11.5px] leading-[1.4]',
            hideWhenCompact,
          )}
        >
          {getSessionCardSummary(session)}
        </span>
      </td>
      <td className={cn(cell, 'max-compact:hidden w-[204px]')}>
        <div className="text-coslash-muted flex min-w-0 items-baseline gap-1.5">
          <button
            type="button"
            className="text-cell hover:text-coslash-accent max-w-full cursor-pointer truncate px-1.5 py-[1.5px] text-left transition-colors"
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
            {formatTableCost(session.cost)}
          </UnpricedModelWarning>
        </div>
        <span className={cn('text-meta text-coslash-muted mt-0.5 block', hideWhenCompact)}>
          {formatTokens(getTotalTokens(session.tokens))} tokens
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
                disabled={reviewDisabled}
                onSelect={() => {
                  openingReviewRef.current = true;
                  setReviewOpen(true);
                }}
              >
                {reviewActive ? <LoaderCircle className="animate-spin" /> : <ScanSearch />}
                {reviewActive ? 'Review running' : session.reviewError ? 'Retry review' : 'Send for review'}
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
            reviewerOptions={review.reviewerOptions}
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

function BoardGroupByMenu<T extends BoardRowGroupBy>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="border-coslash-line bg-coslash-surface text-coslash-muted hover:bg-coslash-soft text-meta flex min-h-8 cursor-pointer items-center gap-1.5 rounded-[9px] border px-2.5 whitespace-nowrap [&>svg]:size-3"
          aria-label={`${label} grouped by ${boardGroupByLabel(value)}`}
        >
          {label}
          <span className="text-coslash-ink font-[550]">{boardGroupByLabel(value)}</span>
          <ChevronDown aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="coslash-shell min-w-40">
        <DropdownMenuRadioGroup value={value} onValueChange={(next) => onChange(next as T)}>
          {options.map((option) => (
            <DropdownMenuRadioItem key={option.value} value={option.value} className="text-meta">
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
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
  onToggleGroup: (id: string) => void;
  review: SessionReviewProps;
}) {
  const headRef = useRef<HTMLTableSectionElement>(null);
  // Section bands pin directly under the header row, whose height changes when its labels wrap.
  const [headHeight, setHeadHeight] = useState(34);

  useEffect(() => {
    const head = headRef.current;
    if (head == null) return;
    const observer = new ResizeObserver(() => setHeadHeight(head.getBoundingClientRect().height));
    observer.observe(head);
    return () => observer.disconnect();
  }, []);

  return (
    <table
      className="max-compact:table-auto w-full table-fixed border-separate border-spacing-0"
      style={{ '--coslash-head-height': `${headHeight}px` } as CSSProperties}
    >
      <thead ref={headRef}>
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
        const aggregate = sessionsForAggregates(rows);
        return (
          <tbody key={status}>
            <tr>
              <th
                colSpan={6}
                className="border-coslash-line-soft bg-coslash-soft sticky top-[var(--coslash-head-height)] z-11 border-y text-left"
              >
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
  onRetrySessions,
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
  onRetrySessions: () => void;
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
  const [openSections, setOpenSections] = useState<Record<StatusKey, boolean>>({
    busy: true,
    waiting: true,
    idle: true,
    inactive: true,
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
  const sessionsInRange = useMemo(() => {
    const start = rangeStart(range);
    return sessions.filter((session) => start == null || session.status != null || session.mtime >= start);
  }, [sessions, range]);
  const searchMatches = useMemo(
    () =>
      new Map(
        sessionsInRange.map((session) => [
          sessionKey(session),
          matchesSearch(session, sessionGroups.get(sessionKey(session))!, preferences.query),
        ]),
      ),
    [preferences.query, sessionGroups, sessionsInRange],
  );
  const visibleSessions = useMemo(
    () =>
      sortSessions(
        sessionsInRange.filter(
          (session) =>
            searchMatches.get(sessionKey(session)) &&
            matchesFacets(session, sessionGroups.get(sessionKey(session))!, preferences),
        ),
        preferences.sort,
      ),
    [preferences, searchMatches, sessionGroups, sessionsInRange],
  );
  const countWith = (predicate: (session: Session) => boolean, skip: FacetKey) =>
    sessionsInRange.filter(
      (session) =>
        searchMatches.get(sessionKey(session)) &&
        matchesFacets(session, sessionGroups.get(sessionKey(session))!, preferences, skip) &&
        predicate(session),
    ).length;
  const toggleStatus = (status: StatusKey) =>
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
        label: STATUSES[status].label,
        count: countWith((session) => boardStatusKey(session) === status, 'status'),
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
          machine.sourceId === LOCAL_SOURCE_ID ? undefined : (
            <MachineDot machine={machine} onRetry={onRetry} retrying={retrying} />
          ),
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
      label: STATUSES[value].label,
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
    rows: visibleSessions.filter((session) => boardStatusKey(session) === status),
  })).filter(({ rows }) => rows.length > 0);
  const reviewIndex = useMemo(() => buildReviewIndex(sessions), [sessions]);

  return (
    <TooltipProvider>
      <div className={cn(styles.shell, { 'inspector-open': inspectorOpen })}>
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
        <div className="flex min-h-0 flex-1">
          <div className={styles.sidebar} role="region" aria-label="Filters">
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
              <div className="bg-coslash-surface sticky top-[-20px] z-20 pb-1">
                <button
                  className={styles.sideHeading}
                  aria-expanded={sideOpen.group}
                  aria-controls="coslash-group"
                  onClick={() => setSideOpen((state) => ({ ...state, group: !state.group }))}
                >
                  <ChevronRight
                    className={cn('size-[13px] transition-transform', { 'rotate-90': sideOpen.group })}
                  />
                  <span className="flex flex-1 items-center justify-between">
                    Detected groups
                    {isLoading && <InlineSpinner />}
                  </span>
                </button>
                {sideOpen.group && (
                  <div className="border-coslash-line bg-coslash-bg text-coslash-muted focus-within:border-coslash-accent mx-2 mt-1 mb-1.5 flex items-center gap-1.5 rounded-[7px] border px-2 py-1.5 [&>svg]:size-3.5">
                    <Search />
                    <input
                      type="search"
                      value={groupQuery}
                      onChange={(event) => setGroupQuery(event.target.value)}
                      placeholder="Filter groups"
                      aria-label="Filter detected groups"
                      className="text-coslash-ink min-w-0 flex-1 bg-transparent text-xs outline-none [&::-webkit-search-cancel-button]:hidden"
                    />
                    {groupQuery && (
                      <button
                        type="button"
                        className="hover:bg-coslash-soft grid size-5 cursor-pointer place-items-center rounded transition-colors [&>svg]:size-3"
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
                      {kind !== 'No location' && (
                        <div className="text-meta text-coslash-muted flex items-center gap-1.5 px-2.5 pt-2 pb-1 font-semibold [&>svg]:size-3.5">
                          {kind === 'Repository' && <FolderGit2 />}
                          {kind === 'Folder' && <Folder />}
                          {GROUP_LABELS[kind]}
                        </div>
                      )}
                      <FacetRows options={options} />
                    </div>
                  ))}
                  {groupSections.length === 0 && (
                    <div className="text-coslash-muted px-2.5 py-2 text-xs">
                      No groups match “{groupQuery}”.
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>

          <div className={styles.main}>
            <div className="flex shrink-0 flex-col gap-3">
              <div className={styles.search}>
                {activeChips.length > 0 && (
                  <button
                    type="button"
                    className="border-coslash-tint-line bg-coslash-tint text-coslash-accent-ink hover:bg-coslash-tint-line grid size-6 shrink-0 cursor-pointer place-items-center rounded-md border [&>svg]:size-3"
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
                        className="hover:bg-coslash-tint-line grid size-4 cursor-pointer place-items-center rounded [&>svg]:size-2.5"
                        aria-label={`Remove ${chip.label}`}
                        onClick={chip.remove}
                      >
                        <X />
                      </button>
                    </span>
                  ))}
                  {activeChips.length === 0 && (
                    <Search className="text-coslash-muted size-4 shrink-0" aria-hidden="true" />
                  )}
                  <input
                    type="search"
                    value={preferences.query}
                    onChange={(event) => patchPreferences({ query: event.target.value })}
                    placeholder="Search sessions -- title, repo, prompt, recap"
                    aria-label="Search session metadata and local prompts, recaps, summaries, goals, and syntheses"
                    className="text-ui min-w-32 flex-1 bg-transparent outline-none [&::-webkit-search-cancel-button]:hidden"
                  />
                </div>
                {preferences.query && (
                  <button
                    type="button"
                    className="hover:bg-coslash-soft grid size-7 cursor-pointer place-items-center rounded-[7px] [&>svg]:size-4"
                    onClick={() => patchPreferences({ query: '' })}
                    aria-label="Clear search"
                  >
                    <X />
                  </button>
                )}
              </div>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <Rollup sessions={visibleSessions} isLoading={isLoading} />
                <div className="flex max-w-full shrink-0 [scrollbar-width:none] items-center gap-2 overflow-x-auto [&::-webkit-scrollbar]:hidden">
                  <div className={styles.segmented} aria-label="Time range">
                    {RANGE_OPTIONS.map((option) => (
                      <button
                        key={option.value}
                        type="button"
                        className={cn(
                          'text-meta text-coslash-muted hover:text-coslash-ink min-h-7 cursor-pointer rounded-[7px] px-2.5 py-1 transition-colors',
                          range === option.value &&
                            'bg-coslash-surface text-coslash-ink font-semibold shadow-sm',
                        )}
                        onClick={() => onRangeChange(option.value)}
                      >
                        {option.label}
                      </button>
                    ))}
                  </div>
                  <span className="bg-coslash-line h-7 w-px" aria-hidden="true" />
                  <div className={styles.segmented} aria-label="View">
                    {(['board', 'list'] as const).map((value) => (
                      <button
                        key={value}
                        type="button"
                        className={cn(
                          'text-meta text-coslash-muted hover:text-coslash-ink min-h-7 cursor-pointer rounded-[7px] px-2.5 py-1 transition-colors',
                          preferences.view === value &&
                            'bg-coslash-surface text-coslash-ink font-semibold shadow-sm',
                        )}
                        onClick={() => patchPreferences({ view: value })}
                      >
                        {value === 'list' ? 'Table' : 'Board'}
                      </button>
                    ))}
                  </div>
                  {preferences.view === 'board' && (
                    <>
                      <BoardGroupByMenu<BoardGroupBy>
                        label="Columns"
                        value={preferences.boardColumns}
                        options={BOARD_GROUP_BY_OPTIONS}
                        onChange={(boardColumns) => patchPreferences({ boardColumns })}
                      />
                      <BoardGroupByMenu<BoardRowGroupBy>
                        label="Rows"
                        value={preferences.boardRows}
                        options={BOARD_ROW_GROUP_BY_OPTIONS}
                        onChange={(boardRows) => patchPreferences({ boardRows })}
                      />
                    </>
                  )}
                  {preferences.view === 'list' && (
                    <button
                      type="button"
                      className="border-coslash-line bg-coslash-surface text-coslash-muted hover:bg-coslash-soft grid min-h-8 min-w-8 cursor-pointer place-items-center rounded-[9px] border [&>svg]:size-3.5"
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
                    <AlertTriangle className="text-coslash-muted size-6" />
                    <h3 className="text-[15px] font-[650]">Sessions could not be loaded</h3>
                    <p className="text-cell text-coslash-muted max-w-[430px] leading-[1.6]">{loadError}</p>
                    <Button variant="outline" onClick={onRetrySessions}>
                      Try again
                    </Button>
                  </div>
                ) : visibleSessions.length === 0 ? (
                  (emptyContent ?? (
                    <div className={styles.empty}>
                      <Search className="text-coslash-muted size-6" />
                      <h3 className="text-[15px] font-[650]">Nothing in this scope</h3>
                      <p className="text-cell text-coslash-muted max-w-[430px] leading-[1.6]">
                        Nothing matched session metadata or local prompts, recaps, summaries, goals and
                        syntheses.
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
                    <SessionBoard
                      sessions={visibleSessions}
                      columnGroupBy={preferences.boardColumns}
                      rowGroupBy={preferences.boardRows}
                      selectedSessionKey={selectedSessionKey}
                      onSelectSession={onSelectSession}
                      review={{
                        index: reviewIndex,
                        reviewerOptions,
                        onStarted: onReviewStarted,
                        onSelectRelated: onSelectSession,
                      }}
                    />
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
          </div>
        </div>
      </div>
    </TooltipProvider>
  );
}
