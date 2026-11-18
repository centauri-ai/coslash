import { lazy, Suspense, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  AlertTriangle,
  ChevronRight,
  Folder,
  FolderGit2,
  Monitor,
  Rows3,
  Rows4,
  Search,
  Server,
  X,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import type { Theme } from '@/lib/theme';
import { cn } from '@/lib/utils';
import { InsightsView } from '@/pages/coslash/components/InsightsView';
import { LoadingSpinner } from '@/pages/coslash/components/LoadingSpinner';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import {
  MACHINE_TONE_DOT,
  machineRetryable,
  machineStatusText,
  machineTone,
} from '@/pages/coslash/lib/machine-status';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import type { ReviewerOption, ReviewIndex } from '@/pages/coslash/lib/review';
import { sessionMatchesSearch } from '@/pages/coslash/lib/search';
import {
  boardStatusKey,
  getSessionVendors,
  getVendor,
  LOCAL_SOURCE_ID,
  sessionKey,
  STATUS_ORDER,
  STATUSES,
  type Session,
  type StatusKey,
} from '@/pages/coslash/lib/session';
import {
  BOARD_GROUP_BY_OPTIONS,
  BOARD_ROW_GROUP_BY_OPTIONS,
  type BoardGroupBy,
  type BoardRowGroupBy,
} from '@/pages/coslash/lib/session-grouping';
import {
  loadSessionViewPreferences,
  saveSessionViewPreferences,
  type SessionRange,
  type SessionSort,
  type SessionView,
  type SessionViewPreferences,
} from '@/pages/coslash/lib/session-view-preferences';
import './coslash-layout.css';
import { BoardGroupByMenu } from '@/pages/coslash/components/session-list/BoardGroupByMenu';
import { CoslashHeader } from '@/pages/coslash/components/session-list/CoslashHeader';
import { InlineSpinner } from '@/pages/coslash/components/session-list/InlineSpinner';
import { Rollup } from '@/pages/coslash/components/session-list/Rollup';
import { SessionListView } from '@/pages/coslash/components/session-list/SessionListView';
import {
  FacetRows,
  SidebarSection,
  type FacetOption,
  type FacetSection,
} from '@/pages/coslash/components/session-list/SidebarSection';
import { styles } from '@/pages/coslash/components/session-list/styles';
import { detectedGroup, type Group } from '@/pages/coslash/lib/detected-groups';
import { matchesFacets, rangeStart, sortSessions, type FacetKey } from '@/pages/coslash/lib/session-filter';

const SessionBoard = lazy(() =>
  import('@/pages/coslash/components/SessionBoard').then((module) => ({ default: module.SessionBoard })),
);

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

function ViewSwitch({ view, onChange }: { view: SessionView; onChange: (view: SessionView) => void }) {
  return (
    <div className={styles.segmented} aria-label="View">
      {(['list', 'board', 'insights'] as const).map((value) => (
        <button
          key={value}
          type="button"
          className={cn(
            'text-meta text-coslash-muted hover:text-coslash-ink min-h-7 cursor-pointer rounded-[7px] px-2.5 py-1 transition-colors',
            { 'bg-coslash-surface text-coslash-ink font-semibold shadow-sm': view === value },
          )}
          aria-pressed={view === value}
          onClick={() => onChange(value)}
        >
          {value === 'list' ? 'Table' : value === 'board' ? 'Board' : 'Insights'}
        </button>
      ))}
    </div>
  );
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

export function CoslashLayout({
  sessions,
  reviewIndex,
  latestHandoffs,
  onOpenHandoffTarget,
  machines,
  range,
  onRangeChange,
  onViewChange,
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
  synthesisCostVersion,
  emptyContent,
  banner,
  headerActions,
  inspectorOpen = false,
  reviewerOptions,
  remoteReviewerOptions,
  remoteReviewUnavailableReason,
  canRetryRemoteReviewers,
  onRetryRemoteReviewers,
  onReviewStarted,
}: {
  sessions: Session[];
  reviewIndex: ReviewIndex<Session>;
  latestHandoffs: ReadonlyMap<string, DirectedHandoff>;
  onOpenHandoffTarget: (handoff: DirectedHandoff) => void;
  machines: MachineFact[];
  range: SessionRange;
  onRangeChange: (range: SessionRange) => void;
  onViewChange: (view: SessionView) => void;
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
  synthesisCostVersion: string | null;
  emptyContent?: ReactNode;
  banner?: ReactNode;
  headerActions?: ReactNode;
  inspectorOpen?: boolean;
  reviewerOptions: readonly ReviewerOption[];
  remoteReviewerOptions: readonly ReviewerOption[];
  remoteReviewUnavailableReason?: string;
  canRetryRemoteReviewers: boolean;
  onRetryRemoteReviewers: () => void;
  onReviewStarted: () => void;
}) {
  const [preferences, setPreferences] = useState(loadSessionViewPreferences);
  const [openSections, setOpenSections] = useState<Record<StatusKey, boolean>>({
    busy: true,
    waiting: true,
    idle: true,
    inactive: true,
    unknown: true,
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
  const changeView = (view: SessionView) => {
    patchPreferences({ view });
    onViewChange(view);
  };

  useEffect(() => saveSessionViewPreferences({ ...preferences, range }), [preferences, range]);

  const sessionGroups = useMemo(() => {
    const groupsById = new Map<string, Group>();
    const result = new Map<string, Group>();
    for (const session of sessions) {
      const detected = detectedGroup(session);
      const group = groupsById.get(detected.id) ?? detected;
      groupsById.set(group.id, group);
      result.set(sessionKey(session), group);
    }
    return result;
  }, [sessions]);
  const groups = useMemo(
    () => [...new Set(sessionGroups.values())].sort((a, b) => a.label.localeCompare(b.label)),
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
          sessionMatchesSearch(session, sessionGroups.get(sessionKey(session))!.label, preferences.query),
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
  // Each facet counts the sessions that pass search and every other facet.
  const facetCounts = useMemo(() => {
    const valueOf: Record<FacetKey, (session: Session, group: Group) => string> = {
      status: (session) => boardStatusKey(session),
      machine: (session) => session.sourceId,
      agent: (session) => session.agent,
      group: (_session, group) => group.id,
    };
    const counts = {} as Record<FacetKey, Map<string, number>>;
    for (const facet of Object.keys(valueOf) as FacetKey[]) {
      const facetCount = new Map<string, number>();
      for (const session of sessionsInRange) {
        const group = sessionGroups.get(sessionKey(session))!;
        if (!searchMatches.get(sessionKey(session)) || !matchesFacets(session, group, preferences, facet))
          continue;
        const value = valueOf[facet](session, group);
        facetCount.set(value, (facetCount.get(value) ?? 0) + 1);
      }
      counts[facet] = facetCount;
    }
    return counts;
  }, [preferences, searchMatches, sessionGroups, sessionsInRange]);
  const toggleStatus = (status: StatusKey) =>
    patchPreferences({
      statusFilters: preferences.statusFilters.includes(status)
        ? preferences.statusFilters.filter((value) => value !== status)
        : [...preferences.statusFilters, status],
    });
  const toggleGroup = (group: Group) =>
    patchPreferences({
      groupFilters: preferences.groupFilters.includes(group.id)
        ? preferences.groupFilters.filter((value) => value !== group.id)
        : [...preferences.groupFilters, group.id],
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

  if (preferences.view === 'insights') {
    const showOnboarding = !isLoading && loadError == null && sessions.length === 0 && emptyContent != null;
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
          <div className={cn(styles.main, { 'overflow-y-auto': showOnboarding })}>
            <div className="flex justify-end pb-4">
              <ViewSwitch view={preferences.view} onChange={changeView} />
            </div>
            {showOnboarding && <div className="shrink-0">{emptyContent}</div>}
            <InsightsView
              sessions={sessions}
              isLoading={isLoading}
              loadError={loadError}
              synthesisCostVersion={synthesisCostVersion}
              onRetry={onRetrySessions}
              inPageFlow={showOnboarding}
            />
          </div>
        </div>
      </TooltipProvider>
    );
  }

  const facetSections: FacetSection[] = [
    {
      id: 'status',
      label: 'Status',
      options: STATUS_ORDER.map((status) => ({
        id: status,
        status,
        label: STATUSES[status].label,
        count: facetCounts.status.get(status) ?? 0,
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
        count: facetCounts.machine.get(machine.sourceId) ?? 0,
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
        count: facetCounts.agent.get(agent) ?? 0,
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
        count: facetCounts.group.get(group.id) ?? 0,
        selected: preferences.groupFilters.includes(group.id),
        onClick: () => toggleGroup(group),
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
                      {groupQuery.trim() === '' ? 'No groups yet.' : `No groups match “${groupQuery}”.`}
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>

          <div className={styles.main}>
            <div className="flex justify-end pb-4">
              <ViewSwitch view={preferences.view} onChange={changeView} />
            </div>
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
                <div className="no-scrollbar flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto">
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
                <div className="flex max-w-full shrink-0 flex-wrap items-center gap-2">
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
                  {preferences.view === 'list' && (
                    <>
                      <span className="bg-coslash-line h-7 w-px" aria-hidden="true" />
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
                    </>
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
                      handoffs={latestHandoffs}
                      onOpenHandoffTarget={onOpenHandoffTarget}
                      columnGroupBy={preferences.boardColumns}
                      rowGroupBy={preferences.boardRows}
                      selectedSessionKey={selectedSessionKey}
                      onSelectSession={onSelectSession}
                      review={{
                        index: reviewIndex,
                        reviewerOptions,
                        remoteReviewerOptions,
                        remoteUnavailableReason: remoteReviewUnavailableReason,
                        canRetryRemoteReviewers,
                        onRetryRemoteReviewers,
                        onStarted: onReviewStarted,
                        onSelectRelated: onSelectSession,
                      }}
                    />
                  </Suspense>
                ) : (
                  <SessionListView
                    sections={sections}
                    handoffs={latestHandoffs}
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
                    review={{
                      index: reviewIndex,
                      reviewerOptions,
                      remoteReviewerOptions,
                      remoteUnavailableReason: remoteReviewUnavailableReason,
                      canRetryRemoteReviewers,
                      onRetryRemoteReviewers,
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
