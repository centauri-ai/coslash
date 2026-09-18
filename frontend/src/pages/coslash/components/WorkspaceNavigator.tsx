import { useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ChevronRight,
  Folder,
  Monitor,
  Plus,
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
  type NavigatorDensity,
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

type Group = {
  id: string;
  label: string;
  kind: 'Repository' | 'Folder parent' | 'No location';
  basis: string;
};

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

function navigatorStatus(session: Session): NavigatorStatus {
  const status = boardStatusKey(session);
  if (status === 'waiting') return 'needs';
  if (status === 'busy') return 'running';
  return 'idle';
}

function rangeStart(range: NavigatorRange): number | null {
  const now = new Date();
  if (range === 'all') return null;
  if (range === 'today') {
    now.setHours(0, 0, 0, 0);
    return now.getTime();
  }
  if (range === 'this-week') {
    const daysSinceMonday = (now.getDay() + 6) % 7;
    now.setHours(0, 0, 0, 0);
    now.setDate(now.getDate() - daysSinceMonday);
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
    const leaf = parts.at(-2) ?? parts.at(-1) ?? 'Folder';
    return {
      id: `folder:${session.sourceId}:${parent}`,
      label: `${session.sourceLabel} · ${leaf}`,
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
  if (values.every((value) => value == null)) return null;
  return values.reduce<number>((total, value) => total + (value ?? 0), 0);
}

function searchFields(session: Session, group: Group): string[] {
  return [
    session.name,
    session.summary,
    session.synthesis?.outcome,
    session.declaredGoal,
    session.firstPrompt,
    session.repo,
    session.branch,
    session.cwd,
    group.label,
    getVendor(session.agent).label,
    session.sourceLabel,
    ...session.fileEdits.map((file) => file.path),
  ].filter((value): value is string => Boolean(value?.trim()));
}

function matchesSearch(session: Session, group: Group, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const haystack = searchFields(session, group).join('\n').toLowerCase();
  return words.every((word) => {
    const prefix = word.match(/^(repo|group|machine|agent|status):(.+)$/);
    if (!prefix) return haystack.includes(word);
    const [, kind, value] = prefix;
    if (kind === 'repo' || kind === 'group') {
      return `${session.repo ?? ''} ${group.label}`.toLowerCase().includes(value);
    }
    if (kind === 'machine') return session.sourceLabel.toLowerCase().includes(value);
    if (kind === 'agent') return getVendor(session.agent).label.toLowerCase().includes(value);
    return STATUS_META[navigatorStatus(session)].label.toLowerCase().includes(value);
  });
}

function Rollup({ sessions }: { sessions: Session[] }) {
  const cost = sumKnown(sessions.map((session) => session.cost));
  const unpriced = sessions.filter((session) => session.cost == null || session.unpricedModels.length > 0);
  return (
    <div className="navigator-rollup">
      <strong>
        {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}
      </strong>
      <span>active in this scope</span>
      <span>·</span>
      <span>{formatTokens(totalTokens(sessions))} tokens</span>
      <span>·</span>
      <UnpricedModelWarning unpriced={unpriced.flatMap((session) => session.unpricedModels)}>
        {formatEstimatedCost(cost)}
      </UnpricedModelWarning>
      {unpriced.length > 0 && (
        <>
          <span>·</span>
          <span>{unpriced.length} not priced</span>
        </>
      )}
      <span className="navigator-price-note">at list API prices</span>
    </div>
  );
}

function SectionHeading({
  id,
  children,
  open,
  onToggle,
}: {
  id: string;
  children: ReactNode;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <button className="navigator-side-heading" aria-expanded={open} aria-controls={id} onClick={onToggle}>
      <ChevronRight className="navigator-chevron" />
      {children}
    </button>
  );
}

function FacetRow({
  label,
  count,
  selected,
  icon,
  status,
  onClick,
}: {
  label: string;
  count: number;
  selected: boolean;
  icon?: ReactNode;
  status?: NavigatorStatus;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className={cn('navigator-facet', { selected, empty: count === 0 })}
      aria-pressed={selected}
      onClick={onClick}
    >
      {status ? <span className={`navigator-dot ${status}`} /> : icon}
      <span className="navigator-facet-label">{label}</span>
      <span className="navigator-facet-count">{count}</span>
    </button>
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
  const problemMachines = machines.filter((machine) => ['stale', 'error'].includes(machine.state));
  return (
    <>
      <div className="navigator-header">
        <div className="navigator-brand-wrap">
          <span aria-label="coSlash">
            <img src="/brand/coslash-logo.svg" alt="" className="navigator-brand dark:hidden" />
            <img src="/brand/coslash-logo-reverse.svg" alt="" className="navigator-brand hidden dark:block" />
          </span>
          <span className="navigator-tagline">Run more agents. Lose less context.</span>
        </div>
        <div className="navigator-header-actions">
          {machines.map((machine) => {
            const scoped = sessions.filter((session) => session.sourceId === machine.sourceId);
            const running = scoped.filter((session) => navigatorStatus(session) === 'running').length;
            const needs = scoped.filter((session) => navigatorStatus(session) === 'needs').length;
            const down = ['stale', 'error'].includes(machine.state);
            return (
              <button
                key={machine.sourceId}
                type="button"
                className={cn('navigator-host-chip', { down })}
                onClick={down ? onRetry : undefined}
                disabled={!down || retrying}
                title={down ? `Retry ${machine.label}` : `${machine.label} is connected`}
              >
                <span className="navigator-host-dot" />
                <strong>{machine.label}</strong>
                <span className="navigator-host-detail">
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
            <Settings /> <span className="navigator-button-text">Settings</span>
          </Button>
        </div>
      </div>
      {problemMachines.length > 0 && (
        <div className="navigator-banner" role="alert">
          <AlertTriangle />
          <span>
            <strong>{problemMachines.map((machine) => machine.label).join(', ')}</strong> unreachable — remote
            sessions show their last recorded context.
          </span>
          <Button variant="outline" size="sm" onClick={onRetry} disabled={retrying}>
            <RefreshCw className={cn({ 'animate-spin': retrying })} /> Retry
          </Button>
        </div>
      )}
    </>
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
  const [initialPreferences] = useState(loadNavigatorPreferences);
  const [query, setQuery] = useState(initialPreferences.query);
  const [statusFilters, setStatusFilters] = useState<NavigatorStatus[]>(initialPreferences.statusFilters);
  const [groupFilters, setGroupFilters] = useState<string[]>(initialPreferences.groupFilters);
  const [machineFilter, setMachineFilter] = useState<string | null>(initialPreferences.machineFilter);
  const [agentFilter, setAgentFilter] = useState<string | null>(initialPreferences.agentFilter);
  const [density, setDensity] = useState<NavigatorDensity>(initialPreferences.density);
  const [sort, setSort] = useState<NavigatorSort>(initialPreferences.sort);
  const [openSections, setOpenSections] = useState<Record<NavigatorStatus, boolean>>({
    needs: true,
    running: true,
    idle: true,
    archived: false,
  });
  const [sectionLimits, setSectionLimits] = useState<Record<string, number>>({});
  const [sideOpen, setSideOpen] = useState<Record<string, boolean>>({
    status: true,
    machine: true,
    agent: true,
    group: true,
  });

  useEffect(() => {
    saveNavigatorPreferences({
      query,
      range,
      statusFilters,
      groupFilters,
      machineFilter,
      agentFilter,
      density,
      sort,
    });
  }, [agentFilter, density, groupFilters, machineFilter, query, range, sort, statusFilters]);

  const sessionGroups = useMemo(
    () => new Map(sessions.map((session) => [sessionKey(session), detectedGroup(session)])),
    [sessions],
  );
  const groups = useMemo(() => {
    const values = new Map<string, Group>();
    for (const group of sessionGroups.values()) values.set(group.id, group);
    return [...values.values()].sort((a, b) => a.label.localeCompare(b.label));
  }, [sessionGroups]);
  const agents = useMemo(() => [...new Set(sessions.map((session) => session.agent))], [sessions]);
  const start = rangeStart(range);
  const sessionsInRange = useMemo(
    () => sessions.filter((session) => start == null || session.status != null || session.mtime >= start),
    [sessions, start],
  );

  const visibleSessions = useMemo(() => {
    const filtered = sessionsInRange.filter((session) => {
      const group = sessionGroups.get(sessionKey(session))!;
      if (statusFilters.length > 0 && !statusFilters.includes(navigatorStatus(session))) return false;
      if (groupFilters.length > 0 && !groupFilters.includes(group.id)) return false;
      if (machineFilter != null && session.sourceId !== machineFilter) return false;
      if (agentFilter != null && session.agent !== agentFilter) return false;
      return matchesSearch(session, group, query);
    });
    return [...filtered].sort((a, b) => {
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
  }, [agentFilter, groupFilters, machineFilter, query, sessionGroups, sessionsInRange, sort, statusFilters]);

  const activeChips = [
    ...statusFilters.map((value) => ({ kind: 'Status', value, label: STATUS_META[value].label })),
    ...groupFilters.map((value) => ({
      kind: 'Group',
      value,
      label: groups.find((g) => g.id === value)?.label ?? value,
    })),
    ...(machineFilter == null
      ? []
      : [
          {
            kind: 'Machine',
            value: machineFilter,
            label: machines.find((m) => m.sourceId === machineFilter)?.label ?? machineFilter,
          },
        ]),
    ...(agentFilter == null
      ? []
      : [{ kind: 'Agent', value: agentFilter, label: getVendor(agentFilter).label }]),
  ];

  const countWith = (
    predicate: (session: Session) => boolean,
    skip: 'status' | 'group' | 'machine' | 'agent',
  ) =>
    sessionsInRange.filter((session) => {
      const group = sessionGroups.get(sessionKey(session))!;
      if (skip !== 'status' && statusFilters.length > 0 && !statusFilters.includes(navigatorStatus(session)))
        return false;
      if (skip !== 'group' && groupFilters.length > 0 && !groupFilters.includes(group.id)) return false;
      if (skip !== 'machine' && machineFilter != null && session.sourceId !== machineFilter) return false;
      if (skip !== 'agent' && agentFilter != null && session.agent !== agentFilter) return false;
      return matchesSearch(session, group, query) && predicate(session);
    }).length;

  const clearFacets = () => {
    setStatusFilters([]);
    setGroupFilters([]);
    setMachineFilter(null);
    setAgentFilter(null);
  };
  const toggleMulti = (value: string, values: string[], setValues: (next: string[]) => void) =>
    setValues(values.includes(value) ? values.filter((item) => item !== value) : [...values, value]);
  const setSortKey = (key: NavigatorSort['key']) =>
    setSort((current) =>
      current.key === key
        ? { key, dir: current.dir === 'asc' ? 'desc' : 'asc' }
        : { key, dir: key === 'title' ? 'asc' : 'desc' },
    );

  const sections = STATUS_ORDER.map((status) => ({
    status,
    rows: visibleSessions.filter((session) => navigatorStatus(session) === status),
  })).filter(({ rows }) => rows.length > 0);

  return (
    <div className={cn('navigator-shell', { 'inspector-open': inspectorOpen })}>
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
      <div className="navigator-layout">
        <div className="navigator-sidebar" aria-label="Filters">
          <div className="navigator-side-group">
            <SectionHeading
              id="navigator-status"
              open={sideOpen.status}
              onToggle={() => setSideOpen((state) => ({ ...state, status: !state.status }))}
            >
              Status
            </SectionHeading>
            {sideOpen.status && (
              <div id="navigator-status">
                {STATUS_ORDER.map((status) => (
                  <FacetRow
                    key={status}
                    status={status}
                    label={STATUS_META[status].label}
                    count={
                      status === 'archived'
                        ? 0
                        : countWith((session) => navigatorStatus(session) === status, 'status')
                    }
                    selected={statusFilters.includes(status)}
                    onClick={() =>
                      status !== 'archived' &&
                      toggleMulti(status, statusFilters, (next) =>
                        setStatusFilters(next as NavigatorStatus[]),
                      )
                    }
                  />
                ))}
              </div>
            )}
          </div>
          <div className="navigator-side-group">
            <SectionHeading
              id="navigator-machines"
              open={sideOpen.machine}
              onToggle={() => setSideOpen((state) => ({ ...state, machine: !state.machine }))}
            >
              Connections
            </SectionHeading>
            {sideOpen.machine && (
              <div id="navigator-machines">
                {machines.map((machine) => (
                  <FacetRow
                    key={machine.sourceId}
                    label={machine.label}
                    count={countWith((session) => session.sourceId === machine.sourceId, 'machine')}
                    selected={machineFilter === machine.sourceId}
                    icon={<Monitor />}
                    onClick={() =>
                      setMachineFilter((current) => (current === machine.sourceId ? null : machine.sourceId))
                    }
                  />
                ))}
              </div>
            )}
          </div>
          <div className="navigator-side-group">
            <SectionHeading
              id="navigator-agents"
              open={sideOpen.agent}
              onToggle={() => setSideOpen((state) => ({ ...state, agent: !state.agent }))}
            >
              Agent
            </SectionHeading>
            {sideOpen.agent && (
              <div id="navigator-agents">
                {agents.map((agent) => (
                  <FacetRow
                    key={agent}
                    label={getVendor(agent).label}
                    count={countWith((session) => session.agent === agent, 'agent')}
                    selected={agentFilter === agent}
                    icon={<Activity />}
                    onClick={() => setAgentFilter((current) => (current === agent ? null : agent))}
                  />
                ))}
              </div>
            )}
          </div>
          <div className="navigator-side-group">
            <SectionHeading
              id="navigator-groups"
              open={sideOpen.group}
              onToggle={() => setSideOpen((state) => ({ ...state, group: !state.group }))}
            >
              Detected groups
            </SectionHeading>
            {sideOpen.group && (
              <div id="navigator-groups">
                {(['Repository', 'Folder parent', 'No location'] as const).map((kind) => {
                  const values = groups.filter((group) => group.kind === kind);
                  if (values.length === 0) return null;
                  return (
                    <div key={kind}>
                      <div className="navigator-kind-label">
                        {kind === 'Repository'
                          ? 'Repositories'
                          : kind === 'Folder parent'
                            ? 'Other folders'
                            : 'No location'}
                      </div>
                      {values.slice(0, 8).map((group) => (
                        <FacetRow
                          key={group.id}
                          label={group.label}
                          count={countWith(
                            (session) => sessionGroups.get(sessionKey(session))?.id === group.id,
                            'group',
                          )}
                          selected={groupFilters.includes(group.id)}
                          icon={<Folder />}
                          onClick={() => toggleMulti(group.id, groupFilters, setGroupFilters)}
                        />
                      ))}
                    </div>
                  );
                })}
              </div>
            )}
          </div>
          <div className="navigator-side-foot">
            {machines.map((machine) => machine.label).join(' + ') || 'Local sessions'}
            <br />
            {sessionsInRange.length} sessions · {formatTokens(totalTokens(sessionsInRange))} tokens ·{' '}
            {formatEstimatedCost(sumKnown(sessionsInRange.map((session) => session.cost)))}
          </div>
        </div>

        <div className="navigator-main">
          <div className="navigator-search-wrap">
            <div className="navigator-search">
              <Search />
              <input
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Find a session, outcome, or file…  try repo:coslash"
                aria-label="Search titles, outcomes, files and context"
              />
              {query && (
                <button
                  type="button"
                  className="navigator-icon-button"
                  onClick={() => setQuery('')}
                  aria-label="Clear search"
                >
                  <X />
                </button>
              )}
              <kbd>⌘ K</kbd>
            </div>
          </div>
          <details className="navigator-coverage">
            <summary>Search recorded context</summary>
            <p>Titles, goals, outcomes, filenames, repository, branch, folder, agent and machine.</p>
          </details>

          {activeChips.length > 0 && (
            <div className="navigator-chipbar">
              <span className="navigator-chip-lead">Scope</span>
              {activeChips.map((chip) => (
                <span className="navigator-chip" key={`${chip.kind}:${chip.value}`}>
                  <span>{chip.kind}:</span> {chip.label}
                  <button
                    type="button"
                    aria-label={`Remove ${chip.label}`}
                    onClick={() => {
                      if (chip.kind === 'Status')
                        setStatusFilters((values) => values.filter((value) => value !== chip.value));
                      if (chip.kind === 'Group')
                        setGroupFilters((values) => values.filter((value) => value !== chip.value));
                      if (chip.kind === 'Machine') setMachineFilter(null);
                      if (chip.kind === 'Agent') setAgentFilter(null);
                    }}
                  >
                    <X />
                  </button>
                </span>
              ))}
              <button type="button" className="navigator-clear" onClick={clearFacets}>
                Clear all
              </button>
            </div>
          )}

          <Rollup sessions={visibleSessions} />
          <div className="navigator-toolbar">
            <div className="navigator-segmented" aria-label="Time range">
              {RANGE_OPTIONS.map((option) => (
                <button
                  key={option.value}
                  type="button"
                  className={cn({ selected: range === option.value })}
                  onClick={() => onRangeChange(option.value)}
                >
                  {option.label}
                </button>
              ))}
            </div>
            <div className="navigator-segmented navigator-density" aria-label="Row density">
              {(['comfortable', 'compact'] as const).map((value) => (
                <button
                  key={value}
                  type="button"
                  className={cn({ selected: density === value })}
                  onClick={() => setDensity(value)}
                >
                  {value === 'comfortable' ? 'Comfortable' : 'Compact'}
                </button>
              ))}
            </div>
            <details className="navigator-mobile-filter">
              <summary>
                <Plus /> Add filter
              </summary>
              <div className="navigator-mobile-menu">
                <strong>Status</strong>
                {STATUS_ORDER.filter((status) => status !== 'archived').map((status) => (
                  <FacetRow
                    key={status}
                    status={status}
                    label={STATUS_META[status].label}
                    count={countWith((session) => navigatorStatus(session) === status, 'status')}
                    selected={statusFilters.includes(status)}
                    onClick={() =>
                      toggleMulti(status, statusFilters, (next) =>
                        setStatusFilters(next as NavigatorStatus[]),
                      )
                    }
                  />
                ))}
                <strong>Connections</strong>
                {machines.map((machine) => (
                  <FacetRow
                    key={machine.sourceId}
                    label={machine.label}
                    count={countWith((session) => session.sourceId === machine.sourceId, 'machine')}
                    selected={machineFilter === machine.sourceId}
                    icon={<Monitor />}
                    onClick={() =>
                      setMachineFilter((current) => (current === machine.sourceId ? null : machine.sourceId))
                    }
                  />
                ))}
                <strong>Agent</strong>
                {agents.map((agent) => (
                  <FacetRow
                    key={agent}
                    label={getVendor(agent).label}
                    count={countWith((session) => session.agent === agent, 'agent')}
                    selected={agentFilter === agent}
                    icon={<Activity />}
                    onClick={() => setAgentFilter((current) => (current === agent ? null : agent))}
                  />
                ))}
              </div>
            </details>
          </div>

          {groupFilters.length === 1 &&
            (() => {
              const group = groups.find((item) => item.id === groupFilters[0]);
              if (!group) return null;
              const vendors = new Map<string, number>();
              const sources = new Map<string, number>();
              for (const session of visibleSessions) {
                const vendor = getVendor(session.agent).label;
                vendors.set(vendor, (vendors.get(vendor) ?? 0) + 1);
                sources.set(session.sourceLabel, (sources.get(session.sourceLabel) ?? 0) + 1);
              }
              return (
                <div className="navigator-scope">
                  <div className="navigator-scope-head">
                    <strong>{group.label}</strong>
                    <span>
                      {group.kind} · {group.basis}
                    </span>
                  </div>
                  <div className="navigator-scope-grid">
                    <div>
                      <span>Total</span>
                      <p>
                        Sessions <strong>{visibleSessions.length}</strong>
                      </p>
                      <p>
                        Tokens <strong>{formatTokens(totalTokens(visibleSessions))}</strong>
                      </p>
                    </div>
                    <div>
                      <span>Vendor</span>
                      {[...vendors].map(([label, count]) => (
                        <p key={label}>
                          {label} <strong>{count}</strong>
                        </p>
                      ))}
                    </div>
                    <div>
                      <span>Machine</span>
                      {[...sources].map(([label, count]) => (
                        <p key={label}>
                          {label} <strong>{count}</strong>
                        </p>
                      ))}
                    </div>
                    <div>
                      <span>Context</span>
                      <p>
                        Resume as-is{' '}
                        <strong>
                          {
                            visibleSessions.filter((session) => sessionReadiness(session).key === 'resume')
                              .length
                          }
                        </strong>
                      </p>
                      <p>
                        Start fresh{' '}
                        <strong>
                          {
                            visibleSessions.filter((session) => sessionReadiness(session).key === 'fresh')
                              .length
                          }
                        </strong>
                      </p>
                    </div>
                  </div>
                </div>
              );
            })()}

          <div className="navigator-table-wrap">
            <LoadingSpinner isLoading={isLoading && sessions.length === 0}>
              {loadError ? (
                <div className="navigator-empty" role="alert">
                  <AlertTriangle />
                  <h3>Sessions could not be loaded</h3>
                  <p>{loadError}</p>
                  <Button variant="outline" onClick={onRetry}>
                    Try again
                  </Button>
                </div>
              ) : visibleSessions.length === 0 ? (
                (emptyContent ?? (
                  <div className="navigator-empty">
                    <Search />
                    <h3>Nothing in this scope</h3>
                    <p>Nothing matched the recorded titles, goals, outcomes, files or context.</p>
                    <div>
                      <Button
                        variant="outline"
                        onClick={() => {
                          setQuery('');
                          clearFacets();
                          onRangeChange('all');
                        }}
                      >
                        Clear everything
                      </Button>
                    </div>
                  </div>
                ))
              ) : (
                <table className={cn('navigator-table', { compact: density === 'compact' })}>
                  <thead>
                    <tr>
                      <th className="navigator-session-column">
                        <button type="button" onClick={() => setSortKey('title')}>
                          Session {sort.key === 'title' && (sort.dir === 'asc' ? <ArrowUp /> : <ArrowDown />)}
                        </button>
                      </th>
                      <th className="navigator-where-column">Where</th>
                      <th className="navigator-state-column">Status / context</th>
                      <th className="navigator-time-column">
                        <button type="button" onClick={() => setSortKey('recent')}>
                          Updated{' '}
                          {sort.key === 'recent' && (sort.dir === 'asc' ? <ArrowUp /> : <ArrowDown />)}
                        </button>
                      </th>
                      <th className="navigator-cost-column">
                        <button type="button" onClick={() => setSortKey('cost')}>
                          Est. cost{' '}
                          {sort.key === 'cost' && (sort.dir === 'asc' ? <ArrowUp /> : <ArrowDown />)}
                        </button>
                      </th>
                      <th className="navigator-action-column">
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
                        <tr className="navigator-section-row">
                          <th colSpan={6}>
                            <button
                              type="button"
                              aria-expanded={open}
                              onClick={() =>
                                setOpenSections((state) => ({ ...state, [status]: !state[status] }))
                              }
                            >
                              <ChevronRight /> {STATUS_META[status].label} <span>{rows.length}</span>
                              <span className="navigator-section-rollup">
                                {formatTokens(totalTokens(rows))} tokens ·{' '}
                                {formatEstimatedCost(sumKnown(rows.map((session) => session.cost)))}
                              </span>
                            </button>
                          </th>
                        </tr>
                        {shown.map((session) => {
                          const group = sessionGroups.get(sessionKey(session))!;
                          const readiness = sessionReadiness(session);
                          const vendor = getVendor(session.agent);
                          const statusMeta = STATUS_META[status];
                          return (
                            <tr
                              key={sessionKey(session)}
                              className={cn('navigator-row', {
                                selected: sessionKey(session) === selectedSessionKey,
                              })}
                              onClick={() => onSelectSession(session)}
                            >
                              <td className="navigator-session-column">
                                <button
                                  type="button"
                                  className="navigator-session-title"
                                  onClick={() => onSelectSession(session)}
                                >
                                  {session.name ?? session.firstPrompt ?? 'Untitled session'}
                                </button>
                                <span className="navigator-evidence">{getSessionCardSummary(session)}</span>
                                <span className="navigator-mobile-meta">
                                  <span className={`navigator-state ${status}`}>
                                    <i />
                                    {statusMeta.label}
                                  </span>
                                  <span>{readiness.detail}</span>
                                  <span>{group.label}</span>
                                  <span>
                                    {formatTimeAgo(session.mtime)} · {formatEstimatedCost(session.cost)}
                                  </span>
                                </span>
                              </td>
                              <td className="navigator-where-column">
                                <div className="navigator-location">
                                  <button
                                    type="button"
                                    onClick={(event) => {
                                      event.stopPropagation();
                                      toggleMulti(group.id, groupFilters, setGroupFilters);
                                    }}
                                  >
                                    {group.label}
                                  </button>
                                  {session.branch && <code>· {session.branch}</code>}
                                </div>
                                <div className="navigator-pills">
                                  <span
                                    className={
                                      session.agent === 'claude'
                                        ? 'claude'
                                        : session.agent === 'codex'
                                          ? 'codex'
                                          : 'host'
                                    }
                                  >
                                    {vendor.label}
                                  </span>
                                  <span className="host">{session.sourceLabel}</span>
                                </div>
                              </td>
                              <td className="navigator-state-column">
                                <span
                                  className={`navigator-state ${status}`}
                                  title={STATUS_META[status].hint}
                                >
                                  <i />
                                  {statusMeta.label}
                                </span>
                                <span className={cn('navigator-context', readiness.key)}>
                                  {readiness.detail}
                                </span>
                              </td>
                              <td className="navigator-time-column">{formatTimeAgo(session.mtime)}</td>
                              <td className="navigator-cost-column">
                                <UnpricedModelWarning unpriced={session.unpricedModels}>
                                  {formatEstimatedCost(session.cost)}
                                </UnpricedModelWarning>
                              </td>
                              <td className="navigator-action-column">
                                <Button
                                  variant="outline"
                                  size="sm"
                                  onClick={(event) => {
                                    event.stopPropagation();
                                    onSelectSession(session);
                                  }}
                                >
                                  {status === 'running' ? 'Watch' : readiness.label}
                                </Button>
                              </td>
                            </tr>
                          );
                        })}
                        {open && rows.length > shown.length && (
                          <tr className="navigator-more-row">
                            <td colSpan={6}>
                              <button
                                type="button"
                                onClick={() =>
                                  setSectionLimits((limits) => ({ ...limits, [status]: limit + 50 }))
                                }
                              >
                                Show {Math.min(50, rows.length - shown.length)} more ·{' '}
                                {rows.length - shown.length} remaining
                              </button>
                            </td>
                          </tr>
                        )}
                      </tbody>
                    );
                  })}
                </table>
              )}
            </LoadingSpinner>
          </div>
        </div>
      </div>
    </div>
  );
}
