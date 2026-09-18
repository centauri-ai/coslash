import { useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ChevronRight,
  Folder,
  Monitor,
  RefreshCw,
  Search,
  Settings,
  X,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { LoadingSpinner } from '@/pages/coslash/components/LoadingSpinner';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTimeAgo } from '@/pages/coslash/lib/format';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  loadNavigatorPreferences,
  saveNavigatorPreferences,
  type NavigatorPreferences,
  type NavigatorRange,
  type NavigatorSort,
  type NavigatorStatus,
} from '@/pages/coslash/lib/navigator-preferences';
import {
  boardStatusKey,
  getSessionCardSummary,
  getTotalTokens,
  getVendor,
  sessionKey,
  sessionReadiness,
  sumKnown,
  type Session,
} from '@/pages/coslash/lib/session';
import { DAY } from '@/pages/coslash/lib/time';
import './workspace-navigator.css';

type Group = {
  id: string;
  label: string;
  kind: 'Repository' | 'Folder parent' | 'No location';
  basis: string;
};
type FacetKey = 'status' | 'machine' | 'agent' | 'group';
type FacetOption = {
  id: string;
  label: string;
  count: number;
  selected: boolean;
  onClick: () => void;
  status?: NavigatorStatus;
  icon?: ReactNode;
};
type FacetSection = { id: FacetKey; label: string; options: FacetOption[] };

const STATUS_META: Record<NavigatorStatus, { label: string; hint: string }> = {
  needs: { label: 'Needs you', hint: 'Blocked on your input' },
  running: { label: 'Running', hint: 'Working right now' },
  idle: { label: 'Idle', hint: 'Stopped — resume whenever you like' },
  archived: { label: 'Archived', hint: 'Done or aged out' },
};
const STATUS_ORDER: NavigatorStatus[] = ['needs', 'running', 'idle', 'archived'];
const RANGE_OPTIONS: { value: NavigatorRange; label: string }[] = [
  { value: 'today', label: 'Today' },
  { value: 'this-week', label: 'This week' },
  { value: 'week', label: '7 days' },
  { value: 'month', label: '30 days' },
  { value: 'all', label: 'All time' },
];
const GROUP_KINDS: Group['kind'][] = ['Repository', 'Folder parent', 'No location'];
const GROUP_LABELS: Record<Group['kind'], string> = {
  'Repository': 'Repositories',
  'Folder parent': 'Other folders',
  'No location': 'No location',
};

const styles = {
  shell: 'navigator-shell min-h-svh bg-[var(--nav-bg)] text-[13px] leading-[1.45] text-[var(--nav-ink)]',
  header:
    'flex min-h-[60px] items-center justify-between gap-3.5 border-b border-[var(--nav-line)] bg-[var(--nav-surface)] px-5',
  hostChip:
    'flex min-h-[30px] items-center gap-1.5 whitespace-nowrap rounded-[7px] border border-transparent bg-[var(--nav-soft)] px-2.5 py-1.5 text-[11px] text-[var(--nav-muted)] disabled:opacity-100',
  banner:
    'flex items-center gap-2.5 border-b border-[var(--nav-clay)] bg-[var(--nav-clay-bg)] px-5 py-2.5 text-xs text-[var(--nav-clay)] [&>svg]:size-4',
  sidebar:
    'navigator-sidebar sticky top-0 min-h-[calc(100svh-60px)] w-[214px] shrink-0 border-r border-[var(--nav-line)] bg-[var(--nav-surface)] px-3 py-5',
  sideHeading:
    'flex min-h-[30px] w-full items-center gap-1.5 rounded-[7px] px-2.5 py-1.5 text-left text-[11px] font-[650] tracking-[.09em] text-[var(--nav-muted)] uppercase hover:bg-[var(--nav-soft)] hover:text-[var(--nav-ink)]',
  facet:
    'flex min-h-8 w-full items-center gap-2 rounded-[7px] px-2.5 py-1.5 text-left text-xs text-[var(--nav-muted)] hover:bg-[var(--nav-soft)] [&>svg]:size-3.5',
  main: 'navigator-main min-w-0 w-[calc(100%-214px)] px-6 pt-5 pb-[60px]',
  search:
    'flex max-w-[920px] items-center gap-2.5 rounded-[9px] border border-[var(--nav-line)] bg-[var(--nav-surface)] px-3.5 py-2.5 focus-within:border-[var(--nav-accent)] focus-within:shadow-[0_0_0_3px_var(--nav-tint)] [&>svg]:size-4',
  chipbar:
    'mt-3.5 flex flex-wrap items-center gap-1.5 rounded-[9px] border border-[var(--nav-line)] bg-[var(--nav-surface)] px-3 py-2.5',
  chip: 'inline-flex min-h-7 items-center gap-1.5 rounded-full border border-[var(--nav-tint-line)] bg-[var(--nav-tint)] py-1.5 pr-1.5 pl-2.5 text-xs font-[550] text-[var(--nav-accent-ink)]',
  segmented: 'inline-flex rounded-lg border border-[var(--nav-line)] bg-[var(--nav-surface)] p-0.5',
  scope: 'mb-3 rounded-[10px] border border-[var(--nav-line)] bg-[var(--nav-surface)] px-4 py-3.5',
  tableWrap:
    'min-h-[180px] overflow-x-auto rounded-[14px] border border-[var(--nav-line)] bg-[var(--nav-surface)]',
  head: 'border-b border-[var(--nav-line)] bg-[var(--nav-surface)] text-left text-xs font-[650] tracking-[.09em] text-[var(--nav-muted)] uppercase',
  headButton:
    'flex min-h-11 w-full items-center gap-1 px-5 py-3 text-left font-[inherit] tracking-[inherit] uppercase hover:bg-[var(--nav-soft)] hover:text-[var(--nav-ink)] [&>svg]:size-3',
  cell: 'overflow-hidden px-5 py-4 align-middle text-[13.5px]',
  empty: 'flex min-h-60 flex-col items-center justify-center gap-2 px-6 py-11 text-center',
};

