import { useCallback, useEffect, useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { setTheme } from '@/lib/theme';
import { DiagnosticsDialog } from '@/pages/coslash/components/DiagnosticsDialog';
import { FirstRunOnboarding } from '@/pages/coslash/components/FirstRunOnboarding';
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
import {
  SettingsButton,
  SettingsDialog,
  type SettingsDialogMode,
} from '@/pages/coslash/components/SettingsDialog';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import {
  AgentVendorFilterTabMenu,
  TimeWindowFilterTabMenu,
  ViewingModeTabMenu,
  type AgentVendor,
  type ViewMode,
} from '@/pages/coslash/CoslashTabMenus';
import { loadHubDestination } from '@/pages/coslash/features/sharing/api';
import { HUB_SHARE_VERSION, type DestinationResult } from '@/pages/coslash/features/sharing/model';
import { ShareToHubDialog } from '@/pages/coslash/features/sharing/ShareToHubDialog';
import { useDiagnostics } from '@/pages/coslash/hooks/use-diagnostics';
import { useSessions } from '@/pages/coslash/hooks/use-sessions';
import { useSettings } from '@/pages/coslash/hooks/use-settings';
import type { Diagnostics } from '@/pages/coslash/lib/diagnostics';
import { formatEstimatedCost } from '@/pages/coslash/lib/format';
import { sessionsEmptyStateCopy } from '@/pages/coslash/lib/page-copy';
import { sessionMatchesSearchTerm } from '@/pages/coslash/lib/search';
import { getStatus, type Session } from '@/pages/coslash/lib/session';
import { shouldPromptForSynthesisConsent } from '@/pages/coslash/lib/settings';
import { timeWindowStart, type TimeWindow } from '@/pages/coslash/lib/time-window';

const WINDOW_ACTIVITY_LABELS: Record<TimeWindow, string> = {
  'week': 'active this week',
  'month': 'active this month',
  '7d': 'active in the last 7 days',
  '30d': 'active in the last 30 days',
  'all': 'across all time',
};

function fixtureDestination(search: string): DestinationResult {
  const state = new URLSearchParams(search).get('share-state');
  if (
    state === 'signed_out' ||
    state === 'pairing_required' ||
    state === 'credential_dormant' ||
    state === 'credential_revoked'
  ) {
    return { contractVersion: HUB_SHARE_VERSION, state, configured: true };
  }
  return {
    contractVersion: HUB_SHARE_VERSION,
    configured: true,
    state: 'ready',
    destination: {
      workspaceId: '10000000-0000-4000-8000-000000000001',
      workspaceName: 'Compiler Team',
      currentMemberCount: 2,
      resultingMemberCount: 2,
      currentApprovedSessionCount: 3,
      historyDisclosure:
        "Sharing this revision makes it visible to the workspace's current members. Membership and approved-session counts are current when viewed.",
      credentialState: 'paired',
    },
  };
}

function CoslashPageHeader({
  onOpenSettings,
  settingsError,
}: {
  onOpenSettings: () => void;
  settingsError: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-4 px-4">
      <div className="flex items-center gap-2">
        <span aria-label="coSlash">
          <img src="/brand/coslash-logo.svg" alt="" className="h-12 dark:hidden" />
          <img src="/brand/coslash-logo-reverse.svg" alt="" className="hidden h-12 dark:block" />
        </span>
        <span className="text-muted-foreground text-sm font-medium">Run more agents. Lose less context.</span>
      </div>
      <SettingsButton onClick={onOpenSettings} hasError={settingsError} />
    </div>
  );
}

function SettingsErrorBanner({ message, onOpen }: { message: string; onOpen: () => void }) {
  return (
    <div
      role="alert"
      className="text-destructive flex items-center justify-between gap-4 border-y bg-neutral-50 px-4 py-2 text-sm"
    >
      <span>{message} Synthesis is off and terminal launches are blocked.</span>
      <Button variant="outline" size="sm" onClick={onOpen}>
        Repair settings
      </Button>
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
          <span className="text-foreground font-semibold">
            {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'}
          </span>{' '}
          {WINDOW_ACTIVITY_LABELS[timeWindow]} ·{' '}
          {sessions.filter((session) => session.agent === 'claude').length} Claude Code,{' '}
          {sessions.filter((session) => session.agent === 'codex').length} Codex,{' '}
          {sessions.filter((session) => session.agent === 'opencode').length} OpenCode ·
        </span>
        <UnpricedModelWarning unpriced={sessions.flatMap((session) => session.unpricedModels)}>
          {formatEstimatedCost(sessions.reduce((sum, session) => sum + session.cost, 0))}
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
  loadError,
  onRetry,
  visibleSessions,
  hasSessions,
  searchTerm,
  timeWindow,
  view,
  onSelectSession,
  diagnostics,
  diagnosticsLoading,
  diagnosticsLoadFailed,
  onRefreshDiagnostics,
}: {
  loadError: string | null;
  onRetry: () => void;
  visibleSessions: Session[];
  hasSessions: boolean;
  searchTerm: string;
  timeWindow: TimeWindow;
  view: ViewMode;
  onSelectSession: (session: Session) => void;
  diagnostics: Diagnostics | null;
  diagnosticsLoading: boolean;
  diagnosticsLoadFailed: boolean;
  onRefreshDiagnostics: () => void;
}) {
  if (loadError != null) {
    return (
      <div role="alert" className="text-destructive bg-background grid h-full place-items-center text-sm">
        <div className="flex flex-col items-center gap-3">
          <div>{loadError}</div>
          <Button variant="outline" size="sm" onClick={onRetry}>
            Try again
          </Button>
        </div>
      </div>
    );
  }
  if (visibleSessions.length === 0) {
    const firstRun = diagnostics?.sources.every(
      (source) => source.state === 'missing' || source.state === 'empty',
    );
    if (!hasSessions && (diagnosticsLoading || diagnosticsLoadFailed || firstRun)) {
      return (
        <FirstRunOnboarding
          diagnostics={diagnostics}
          isLoading={diagnosticsLoading}
          loadFailed={diagnosticsLoadFailed}
          onRefresh={onRefreshDiagnostics}
        />
      );
    }
    const emptyState = sessionsEmptyStateCopy({ hasSessions, searchTerm, timeWindow });
    return (
      <div role="status" className="bg-background grid h-full place-items-center text-center">
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
        <div className="bg-background flex flex-col gap-4 px-4 py-2">
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
  const shareParams = new URLSearchParams(window.location.search);
  const shareFixtureEnabled = shareParams.get('team-share') === '1';
  const [hubDestination, setHubDestination] = useState<DestinationResult | null>(null);
  const shareEnabled = shareFixtureEnabled || hubDestination?.configured === true;
  const { sessions, isLoading, loadError, sessionsVersion, retrySessions } = useSessions(
    shareEnabled ? 'all' : timeWindow,
  );
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  const diagnosticsEnabled = diagnosticsOpen || (!isLoading && loadError == null && sessions.length === 0);
  const {
    diagnostics,
    isLoading: diagnosticsLoading,
    loadFailed: diagnosticsLoadFailed,
    refresh: refreshDiagnostics,
  } = useDiagnostics(diagnosticsEnabled);
  const [view, setView] = useState<ViewMode>('list');
  const [sortKey, setSortKey] = useState<SortKey>(SortKey.Recency);
  const [sortDir, setSortDir] = useState<SortDir>('desc');
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(null);
  const [searchTerm, setSearchTerm] = useState('');
  const [settingsDialogMode, setSettingsDialogMode] = useState<SettingsDialogMode | null>(null);
  const [shareDialogOpen, setShareDialogOpen] = useState(false);
  const settingsState = useSettings();
  const settingsHaveError = settingsState.loadError != null || settingsState.response?.valid === false;
  const shareDestination = shareFixtureEnabled ? fixtureDestination(window.location.search) : hubDestination;
  const shareFixtureOutcome = shareParams.get('share-result') === 'partial' ? 'partial' : 'success';
  const shareCandidates = useMemo(
    () =>
      sessions.map((session, index) => ({
        session,
        previouslyShared: shareFixtureEnabled && index === 0,
      })),
    [sessions, shareFixtureEnabled],
  );

  const refreshHubDestination = useCallback(async () => {
    const destination = await loadHubDestination();
    setHubDestination(destination);
    return destination;
  }, []);

  useEffect(() => {
    if (shareFixtureEnabled) return;
    void refreshHubDestination().catch(() => undefined);
  }, [refreshHubDestination, shareFixtureEnabled]);

  useEffect(() => {
    if (settingsState.response) setTheme(settingsState.response.settings.appearance.theme);
  }, [settingsState.response]);
  // Held by id, not by value: the inspector must render the freshest record
  // each refresh, and a stored object would freeze at click time. Looked up
  // from the unfiltered list so filters never close an open inspector.
  const selectedSession = sessions.find((session) => session.id === selectedSessionId) ?? null;
  const synthesisSettingsKey = settingsState.response
    ? [
        settingsState.response.persisted,
        settingsState.response.settings.synthesis.enabled,
        settingsState.response.settings.synthesis.backend,
        settingsState.response.settings.synthesis.model,
      ].join(':')
    : 'loading';

  useEffect(() => {
    if (shouldPromptForSynthesisConsent(selectedSession, settingsState.response)) {
      setSettingsDialogMode((current) => current ?? 'synthesis-consent');
    }
  }, [selectedSession, settingsState.response]);
  // Keep live sessions visible even when their logs predate the window.
  const windowStart = timeWindowStart(timeWindow);
  const sessionsInWindow =
    windowStart == null
      ? sessions
      : sessions.filter((session) => session.status != null || session.mtime >= windowStart);
  const sessionsForVendor = sessionsInWindow.filter(
    (session) => vendor === 'all' || session.agent === vendor,
  );
  const visibleSessions = sortSessions(
    sessionsForVendor.filter((session) => sessionMatchesSearchTerm(session, searchTerm)),
    sortKey,
    sortDir,
  );
  const refreshFirstRun = () => {
    retrySessions();
    refreshDiagnostics();
  };

  return (
    <div className="flex h-svh flex-col">
      <CoslashPageHeader
        onOpenSettings={() => setSettingsDialogMode('full-settings')}
        settingsError={settingsHaveError}
      />
      {settingsState.response?.valid === false && (
        <SettingsErrorBanner
          message={settingsState.response.error ?? 'settings.json is invalid.'}
          onOpen={() => setSettingsDialogMode('full-settings')}
        />
      )}
      <div className="bg-background flex flex-col gap-2 border-b px-4 pb-2">
        <div className="-m-1 flex items-center gap-2 overflow-x-auto p-1">
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
          <div className="flex w-full items-center justify-between gap-3">
            <div className="min-w-0 flex-1">
              <LoadingSpinner isLoading={isLoading}>
                <SessionsStats
                  sessions={sessionsForVendor}
                  loadFailed={loadError != null}
                  timeWindow={timeWindow}
                />
              </LoadingSpinner>
            </div>
            <DiagnosticsDialog
              open={diagnosticsOpen}
              onOpenChange={setDiagnosticsOpen}
              diagnostics={diagnostics}
              isLoading={diagnosticsLoading}
              loadFailed={diagnosticsLoadFailed}
              onRefresh={refreshDiagnostics}
            />
            {shareEnabled && (
              <Button variant="outline" size="sm" onClick={() => setShareDialogOpen(true)}>
                Share to Hub
              </Button>
            )}
          </div>
        </div>
      </div>
      <div className="flex flex-1 flex-col overflow-hidden">
        <div className="min-h-0 flex-1 overflow-hidden">
          <LoadingSpinner isLoading={isLoading && sessions.length === 0}>
            <CoslashContent
              loadError={loadError}
              onRetry={retrySessions}
              visibleSessions={visibleSessions}
              hasSessions={sessionsInWindow.length > 0}
              searchTerm={searchTerm}
              timeWindow={timeWindow}
              view={view}
              onSelectSession={(session) => setSelectedSessionId(session.id)}
              diagnostics={diagnostics}
              diagnosticsLoading={diagnosticsLoading}
              diagnosticsLoadFailed={diagnosticsLoadFailed}
              onRefreshDiagnostics={refreshFirstRun}
            />
          </LoadingSpinner>
        </div>
      </div>
      <SessionInspector
        session={selectedSession}
        sessionsVersion={sessionsVersion}
        synthesisSettingsKey={synthesisSettingsKey}
        onClose={() => setSelectedSessionId(null)}
      />
      {shareEnabled && shareDestination && (
        <ShareToHubDialog
          open={shareDialogOpen}
          onOpenChange={setShareDialogOpen}
          candidates={shareCandidates}
          destinationResult={shareDestination}
          fixtureMode={shareFixtureEnabled}
          fixtureOutcome={shareFixtureOutcome}
          onDestinationRefresh={refreshHubDestination}
          onOpenSettings={() => {
            setShareDialogOpen(false);
            setSettingsDialogMode('full-settings');
          }}
        />
      )}
      <SettingsDialog
        open={settingsDialogMode != null}
        mode={settingsDialogMode ?? 'full-settings'}
        onOpenChange={(open) => {
          if (!open) setSettingsDialogMode(null);
        }}
        response={settingsState.response}
        isLoading={settingsState.isLoading}
        loadError={settingsState.loadError}
        saveError={settingsState.saveError}
        isSaving={settingsState.isSaving}
        onSave={settingsState.save}
      />
    </div>
  );
}
