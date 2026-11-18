import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import {
  AlertTriangle,
  ChevronRight,
  Folder,
  GitFork,
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
import { SearchDecisions } from '@/pages/coslash/components/SearchDecisions';
import { BoardGroupByMenu } from '@/pages/coslash/components/session-list/BoardGroupByMenu';
import { CoslashHeader } from '@/pages/coslash/components/session-list/CoslashHeader';
import { MachineDot } from '@/pages/coslash/components/session-list/MachineDot';
import { Rollup } from '@/pages/coslash/components/session-list/Rollup';
import { SessionListView } from '@/pages/coslash/components/session-list/SessionListView';
import {
  FacetRow,
  FacetRows,
  SidebarSection,
  type FacetOption,
  type FacetSection,
} from '@/pages/coslash/components/session-list/SidebarSection';
import { styles } from '@/pages/coslash/components/session-list/styles';
import { useRankedSearch } from '@/pages/coslash/hooks/use-ranked-search';
import {
  locationChoiceId,
  locationChoices,
  locationGroups,
  matchingLocation,
  sidebarGroups,
  type Group,
} from '@/pages/coslash/lib/detected-groups';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import type { ReviewerOption, ReviewIndex } from '@/pages/coslash/lib/review';
import {
  canFallbackSearch,
  searchHitKey,
  searchParts,
  sessionMatchesSearch,
} from '@/pages/coslash/lib/search';
import {
  boardStatusKey,
  getSessionVendors,
  getVendor,
  isLocalSession,
  LOCAL_SOURCE_ID,
  sessionKey,
  STATUS_ORDER,
  STATUSES,
  type Session,
  type StatusKey,
} from '@/pages/coslash/lib/session';
import { matchesFacets, rangeStart, sortSessions, type FacetKey } from '@/pages/coslash/lib/session-filter';
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

const SessionBoard = lazy(() =>
  import('@/pages/coslash/components/SessionBoard').then((module) => ({ default: module.SessionBoard })),
);

type FilterKey = 'statusFilters' | 'groupFilters' | 'machineFilters' | 'agentFilters';

const RANGE_OPTIONS: { value: SessionRange; label: string }[] = [
  { value: 'today', label: 'Today' },
  { value: 'this-week', label: 'This week' },
  { value: 'week', label: '7 days' },
  { value: 'month', label: '30 days' },
  { value: 'all', label: 'All time' },
];
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

export function CoslashLayout({
  sessions,
  reviewIndex,
  latestHandoffs,
  onOpenHandoffTarget,
  machines,
  range,
  onRangeChange,
  onViewChange,
  onSearchActiveChange,
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
  onSearchActiveChange?: (active: boolean) => void;
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
  const queryParts = useMemo(() => searchParts(preferences.query), [preferences.query]);
  const searchActive = queryParts.content !== '';
  const search = useRankedSearch(preferences.query);
  const ranked = searchActive && search.results != null;
  const searchHits = useMemo(
    () => new Map((search.results?.sessions ?? []).map((hit) => [searchHitKey(hit), hit])),
    [search.results],
  );
  const indexedRemote = useMemo(
    () => new Set((search.results?.indexed ?? []).map(searchHitKey)),
    [search.results],
  );
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
  const patchPreferences = (patch: Partial<SessionViewPreferences>) => {
    if (patch.query != null) onSearchActiveChange?.(searchParts(patch.query).content !== '');
    setPreferences((current) => ({ ...current, ...patch }));
  };
  const changeView = (view: SessionView) => {
    patchPreferences({ view });
    onViewChange(view);
  };

  useEffect(() => saveSessionViewPreferences({ ...preferences, range }), [preferences, range]);

  const [expandedRepos, setExpandedRepos] = useState<Record<string, boolean>>({});
  const locationInput = useRef<HTMLInputElement>(null);
  const sessionGroups = useMemo(
    () => new Map(sessions.map((session) => [sessionKey(session), locationGroups(session)])),
    [sessions],
  );
  const groups = useMemo(() => sidebarGroups(sessionGroups.values()), [sessionGroups]);
  const agents = useMemo(() => getSessionVendors(sessions), [sessions]);
  const sessionsInRange = useMemo(() => {
    const start = searchActive ? null : rangeStart(range);
    return sessions.filter((session) => start == null || session.status != null || session.mtime >= start);
  }, [sessions, range, searchActive]);
  const searchMatches = useMemo(
    () =>
      new Map(
        sessionsInRange.map((session) => {
          const key = sessionKey(session);
          const groupLabel = sessionGroups
            .get(key)!
            .options.map((g) => [g.label, g.basis].join(' '))
            .join(' ');
          const matches = (query: string) => sessionMatchesSearch(session, groupLabel, query);
          return [
            key,
            ranked
              ? matches(queryParts.filters) &&
                (searchHits.has(key) ||
                  (!isLocalSession(session) && !indexedRemote.has(key) && matches(preferences.query)))
              : (!searchActive || canFallbackSearch(preferences.query)) && matches(preferences.query),
          ];
        }),
      ),
    [
      preferences.query,
      queryParts.filters,
      sessionGroups,
      sessionsInRange,
      ranked,
      searchHits,
      searchActive,
      indexedRemote,
    ],
  );
  // Each facet counts the sessions that pass search and every other facet.
  const facetCounts = useMemo(() => {
    const valueOf: Record<Exclude<FacetKey, 'group'>, (session: Session) => string> = {
      status: (session) => boardStatusKey(session),
      machine: (session) => session.sourceId,
      agent: (session) => session.agent,
    };
    const counts = {} as Record<FacetKey, Map<string, number>>;
    for (const facet of [...Object.keys(valueOf), 'group'] as FacetKey[]) {
      const facetCount = new Map<string, number>();
      for (const session of sessionsInRange) {
        const group = sessionGroups.get(sessionKey(session))!;
        if (!searchMatches.get(sessionKey(session)) || !matchesFacets(session, group, preferences, facet))
          continue;
        if (facet === 'group') {
          const counted = new Set<string>();
          for (const option of group.options) {
            // A repository name alone doesn't verify that a session used its main checkout.
            if (option.kind !== 'Repository') counted.add(locationChoiceId(option));
          }
          for (const id of counted) facetCount.set(id, (facetCount.get(id) ?? 0) + 1);
        } else {
          const value = valueOf[facet](session);
          facetCount.set(value, (facetCount.get(value) ?? 0) + 1);
        }
      }
      counts[facet] = facetCount;
    }
    return counts;
  }, [preferences, searchMatches, sessionGroups, sessionsInRange]);
  const visibleSessions = useMemo(() => {
    const filtered = sessionsInRange.filter(
      (session) =>
        searchMatches.get(sessionKey(session)) &&
        matchesFacets(session, sessionGroups.get(sessionKey(session))!, preferences),
    );
    if (ranked)
      return filtered.sort(
        (a, b) =>
          (searchHits.get(sessionKey(b))?.score ?? -1) - (searchHits.get(sessionKey(a))?.score ?? -1) ||
          b.mtime - a.mtime,
      );
    return sortSessions(filtered, preferences.sort);
  }, [preferences, searchMatches, sessionGroups, sessionsInRange, ranked, searchHits]);
  const displayedGroups = useMemo(
    () =>
      new Map(
        visibleSessions.map((session) => {
          const key = sessionKey(session);
          return [key, matchingLocation(sessionGroups.get(key)!, preferences.groupFilters)!];
        }),
      ),
    [visibleSessions, sessionGroups, preferences.groupFilters],
  );
  const toggleFilter = <K extends FilterKey>(key: K, value: SessionViewPreferences[K][number]) => {
    const values: string[] = preferences[key];
    patchPreferences({
      [key]: values.includes(value) ? values.filter((item) => item !== value) : [...values, value],
    });
  };
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
        onClick: () => toggleFilter('statusFilters', status),
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
        onClick: () => toggleFilter('machineFilters', machine.sourceId),
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
        onClick: () => toggleFilter('agentFilters', agent),
      })),
    },
  ];
  const normalizedGroupQuery = groupQuery.trim().toLowerCase();
  const choices = locationChoices(groups, normalizedGroupQuery);
  const locationOption = (group: Group): FacetOption => {
    const id = locationChoiceId(group);
    const selected = preferences.groupFilters.includes(id);
    const isRepo = group.kind === 'Repository';
    return {
      id,
      label: group.label,
      context: group.context,
      title: isRepo ? group.basis + ' · Main checkout' : group.root || group.label,
      highlight: normalizedGroupQuery,
      icon: group.kind === 'Worktree' ? <GitFork /> : group.kind === 'Folder' ? <Folder /> : undefined,
      count: facetCounts.group.get(id) ?? 0,
      selected,
      onClick: () => patchPreferences({ groupFilters: selected ? [] : [id] }),
    };
  };
  const locationHeading = 'text-meta text-coslash-muted px-2.5 pt-2 pb-1 font-semibold';
  const chipLabel = (value: string) => {
    const option = groups.find((group) => group.id === value || locationChoiceId(group) === value);
    if (value.startsWith('main-repo:')) {
      const repo = groups.find((group) => group.kind === 'Repository' && locationChoiceId(group) === value);
      return (repo?.label ?? value.slice('main-repo:'.length)) + ' · Main checkout';
    }
    return option?.label ?? value;
  };
  const activeChips = [
    ...preferences.statusFilters.map((value) => ({
      kind: 'Status',
      value,
      label: STATUSES[value].label,
      remove: () => toggleFilter('statusFilters', value),
    })),
    ...preferences.groupFilters.map((value) => ({
      kind: 'Group',
      value,
      label: chipLabel(value),
      remove: () => toggleFilter('groupFilters', value),
    })),
    ...preferences.machineFilters.map((value) => ({
      kind: 'Machine',
      value,
      label: machines.find((machine) => machine.sourceId === value)?.label ?? value,
      remove: () => toggleFilter('machineFilters', value),
    })),
    ...preferences.agentFilters.map((value) => ({
      kind: 'Agent',
      value,
      label: getVendor(value).label,
      remove: () => toggleFilter('agentFilters', value),
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
                id={section.id}
                label={section.label}
                open={sideOpen[section.id]}
                isLoading={isLoading}
                onToggle={() => setSideOpen((state) => ({ ...state, [section.id]: !state[section.id] }))}
              >
                <FacetRows options={section.options} />
              </SidebarSection>
            ))}
            <SidebarSection
              id="group"
              label="Locations"
              open={sideOpen.group}
              isLoading={isLoading}
              onToggle={() => setSideOpen((state) => ({ ...state, group: !state.group }))}
              headerClassName="bg-coslash-surface sticky top-[-20px] z-20 pb-1"
              headerExtra={
                sideOpen.group && (
                  <div className="border-coslash-line bg-coslash-bg text-coslash-muted focus-within:border-coslash-accent mx-2 mt-1 mb-1.5 flex items-center gap-1.5 rounded-[7px] border px-2 py-1.5 [&>svg]:size-3.5">
                    <Search />
                    <input
                      type="search"
                      value={groupQuery}
                      onChange={(event) => setGroupQuery(event.target.value)}
                      ref={locationInput}
                      onKeyDown={(event) => {
                        if (event.key === 'Escape') setGroupQuery('');
                      }}
                      placeholder="Find a location"
                      aria-label="Find a location"
                      className="text-coslash-ink min-w-0 flex-1 bg-transparent text-xs outline-none [&::-webkit-search-cancel-button]:hidden"
                    />
                    {groupQuery && (
                      <button
                        type="button"
                        className="hover:bg-coslash-soft grid size-5 cursor-pointer place-items-center rounded transition-colors [&>svg]:size-3"
                        onClick={() => {
                          setGroupQuery('');
                          locationInput.current?.focus();
                        }}
                        aria-label="Clear location search"
                      >
                        <X />
                      </button>
                    )}
                  </div>
                )
              }
            >
              {choices.repositories.length > 0 && (
                <section>
                  <h3 className={locationHeading}>Repositories</h3>
                  {choices.repositories.map(({ group, worktrees, unknown }) => {
                    const expanded = !!normalizedGroupQuery || !!expandedRepos[group.id];
                    return (
                      <div key={group.id}>
                        <FacetRow {...locationOption(group)} />
                        {unknown && (
                          <div className="border-coslash-line ml-3 border-l pl-1">
                            <FacetRow {...locationOption(unknown)} />
                          </div>
                        )}
                        {worktrees.length > 0 && (
                          <div className="border-coslash-line ml-3 border-l pl-1">
                            <button
                              type="button"
                              aria-expanded={expanded}
                              aria-label={'Worktrees in ' + group.label}
                              disabled={!!normalizedGroupQuery}
                              onClick={() =>
                                setExpandedRepos((state) => ({ ...state, [group.id]: !state[group.id] }))
                              }
                              className="text-meta text-coslash-muted hover:text-coslash-ink flex w-full cursor-pointer items-center gap-1 rounded-md px-2 py-1 font-medium disabled:cursor-default"
                            >
                              <ChevronRight
                                className={cn('size-3 transition-transform', { 'rotate-90': expanded })}
                              />
                              Worktrees
                            </button>
                            {expanded && (
                              <div className="border-coslash-line ml-3 border-l pl-1">
                                <FacetRows options={worktrees.map(locationOption)} />
                              </div>
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </section>
              )}
              {choices.others.length > 0 && (
                <section>
                  <h3 className={locationHeading}>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span tabIndex={0} className="cursor-default">
                          Other worktrees
                        </span>
                      </TooltipTrigger>
                      <TooltipContent className="coslash-shell max-w-64">
                        These are verified worktrees. Their parent repository could not be identified.
                      </TooltipContent>
                    </Tooltip>
                  </h3>
                  <FacetRows options={choices.others.map(locationOption)} />
                </section>
              )}
              {choices.folders.length > 0 && (
                <section>
                  <h3 className={locationHeading}>Folders</h3>
                  <FacetRows options={choices.folders.map(locationOption)} />
                </section>
              )}
              <FacetRows options={choices.unlocated.map(locationOption)} />
              {!choices.repositories.length &&
                !choices.others.length &&
                !choices.folders.length &&
                !choices.unlocated.length && (
                  <div className="text-coslash-muted px-2.5 py-2 text-xs">
                    {normalizedGroupQuery ? 'No matching locations.' : 'No locations yet.'}
                  </div>
                )}
            </SidebarSection>
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
                    placeholder="Search sessions — prompts, decisions, file paths…"
                    aria-label="Search sessions and decisions"
                    title="Search may match titles, repositories, branches, prompts, answers, plans, recaps, goals, outcomes, next steps, decisions and their reasons and alternatives, commit subjects, commands, file paths, and pull request links."
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
                  {searchActive && <span className="text-xs">Relevance · All time (search)</span>}
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
                        disabled={searchActive}
                        onClick={() => onRangeChange(option.value)}
                      >
                        {option.label}
                      </button>
                    ))}
                  </div>
                  <ViewSwitch view={preferences.view} onChange={changeView} />
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

            {searchActive && (
              <div role="status" className="text-coslash-muted px-3 py-2 text-xs">
                {search.results
                  ? search.results.truncated
                    ? 'Results truncated. Add more words to narrow your search.'
                    : ''
                  : search.state === 'ready' || search.state === 'checking'
                    ? 'Searching…'
                    : !canFallbackSearch(preferences.query)
                      ? `This search needs the index, which is ${search.state === 'building' ? 'still building' : 'unavailable'}.`
                      : `Search index ${search.state}; using browser search.`}
              </div>
            )}
            {ranked && !isLoading && (
              <SearchDecisions
                decisions={search.results!.decisions}
                sessions={visibleSessions}
                onSelect={onSelectSession}
              />
            )}
            <div className={cn(styles.tableWrap, 'mt-3')}>
              <LoadingSpinner isLoading={isLoading && (searchActive || sessions.length === 0)}>
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
                        No sessions match the current search and filters.
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
                      displayedGroups={displayedGroups}
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
                    sections={ranked ? [{ status: 'idle', rows: visibleSessions }] : sections}
                    ranked={ranked}
                    searchHits={searchHits}
                    handoffs={latestHandoffs}
                    groups={displayedGroups}
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