function navigatorStatus(session: Session): NavigatorStatus {
  const status = boardStatusKey(session);
  if (status === 'waiting') return 'needs';
  if (status === 'busy') return 'running';
  return 'idle';
}

function statusDot(status: NavigatorStatus): string {
  if (status === 'needs') return 'bg-[var(--nav-amber-dot)]';
  if (status === 'running') return 'bg-[var(--nav-green-dot)]';
  return 'bg-[var(--nav-neutral-dot)]';
}

function rangeStart(range: NavigatorRange): number | null {
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
  if (session.repo?.trim()) {
    return {
      id: `repo:${session.repo.toLowerCase()}`,
      label: session.repo.split('/').filter(Boolean).at(-1) ?? session.repo,
      kind: 'Repository',
      basis: session.repo,
    };
  }
  const cwd = session.cwd.trim();
  if (cwd) {
    const parts = cwd.split('/').filter(Boolean);
    const parent = parts.slice(0, -1).join('/');
    return {
      id: `folder:${session.sourceId}:${parent}`,
      label: `${session.sourceLabel} · ${parts.at(-2) ?? parts.at(-1) ?? 'Folder'}`,
      kind: 'Folder parent',
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
  const fields = [
    session.name,
    session.summary,
    session.synthesis?.outcome,
    session.declaredGoal,
    session.firstPrompt,
    session.repo,
    session.branch,
    session.cwd,
    group.label,
    vendor,
    session.sourceLabel,
    ...session.fileEdits.map((file) => file.path),
  ];
  const haystack = fields
    .filter((value) => value?.trim())
    .join('\n')
    .toLowerCase();
  return words.every((word) => {
    const prefix = word.match(/^(repo|group|machine|agent|status):(.+)$/);
    if (!prefix) return haystack.includes(word);
    const [, kind, value] = prefix;
    if (kind === 'repo' || kind === 'group') {
      return `${session.repo ?? ''} ${group.label}`.toLowerCase().includes(value);
    }
    if (kind === 'machine') return session.sourceLabel.toLowerCase().includes(value);
    if (kind === 'agent') return vendor.toLowerCase().includes(value);
    return STATUS_META[navigatorStatus(session)].label.toLowerCase().includes(value);
  });
}

function matchesFilters(
  session: Session,
  group: Group,
  preferences: NavigatorPreferences,
  skip?: FacetKey,
): boolean {
  const { statusFilters, groupFilters, machineFilter, agentFilter, query } = preferences;
  if (skip !== 'status' && statusFilters.length > 0 && !statusFilters.includes(navigatorStatus(session)))
    return false;
  if (skip !== 'group' && groupFilters.length > 0 && !groupFilters.includes(group.id)) return false;
  if (skip !== 'machine' && machineFilter != null && session.sourceId !== machineFilter) return false;
  if (skip !== 'agent' && agentFilter != null && session.agent !== agentFilter) return false;
  return matchesSearch(session, group, query);
}

function sortSessions(sessions: Session[], sort: NavigatorSort): Session[] {
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

function Rollup({ sessions }: { sessions: Session[] }) {
  const unpriced = sessions.filter((session) => session.cost == null || session.unpricedModels.length > 0);
  return (
    <div className="flex flex-wrap items-center gap-2 pt-3 text-[12.5px]">
      <strong className="font-[650]">
        {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}
      </strong>
      <span className="text-[var(--nav-muted)]">active in this scope</span>
      <span className="text-[var(--nav-muted)]">·</span>
      <span className="text-[var(--nav-muted)]">{formatTokens(totalTokens(sessions))} tokens</span>
      <span className="text-[var(--nav-muted)]">·</span>
      <UnpricedModelWarning unpriced={unpriced.flatMap((session) => session.unpricedModels)}>
        {formatEstimatedCost(sumKnown(sessions.map((session) => session.cost)))}
      </UnpricedModelWarning>
      {unpriced.length > 0 && <span className="text-[var(--nav-muted)]">· {unpriced.length} not priced</span>}
      <span className="text-[11px] text-[var(--nav-muted)]">at list API prices</span>
    </div>
  );
}

function FacetRow({ label, count, selected, icon, status, onClick }: FacetOption) {
  return (
    <button
      type="button"
      className={cn(styles.facet, {
        'bg-[var(--nav-tint)] font-semibold text-[var(--nav-accent-ink)]': selected,
        'opacity-55': count === 0 && !selected,
      })}
      aria-pressed={selected}
      onClick={onClick}
    >
      {status ? <span className={cn('size-[7px] shrink-0 rounded-full', statusDot(status))} /> : icon}
      <span className="min-w-0 truncate">{label}</span>
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
  onToggle,
}: {
  section: FacetSection;
  open: boolean;
  onToggle: () => void;
}) {
  const contentId = `navigator-${section.id}`;
  return (
    <div className="pb-3.5">
      <button
        className={styles.sideHeading}
        aria-expanded={open}
        aria-controls={contentId}
        onClick={onToggle}
      >
        <ChevronRight className={cn('size-[13px] transition-transform', open && 'rotate-90')} />
        {section.label}
      </button>
      {open && (
        <div id={contentId}>
          <FacetRows options={section.options} />
        </div>
      )}
    </div>
  );
}

function WorkspaceHeader({
  sessions,
  machines,
  diagnostics,
  onSettings,
  onRetry,
  retrying,
  actions,
}: {
  sessions: Session[];
  machines: MachineFact[];
  diagnostics: ReactNode;
  onSettings: () => void;
  onRetry: () => void;
  retrying: boolean;
  actions?: ReactNode;
}) {
  const problems = machines.filter((machine) => ['stale', 'error'].includes(machine.state));
  return (
    <>
      <div className={styles.header}>
        <div className="flex min-w-0 items-center gap-3.5">
          <span aria-label="coSlash">
            <img src="/brand/coslash-logo.svg" alt="" className="w-[104px] dark:hidden" />
            <img src="/brand/coslash-logo-reverse.svg" alt="" className="hidden w-[104px] dark:block" />
          </span>
          <span className="truncate text-[11px] text-[var(--nav-muted)]">
            Run more agents. Lose less context.
          </span>
        </div>
        <div className="flex items-center gap-2">
          {machines.map((machine) => {
            const scoped = sessions.filter((session) => session.sourceId === machine.sourceId);
            const running = scoped.filter((session) => navigatorStatus(session) === 'running').length;
            const needs = scoped.filter((session) => navigatorStatus(session) === 'needs').length;
            const down = ['stale', 'error'].includes(machine.state);
            return (
              <button
                key={machine.sourceId}
                type="button"
                className={cn(
                  styles.hostChip,
                  down && 'border-[var(--nav-clay)] bg-[var(--nav-clay-bg)] text-[var(--nav-clay)]',
                )}
                onClick={down ? onRetry : undefined}
                disabled={!down || retrying}
                title={down ? `Retry ${machine.label}` : `${machine.label} is connected`}
              >
                <span
                  className={cn(
                    'size-[7px] rounded-full bg-[var(--nav-green-dot)]',
                    down && 'bg-[var(--nav-clay-dot)]',
                  )}
                />
                <strong
                  className={cn('font-semibold text-[var(--nav-ink)]', down && 'text-[var(--nav-clay)]')}
                >
                  {machine.label}
                </strong>
                <span>
                  {down
                    ? `unreachable · ${scoped.length} sessions`
                    : `${running} running · ${needs} need you`}
                </span>
              </button>
            );
          })}
          {diagnostics}
          {actions}
          <Button variant="outline" size="sm" onClick={onSettings}>
            <Settings /> Settings
          </Button>
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
            className="ml-auto border-[var(--nav-clay)] bg-transparent text-[var(--nav-clay)]"
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

function ScopeSummary({ group, sessions }: { group: Group; sessions: Session[] }) {
  const counts = (valueOf: (session: Session) => string) => {
    const values = new Map<string, number>();
    for (const session of sessions) {
      const value = valueOf(session);
      values.set(value, (values.get(value) ?? 0) + 1);
    }
    return [...values];
  };
  const column = (title: string, rows: [string, string | number][]) => (
    <div>
      <span className="block pb-1.5 text-[11px] font-[650] tracking-[.09em] text-[var(--nav-muted)] uppercase">
        {title}
      </span>
      {rows.map(([label, value]) => (
        <p key={label} className="flex justify-between gap-2.5 py-0.5 text-xs text-[var(--nav-muted)]">
          {label} <strong className="font-semibold text-[var(--nav-ink)]">{value}</strong>
        </p>
      ))}
    </div>
  );
  return (
    <div className={styles.scope}>
      <div className="flex flex-wrap items-center gap-2.5 pb-3">
        <strong className="text-sm font-[650]">{group.label}</strong>
        <span className="truncate text-[11.5px] text-[var(--nav-muted)]">
          {group.kind} · {group.basis}
        </span>
      </div>
      <div className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-3.5">
        {column('Total', [
          ['Sessions', sessions.length],
          ['Tokens', formatTokens(totalTokens(sessions))],
        ])}
        {column(
          'Vendor',
          counts((session) => getVendor(session.agent).label),
        )}
        {column(
          'Machine',
          counts((session) => session.sourceLabel),
        )}
        {column('Context', [
          ['Resume as-is', sessions.filter((session) => sessionReadiness(session).key === 'resume').length],
          ['Start fresh', sessions.filter((session) => sessionReadiness(session).key === 'fresh').length],
        ])}
      </div>
    </div>
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
  sortKey: NavigatorSort['key'];
  sort: NavigatorSort;
  className?: string;
  onSort: (key: NavigatorSort['key']) => void;
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
}: {
  session: Session;
  group: Group;
  status: NavigatorStatus;
  selected: boolean;
  compact: boolean;
  onSelect: () => void;
  onToggleGroup: () => void;
}) {
  const readiness = sessionReadiness(session);
  const vendor = getVendor(session.agent);
  const cell = cn(styles.cell, compact && 'py-2');
  const hideWhenCompact = compact && 'hidden';
  return (
    <tr
      className={cn(
        'navigator-row group cursor-pointer border-b border-[var(--nav-line-soft)] hover:bg-[var(--nav-soft)]',
        selected && 'bg-[var(--nav-tint)] [&>td:first-child]:shadow-[inset_3px_0_0_var(--nav-accent)]',
      )}
      onClick={onSelect}
    >
      <td className={cn(cell, 'w-auto')}>
        <button
          type="button"
          className="block w-full truncate text-left text-base leading-[1.3] font-medium hover:text-[var(--nav-accent)]"
          onClick={onSelect}
        >
          {session.name ?? session.firstPrompt ?? 'Untitled session'}
        </button>
        <span
          className={cn(
            'block truncate pt-1 text-[13.5px] leading-[1.4] text-[var(--nav-muted)]',
            hideWhenCompact,
          )}
        >
          {getSessionCardSummary(session)}
        </span>
      </td>
      <td className={cn(cell, 'w-[230px]')}>
        <div className="flex min-w-0 items-center gap-1.5 font-mono text-[var(--nav-muted)]">
          <button
            type="button"
            className="max-w-full truncate text-left font-[inherit] hover:text-[var(--nav-accent)]"
            onClick={(event) => {
              event.stopPropagation();
              onToggleGroup();
            }}
          >
            {group.label}
          </button>
          {session.branch && <code className="min-w-0 truncate text-[inherit]">· {session.branch}</code>}
        </div>
        <div className={cn('flex min-w-0 items-center gap-1 pt-1.5', hideWhenCompact)}>
          <span
            className={cn(
              'max-w-full truncate rounded-full px-1.5 py-0.5 text-[10.5px] leading-[1.55] font-[520]',
              session.agent === 'claude'
                ? 'bg-[#f8ede6] text-[#96552f] dark:bg-[#332318] dark:text-[#e0a483]'
                : session.agent === 'codex'
                  ? 'bg-[#eceffc] text-[#4a5ab8] dark:bg-[#252d47] dark:text-[#a9b6f0]'
                  : 'bg-[var(--nav-soft)] text-[var(--nav-muted)]',
            )}
          >
            {vendor.label}
          </span>
          <span className="max-w-full truncate rounded-full bg-[var(--nav-soft)] px-1.5 py-0.5 text-[10.5px] leading-[1.55] font-medium text-[var(--nav-muted)]">
            {session.sourceLabel}
          </span>
        </div>
      </td>
      <td className={cn(cell, 'w-[205px]')}>
        <span
          className="flex items-center gap-1.5 whitespace-nowrap text-[var(--nav-muted)]"
          title={STATUS_META[status].hint}
        >
          <i className={cn('size-[7px] rounded-full', statusDot(status))} />
          {STATUS_META[status].label}
        </span>
        <span
          className={cn(
            'block truncate pt-0.5 text-xs text-[var(--nav-muted)]',
            readiness.key === 'fresh' && 'font-[550] text-[var(--nav-clay)]',
            hideWhenCompact,
          )}
        >
          {readiness.detail}
        </span>
      </td>
      <td className={cn(cell, 'w-[105px] text-right whitespace-nowrap text-[var(--nav-muted)] tabular-nums')}>
        {formatTimeAgo(session.mtime)}
      </td>
      <td
        className={cn(
          cell,
          'w-[115px] text-right font-[430] whitespace-nowrap text-[var(--nav-muted)] tabular-nums',
        )}
      >
        <UnpricedModelWarning unpriced={session.unpricedModels}>
          {formatEstimatedCost(session.cost)}
        </UnpricedModelWarning>
      </td>
      <td className={cn(cell, 'navigator-action-column w-24 pl-0 text-right whitespace-nowrap')}>
        <Button
          variant="outline"
          size="sm"
          className="navigator-action-button min-w-0 px-2"
          onClick={(event) => {
            event.stopPropagation();
            onSelect();
          }}
        >
          {status === 'running' ? 'Watch' : readiness.label}
        </Button>
      </td>
    </tr>
  );
}

function SessionTable({
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
}: {
  sections: { status: NavigatorStatus; rows: Session[] }[];
  groups: Map<string, Group>;
  selectedSessionKey: string | null;
  compact: boolean;
  sort: NavigatorSort;
  openSections: Record<NavigatorStatus, boolean>;
  sectionLimits: Record<string, number>;
  onSort: (key: NavigatorSort['key']) => void;
  onToggleSection: (status: NavigatorStatus) => void;
  onShowMore: (status: NavigatorStatus, limit: number) => void;
  onSelectSession: (session: Session) => void;
  onToggleGroup: (id: string) => void;
}) {
  return (
    <table className="w-full min-w-[960px] table-fixed border-collapse">
      <thead>
        <tr>
          <SortHeader label="Session" sortKey="title" sort={sort} onSort={onSort} />
          <th className={cn(styles.head, 'w-[230px] px-5 py-3')}>Where</th>
          <th className={cn(styles.head, 'w-[205px] px-5 py-3')}>Status / context</th>
          <SortHeader
            label="Updated"
            sortKey="recent"
            sort={sort}
            onSort={onSort}
            className="w-[105px] text-right [&_button]:justify-end"
          />
          <SortHeader
            label="Est. cost"
            sortKey="cost"
            sort={sort}
            onSort={onSort}
            className="w-[115px] text-right [&_button]:justify-end"
          />
          <th className={cn(styles.head, 'navigator-action-column w-24 px-5 py-3 text-right')}>
            <span className="sr-only">Actions</span>
          </th>
        </tr>
      </thead>
      {sections.map(({ status, rows }) => {
        const open = openSections[status];
        const limit = sectionLimits[status] ?? 5;
        const shown = open ? rows.slice(0, limit) : [];
        return (
          <tbody key={status}>
            <tr>
              <th
                colSpan={6}
                className="border-y border-[var(--nav-line-soft)] bg-[var(--nav-soft)] text-left"
              >
                <button
                  type="button"
                  className="flex min-h-8 w-full items-center gap-2 px-2.5 py-1.5 text-xs font-[650]"
                  aria-expanded={open}
                  onClick={() => onToggleSection(status)}
                >
                  <ChevronRight className={cn('size-4 transition-transform', open && 'rotate-90')} />
                  {STATUS_META[status].label}{' '}
                  <span className="font-medium text-[var(--nav-muted)]">{rows.length}</span>
                  <span className="ml-auto text-[11.5px] font-medium text-[var(--nav-muted)]">
                    {formatTokens(totalTokens(rows))} tokens ·{' '}
                    {formatEstimatedCost(sumKnown(rows.map((session) => session.cost)))}
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
                />
              );
            })}
            {open && rows.length > shown.length && (
              <tr>
                <td colSpan={6} className="p-0">
                  <button
                    type="button"
                    className="w-full p-2 text-xs font-semibold text-[var(--nav-accent)] hover:bg-[var(--nav-soft)]"
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

export function WorkspaceNavigator({
  sessions,
  machines,
  range,
  onRangeChange,
  selectedSessionKey,
  onSelectSession,
  diagnostics,
  onSettings,
  onRetry,
  retrying,
  isLoading,
  loadError,
  emptyContent,
  banner,
  headerActions,
  inspectorOpen = false,
}: {
  sessions: Session[];
  machines: MachineFact[];
  range: NavigatorRange;
  onRangeChange: (range: NavigatorRange) => void;
  selectedSessionKey: string | null;
  onSelectSession: (session: Session) => void;
  diagnostics: ReactNode;
  onSettings: () => void;
  onRetry: () => void;
  retrying: boolean;
  isLoading: boolean;
  loadError: string | null;
  emptyContent?: ReactNode;
  banner?: ReactNode;
  headerActions?: ReactNode;
  inspectorOpen?: boolean;
}) {
  const [preferences, setPreferences] = useState(loadNavigatorPreferences);
  const [openSections, setOpenSections] = useState<Record<NavigatorStatus, boolean>>({
    needs: true,
    running: true,
    idle: true,
    archived: false,
  });
  const [sectionLimits, setSectionLimits] = useState<Record<string, number>>({});
  const [sideOpen, setSideOpen] = useState<Record<FacetKey, boolean>>({
    status: true,
    machine: true,
    agent: true,
    group: true,
  });
  const patchPreferences = (patch: Partial<NavigatorPreferences>) =>
    setPreferences((current) => ({ ...current, ...patch }));

  useEffect(() => saveNavigatorPreferences({ ...preferences, range }), [preferences, range]);

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
  const agents = useMemo(() => [...new Set(sessions.map((session) => session.agent))], [sessions]);
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
  const toggleStatus = (status: NavigatorStatus) =>
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
  const clearFacets = () =>
    patchPreferences({ statusFilters: [], groupFilters: [], machineFilter: null, agentFilter: null });
  const setSortKey = (key: NavigatorSort['key']) =>
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
        count:
          status === 'archived' ? 0 : countWith((session) => navigatorStatus(session) === status, 'status'),
        selected: preferences.statusFilters.includes(status),
        onClick: () => status !== 'archived' && toggleStatus(status),
      })),
    },
    {
      id: 'machine',
      label: 'Connections',
      options: machines.map((machine) => ({
        id: machine.sourceId,
        label: machine.label,
        icon: <Monitor />,
        count: countWith((session) => session.sourceId === machine.sourceId, 'machine'),
        selected: preferences.machineFilter === machine.sourceId,
        onClick: () =>
          patchPreferences({
            machineFilter: preferences.machineFilter === machine.sourceId ? null : machine.sourceId,
          }),
      })),
    },
    {
      id: 'agent',
      label: 'Agent',
      options: agents.map((agent) => ({
        id: agent,
        label: getVendor(agent).label,
        icon: <Activity />,
        count: countWith((session) => session.agent === agent, 'agent'),
        selected: preferences.agentFilter === agent,
        onClick: () => patchPreferences({ agentFilter: preferences.agentFilter === agent ? null : agent }),
      })),
    },
  ];
  const groupOptions = (kind: Group['kind']): FacetOption[] =>
    groups
      .filter((group) => group.kind === kind)
      .slice(0, 8)
      .map((group) => ({
        id: group.id,
        label: group.label,
        icon: <Folder />,
        count: countWith((session) => sessionGroups.get(sessionKey(session))?.id === group.id, 'group'),
        selected: preferences.groupFilters.includes(group.id),
        onClick: () => toggleGroup(group.id),
      }));
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
    ...(preferences.machineFilter == null
      ? []
      : [
          {
            kind: 'Machine',
            value: preferences.machineFilter,
            label:
              machines.find((machine) => machine.sourceId === preferences.machineFilter)?.label ??
              preferences.machineFilter,
            remove: () => patchPreferences({ machineFilter: null }),
          },
        ]),
    ...(preferences.agentFilter == null
      ? []
      : [
          {
            kind: 'Agent',
            value: preferences.agentFilter,
            label: getVendor(preferences.agentFilter).label,
            remove: () => patchPreferences({ agentFilter: null }),
          },
        ]),
  ];
  const sections = STATUS_ORDER.map((status) => ({
    status,
    rows: visibleSessions.filter((session) => navigatorStatus(session) === status),
  })).filter(({ rows }) => rows.length > 0);
  const selectedGroup =
    preferences.groupFilters.length === 1
      ? groups.find((group) => group.id === preferences.groupFilters[0])
      : undefined;

  return (
    <div className={cn(styles.shell, inspectorOpen && 'inspector-open')}>
      <WorkspaceHeader
        sessions={sessionsInRange}
        machines={machines}
        diagnostics={diagnostics}
        onSettings={onSettings}
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
              onToggle={() => setSideOpen((state) => ({ ...state, [section.id]: !state[section.id] }))}
            />
          ))}
          <div className="pb-3.5">
            <button
              className={styles.sideHeading}
              aria-expanded={sideOpen.group}
              aria-controls="navigator-group"
              onClick={() => setSideOpen((state) => ({ ...state, group: !state.group }))}
            >
              <ChevronRight
                className={cn('size-[13px] transition-transform', sideOpen.group && 'rotate-90')}
              />
              Detected groups
            </button>
            {sideOpen.group && (
              <div id="navigator-group">
                {GROUP_KINDS.map((kind) => {
                  const options = groupOptions(kind);
                  return (
                    options.length > 0 && (
                      <div key={kind}>
                        <div className="px-2.5 pt-2 pb-1 text-[11px] text-[var(--nav-muted)]">
                          {GROUP_LABELS[kind]}
                        </div>
                        <FacetRows options={options} />
                      </div>
                    )
                  );
                })}
              </div>
            )}
          </div>
          <div className="border-t border-[var(--nav-line-soft)] px-2.5 pt-3.5 text-[11px] leading-[1.7] text-[var(--nav-muted)]">
            {machines.map((machine) => machine.label).join(' + ') || 'Local sessions'}
            <br />
            {sessionsInRange.length} sessions · {formatTokens(totalTokens(sessionsInRange))} tokens ·{' '}
            {formatEstimatedCost(sumKnown(sessionsInRange.map((session) => session.cost)))}
          </div>
        </aside>

        <main className={styles.main}>
          <div className={styles.search}>
            <Search />
            <input
              type="search"
              value={preferences.query}
              onChange={(event) => patchPreferences({ query: event.target.value })}
              placeholder="Find a session, outcome, or file…  try repo:coslash"
              aria-label="Search titles, outcomes, files and context"
              className="min-w-0 flex-1 bg-transparent text-[13px] outline-none [&::-webkit-search-cancel-button]:hidden"
            />
            {preferences.query && (
              <button
                type="button"
                className="grid size-7 place-items-center rounded-[7px] hover:bg-[var(--nav-soft)] [&>svg]:size-4"
                onClick={() => patchPreferences({ query: '' })}
                aria-label="Clear search"
              >
                <X />
              </button>
            )}
            <kbd className="rounded-[5px] border border-[var(--nav-line)] px-1.5 py-1 text-[11px] leading-none text-[var(--nav-muted)]">
              ⌘ K
            </kbd>
          </div>
          <details className="pt-2 text-[11.5px] text-[var(--nav-muted)]">
            <summary className="w-fit cursor-pointer">Search recorded context</summary>
            <p className="max-w-[640px] pt-1.5 leading-[1.6]">
              Titles, goals, outcomes, filenames, repository, branch, folder, agent and machine.
            </p>
          </details>

          {activeChips.length > 0 && (
            <div className={styles.chipbar}>
              <span className="pr-0.5 text-[11.5px] text-[var(--nav-muted)]">Scope</span>
              {activeChips.map((chip) => (
                <span className={styles.chip} key={`${chip.kind}:${chip.value}`}>
                  <span className="font-normal opacity-70">{chip.kind}:</span> {chip.label}
                  <button
                    type="button"
                    className="grid size-[18px] place-items-center rounded-full hover:bg-[var(--nav-tint-line)] [&>svg]:size-[11px]"
                    aria-label={`Remove ${chip.label}`}
                    onClick={chip.remove}
                  >
                    <X />
                  </button>
                </span>
              ))}
              <button
                type="button"
                className="ml-auto px-0.5 py-1.5 text-xs font-[550] text-[var(--nav-accent)]"
                onClick={clearFacets}
              >
                Clear all
              </button>
            </div>
          )}

          <Rollup sessions={visibleSessions} />
          <div className="flex flex-wrap items-center gap-2 py-3">
            <div className={styles.segmented} aria-label="Time range">
              {RANGE_OPTIONS.map((option) => (
                <button
                  key={option.value}
                  type="button"
                  className={cn(
                    'min-h-7 rounded-md px-2.5 py-1 text-xs text-[var(--nav-muted)]',
                    range === option.value &&
                      'bg-[var(--nav-tint)] font-semibold text-[var(--nav-accent-ink)]',
                  )}
                  onClick={() => onRangeChange(option.value)}
                >
                  {option.label}
                </button>
              ))}
            </div>
            <div className={styles.segmented} aria-label="Row density">
              {(['comfortable', 'compact'] as const).map((value) => (
                <button
                  key={value}
                  type="button"
                  className={cn(
                    'min-h-7 rounded-md px-2.5 py-1 text-xs text-[var(--nav-muted)]',
                    preferences.density === value &&
                      'bg-[var(--nav-tint)] font-semibold text-[var(--nav-accent-ink)]',
                  )}
                  onClick={() => patchPreferences({ density: value })}
                >
                  {value === 'comfortable' ? 'Comfortable' : 'Compact'}
                </button>
              ))}
            </div>
          </div>

          {selectedGroup && <ScopeSummary group={selectedGroup} sessions={visibleSessions} />}
          <div className={styles.tableWrap}>
            <LoadingSpinner isLoading={isLoading && sessions.length === 0}>
              {loadError ? (
                <div className={styles.empty} role="alert">
                  <AlertTriangle className="size-6 text-[var(--nav-muted)]" />
                  <h3 className="text-[15px] font-[650]">Sessions could not be loaded</h3>
                  <p className="max-w-[430px] text-[12.5px] leading-[1.6] text-[var(--nav-muted)]">
                    {loadError}
                  </p>
                  <Button variant="outline" onClick={onRetry}>
                    Try again
                  </Button>
                </div>
              ) : visibleSessions.length === 0 ? (
                (emptyContent ?? (
                  <div className={styles.empty}>
                    <Search className="size-6 text-[var(--nav-muted)]" />
                    <h3 className="text-[15px] font-[650]">Nothing in this scope</h3>
                    <p className="max-w-[430px] text-[12.5px] leading-[1.6] text-[var(--nav-muted)]">
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
              ) : (
                <SessionTable
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
                />
              )}
            </LoadingSpinner>
          </div>
        </main>
      </div>
    </div>
  );
}
