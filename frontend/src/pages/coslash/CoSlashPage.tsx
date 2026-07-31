import { useState } from 'react';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
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
} from '@/pages/coslash/CoSlashTabMenus';
import { useSessions } from '@/pages/coslash/hooks/use-sessions';
import { formatCost } from '@/pages/coslash/lib/format';
import { getEstimatedCost } from '@/pages/coslash/lib/pricing';
import { sessionMatchesSearchTerm } from '@/pages/coslash/lib/search';
import { type Session } from '@/pages/coslash/lib/session';
import { timeWindowStart, type TimeWindow } from '@/pages/coslash/lib/time-window';

const WINDOW_ACTIVITY_LABELS: Record<TimeWindow, string> = {
  'week': 'active this week',
  'month': 'active this month',
  '7d': 'active in the last 7 days',
  '30d': 'active in the last 30 days',
  'all': 'across all time',
};

function CoSlashPageHeader({
  searchTerm,
  onSearchTermChange,
}: {
  searchTerm: string;
  onSearchTermChange: (value: string) => void;
}) {
  return (
    <div className="bg-background flex items-center justify-between gap-2 border-b p-2 px-4">
      <div className="flex items-center gap-2">
        <div className="bg-brand grid size-6 place-items-center rounded-sm text-base font-extrabold text-white">
          <span>F</span>
        </div>
        <span className="text-sm font-bold">CoSlash</span>
      </div>
      <div>
        <div className="flex items-center gap-2">
          <Input
            placeholder="Search titles, repos, branches"
            className="bg-muted w-2xs text-xs"
            value={searchTerm}
            onChange={(event) => onSearchTermChange(event.target.value)}
          />
          <Avatar className="size-8">
            <AvatarFallback className="text-muted-foreground text-xs font-bold">
              <span>FL</span>
            </AvatarFallback>
          </Avatar>
        </div>
      </div>
    </div>
  );
}

function CoSlashPageFooter({
  sessions,
  isLoading,
  loadFailed,
}: {
  sessions: Session[];
  isLoading: boolean;
  loadFailed: boolean;
}) {
  const sessionCount = loadFailed
    ? 'session count unavailable'
    : isLoading
      ? 'loading sessions'
      : `${sessions.length} sessions in selected window`;

  return (
    <div className="bg-background flex items-center gap-2 border-t px-4 py-2 font-mono text-xs">
      <span>{sessionCount}</span>
      <span>·</span>
      <span>read-only</span>
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

  return (
    <div className="text-muted-foreground flex items-center gap-2 text-xs">
      <UnpricedModelWarning tokens={sessions.map((session) => session.tokens)}>
        {formatCost(sessions.reduce((sum, session) => sum + getEstimatedCost(session.tokens), 0))}
      </UnpricedModelWarning>
      <span
        className="cursor-help underline decoration-dotted underline-offset-2"
        title="Full-session totals for sessions with activity in the selected window."
      >
        lifetime est. API value
      </span>
      <span>
        · {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}{' '}
        {WINDOW_ACTIVITY_LABELS[timeWindow]} ·{' '}
        {sessions.filter((session) => session.agent === 'claude').length} Claude Code /{' '}
        {sessions.filter((session) => session.agent === 'codex').length} Codex
      </span>
    </div>
  );
}

function CoSlashContent({
  loadFailed,
  onRetry,
  visibleSessions,
  view,
  onSelectSession,
}: {
  loadFailed: boolean;
  onRetry: () => void;
  visibleSessions: Session[];
  view: ViewMode;
  onSelectSession: (session: Session) => void;
}) {
  if (loadFailed) {
    return (
      <div role="alert" className="text-destructive grid h-full place-items-center bg-neutral-50 text-sm">
        <div className="flex flex-col items-center gap-3">
          <div>Unable to load sessions</div>
          <Button variant="outline" size="sm" onClick={onRetry}>
            Try again
          </Button>
        </div>
      </div>
    );
  }
  if (visibleSessions.length === 0) {
    return (
      <div role="status" className="grid h-full place-items-center bg-neutral-50 text-center">
        <div>
          <div className="text-sm font-semibold">No sessions found</div>
          <div className="text-muted-foreground pt-1 text-xs">Try another vendor or a wider time window.</div>
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

export function CoSlashPage() {
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

  return (
    <div className="flex h-svh flex-col">
      <div className="bg-background border-b">
        <CoSlashPageHeader searchTerm={searchTerm} onSearchTermChange={setSearchTerm} />
        <div className="flex items-baseline justify-between px-4 pt-3">
          <h1 className="text-base font-bold">Sessions</h1>
          <LoadingSpinner isLoading={isLoading}>
            <SessionsStats sessions={sessionsInWindow} loadFailed={loadFailed} timeWindow={timeWindow} />
          </LoadingSpinner>
        </div>
        <div className="flex items-center justify-between gap-2 px-4 py-2">
          <div className="flex items-center gap-2">
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
      </div>
      <div className="relative flex-1 overflow-hidden">
        <LoadingSpinner isLoading={isLoading && sessions.length === 0}>
          <CoSlashContent
            loadFailed={loadFailed}
            onRetry={retrySessions}
            visibleSessions={visibleSessions}
            view={view}
            onSelectSession={(session) => setSelectedSessionId(session.id)}
          />
        </LoadingSpinner>
      </div>
      <CoSlashPageFooter sessions={sessionsInWindow} isLoading={isLoading} loadFailed={loadFailed} />
      <SessionInspector
        session={selectedSession}
        sessionsVersion={sessionsVersion}
        onClose={() => setSelectedSessionId(null)}
      />
    </div>
  );
}
