import { useState } from 'react';
import { Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { LoadingSpinner } from '@/pages/coslash/components/LoadingSpinner';
import { SessionBoard } from '@/pages/coslash/components/SessionBoard';
import { SessionCard } from '@/pages/coslash/components/SessionCard';
import { SessionInspector } from '@/pages/coslash/components/SessionInspector';
import {
  SessionSortDropdownMenu,
  SortKey,
  sortSessions,
  type SortDir,
} from '@/pages/coslash/components/SessionSortDropdownMenu';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import {
  AgentVendorFilterTabMenu,
  TimeWindowFilterTabMenu,
  ViewingModeTabMenu,
  type AgentVendor,
  type ViewMode,
} from '@/pages/coslash/CoslashTabMenus';
import { useSessions } from '@/pages/coslash/hooks/use-sessions';
import { formatEstimatedCost } from '@/pages/coslash/lib/format';
import { sessionsEmptyStateCopy } from '@/pages/coslash/lib/page-copy';
import { getEstimatedCost } from '@/pages/coslash/lib/pricing';
import { sessionMatchesSearchTerm } from '@/pages/coslash/lib/search';
import { getStatus, type Session } from '@/pages/coslash/lib/session';
import { timeWindowStart, type TimeWindow } from '@/pages/coslash/lib/time-window';

const WINDOW_ACTIVITY_LABELS: Record<TimeWindow, string> = {
  'week': 'active this week',
  'month': 'active this month',
  '7d': 'active in the last 7 days',
  '30d': 'active in the last 30 days',
  'all': 'across all time',
};

function CoslashPageHeader() {
  return (
      <div className="flex items-center gap-2 px-4">
        <img src="/brand/coslash-logo.svg" alt="coSlash" className="h-12" />
        <span className="text-muted-foreground text-sm font-medium">
        Run more agents. Lose less context.
      </span>
    </div>
  );
}

function SessionSearch({
  searchTerm,
  onSearchTermChange,
}: {
  searchTerm: string;
  onSearchTermChange: (value: string) => void;
}) {
  return (
    <div className="relative max-w-sm min-w-32 flex-1">
      <Search className="text-muted-foreground pointer-events-none absolute top-2 left-2.5 size-4" />
      <Input
        placeholder="Search sessions -- title, repo, branch"
        className="bg-muted h-8 pl-8 text-sm"
        value={searchTerm}
        onChange={(event) => onSearchTermChange(event.target.value)}
      />
    </div>
  );
}

function SessionsStats({
  sessions,
  loadFailed,
  timeWindow,
}: {
  sessions: Session[];
  loadFailed: boolean;
  timeWindow: TimeWindow;
}) {
  if (loadFailed) return null;

  const activeSessions = sessions.filter((session) => getStatus(session.status) === 'busy').length;
  const waitingSessions = sessions.filter((session) => getStatus(session.status) === 'waiting').length;

  return (
    <div className="flex w-full min-w-0 items-center justify-between gap-3">
      <div className="text-muted-foreground flex min-w-0 items-center gap-2 text-sm">
        <span className="truncate">
          <span className="font-semibold text-foreground">
            {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}
          </span>{' '}
          {WINDOW_ACTIVITY_LABELS[timeWindow]} ·{' '}
          {sessions.filter((session) => session.agent === 'claude').length} Claude Code,{' '}
          {sessions.filter((session) => session.agent === 'codex').length} Codex ·
        </span>
        <UnpricedModelWarning tokens={sessions.map((session) => session.tokens)}>
          {formatEstimatedCost(sessions.reduce((sum, session) => sum + getEstimatedCost(session.tokens), 0))}
        </UnpricedModelWarning>
        <span
          className="shrink-0 cursor-help underline decoration-dotted underline-offset-2"
          title="Includes each session’s full history, not only activity in this window."
        >
          at list API prices
        </span>
      </div>
      <div className="text-muted-foreground flex shrink-0 items-center gap-3 text-xs">
        <span className="inline-flex items-center gap-1.5">
          <span className="bg-success size-1.5 animate-pulse rounded-full" />
          {activeSessions} active
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="bg-warning size-1.5 rounded-full" />
          {waitingSessions} waiting on you
        </span>
      </div>
    </div>
  );
}

function CoslashContent({
  loadFailed,
  onRetry,
  visibleSessions,
  hasSessions,
  searchTerm,
  outsideWindowMatches,
  view,
  onSelectSession,
}: {
  loadFailed: boolean;
  onRetry: () => void;
  visibleSessions: Session[];
  hasSessions: boolean;
  searchTerm: string;
  outsideWindowMatches: number;
  view: ViewMode;
  onSelectSession: (session: Session) => void;
}) {
  if (loadFailed) {
    return (
      <div role="alert" className="text-destructive grid h-full place-items-center bg-neutral-50 text-sm">
        <div className="flex flex-col items-center gap-3">
          <div>CoSlash couldn’t load sessions from the API.</div>
          <Button variant="outline" size="sm" onClick={onRetry}>
            Try again
          </Button>
        </div>
      </div>
    );
  }
  if (visibleSessions.length === 0) {
    const emptyState = sessionsEmptyStateCopy({ hasSessions, searchTerm, outsideWindowMatches });
    return (
      <div role="status" className="grid h-full place-items-center bg-neutral-50 text-center">
        <div>
          <div className="text-sm font-semibold">{emptyState.title}</div>
          {emptyState.detail && <div className="text-muted-foreground pt-1 text-xs">{emptyState.detail}</div>}
        </div>
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto">
      {view === 'board' ? (
        <SessionBoard sessions={visibleSessions} onSelectSession={onSelectSession} />
      ) : (
        <div className="flex flex-col gap-4 bg-neutral-50 px-4 py-2">
          {visibleSessions.map((session) => (
            <SessionCard
              key={`${session.agent}:${session.id}`}
              session={session}
              onClick={() => onSelectSession(session)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export function CoslashPage() {
  const [vendor, setVendor] = useState<AgentVendor>('all');
  const [timeWindow, setTimeWindow] = useState<TimeWindow>('week');
  const { sessions, isLoading, loadFailed, sessionsVersion, retrySessions } = useSessions();
  const [view, setView] = useState<ViewMode>('list');
  const [sortKey, setSortKey] = useState<SortKey>(SortKey.Recency);
  const [sortDir, setSortDir] = useState<SortDir>('desc');
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(null);
  const [searchTerm, setSearchTerm] = useState('');
  // Held by id, not by value: the inspector must render the freshest record
  // each refresh, and a stored object would freeze at click time. Looked up
  // from the unfiltered list so filters never close an open inspector.
  const selectedSession = sessions.find((session) => session.id === selectedSessionId) ?? null;
  // The API returns every session, so the window is applied here — switching it
  // never refetches. A live session shows regardless of how old its log is.
  const windowStart = timeWindowStart(timeWindow);
  const sessionsInWindow =
    windowStart == null
      ? sessions
      : sessions.filter((session) => session.status != null || session.mtime >= windowStart);
  const visibleSessions = sortSessions(
    sessionsInWindow.filter(
      (session) =>
        (vendor === 'all' || session.agent === vendor) && sessionMatchesSearchTerm(session, searchTerm),
    ),
    sortKey,
    sortDir,
  );
  const outsideWindowMatches =
    windowStart == null
      ? 0
      : sessions.filter(
          (session) =>
            session.status == null &&
            session.mtime < windowStart &&
            (vendor === 'all' || session.agent === vendor) &&
            sessionMatchesSearchTerm(session, searchTerm),
        ).length;

  return (
    <div className="flex h-svh flex-col">
      <CoslashPageHeader />
      <div className="bg-background flex flex-col border-b px-4 gap-2 pb-2">
        <div className="flex items-center gap-2 overflow-x-auto">
          <SessionSearch searchTerm={searchTerm} onSearchTermChange={setSearchTerm} />
          <div className="flex shrink-0 items-center gap-2">
            <AgentVendorFilterTabMenu value={vendor} onValueChange={setVendor} />
            <span className="bg-border h-5 w-px" />
            <TimeWindowFilterTabMenu value={timeWindow} onValueChange={setTimeWindow} />
            <span className="bg-border h-5 w-px" />
            <ViewingModeTabMenu value={view} onValueChange={setView} />
          </div>
          <SessionSortDropdownMenu
            sortKey={sortKey}
            sortDir={sortDir}
            onSortKeyChange={setSortKey}
            onSortDirChange={setSortDir}
          />
        </div>
        <div className="flex min-h-7 items-center">
          <LoadingSpinner isLoading={isLoading}>
            <SessionsStats sessions={sessionsInWindow} loadFailed={loadFailed} timeWindow={timeWindow} />
          </LoadingSpinner>
        </div>
      </div>
      <div className="relative flex-1 overflow-hidden">
        <LoadingSpinner isLoading={isLoading && sessions.length === 0}>
          <CoslashContent
            loadFailed={loadFailed}
            onRetry={retrySessions}
            visibleSessions={visibleSessions}
            hasSessions={sessions.length > 0}
            searchTerm={searchTerm}
            outsideWindowMatches={outsideWindowMatches}
            view={view}
            onSelectSession={(session) => setSelectedSessionId(session.id)}
          />
        </LoadingSpinner>
      </div>
      <SessionInspector
        session={selectedSession}
        sessionsVersion={sessionsVersion}
        onClose={() => setSelectedSessionId(null)}
      />
    </div>
  );
}
