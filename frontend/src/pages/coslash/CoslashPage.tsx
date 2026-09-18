import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { setTheme, type Theme } from '@/lib/theme';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { DiagnosticsDialog } from '@/pages/coslash/components/DiagnosticsDialog';
import { FirstRunOnboarding } from '@/pages/coslash/components/FirstRunOnboarding';
import { SessionInspector } from '@/pages/coslash/components/SessionInspector';
import { SettingsDialog, type SettingsDialogMode } from '@/pages/coslash/components/SettingsDialog';
import { FullSessionShareDialog } from '@/pages/coslash/features/full-sharing/FullSessionShareDialog';
import { fullSessionCandidates } from '@/pages/coslash/features/full-sharing/model';
import { loadHubDestination } from '@/pages/coslash/features/sharing/api';
import {
  HUB_SHARE_VERSION,
  type DestinationResult,
  type ShareWindow,
} from '@/pages/coslash/features/sharing/model';
import { ShareToHubDialog } from '@/pages/coslash/features/sharing/ShareToHubDialog';
import { useDiagnostics } from '@/pages/coslash/hooks/use-diagnostics';
import { useSessions, useShareCandidates } from '@/pages/coslash/hooks/use-sessions';
import { useSettings } from '@/pages/coslash/hooks/use-settings';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { retryRemoteRefreshAndWait } from '@/pages/coslash/lib/remote-api';
import { isLocalSession, LOCAL_SOURCE_ID, sessionKey } from '@/pages/coslash/lib/session';
import { eligibleSessionCandidates, latestLogicalSessions } from '@/pages/coslash/lib/session-library';
import { loadSessionViewPreferences, type SessionRange } from '@/pages/coslash/lib/session-view-preferences';
import {
  initialSettingsDraft,
  requiresFirstRunConsent,
  shouldPromptForSynthesisConsent,
} from '@/pages/coslash/lib/settings';
import type { TimeWindow } from '@/pages/coslash/lib/time-window';

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

function apiWindowForRange(range: SessionRange): TimeWindow {
  if (range === 'this-week') return 'week';
  if (range === 'today' || range === 'week') return '7d';
  if (range === 'month') return '30d';
  return 'all';
}

function SettingsErrorBanner({
  message,
  actionLabel,
  onOpen,
}: {
  message: string;
  actionLabel: string;
  onOpen: () => void;
}) {
  return (
    <div
      role="alert"
      className="text-danger-fg border-danger-fg bg-danger-bg flex items-center justify-between gap-4 border-b px-5 py-2 text-sm"
    >
      <span>{message}</span>
      <Button variant="outline" size="sm" onClick={onOpen}>
        {actionLabel}
      </Button>
    </div>
  );
}

export function CoslashPage() {
  const [range, setRange] = useState<SessionRange>(() => loadSessionViewPreferences().range);
  const shareParams = new URLSearchParams(window.location.search);
  const shareFixtureEnabled = shareParams.get('team-share') === '1';
  const [hubDestination, setHubDestination] = useState<DestinationResult | null>(null);
  const shareEnabled = shareFixtureEnabled || hubDestination?.configured === true;
  const apiWindow = apiWindowForRange(range);
  const { sessions, machines, isLoading, loadError, sessionsVersion, retrySessions, refreshSessions } =
    useSessions({
      localWindow: apiWindow,
      remoteWindow: apiWindow,
    });
  const [selectedSessionKey, setSelectedSessionKey] = useState<string | null>(null);
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  const [settingsDialogMode, setSettingsDialogMode] = useState<SettingsDialogMode | null>(null);
  const [shareDialogOpen, setShareDialogOpen] = useState(false);
  const [shareWindow, setShareWindow] = useState<ShareWindow>('7d');
  const [remoteRetryInFlight, setRemoteRetryInFlight] = useState(false);
  const diagnosticsEnabled = diagnosticsOpen || (!isLoading && loadError == null && sessions.length === 0);
  const {
    diagnostics,
    isLoading: diagnosticsLoading,
    loadFailed: diagnosticsLoadFailed,
    refresh: refreshDiagnostics,
  } = useDiagnostics(diagnosticsEnabled);
  const [fullShareDialogOpen, setFullShareDialogOpen] = useState(false);
  const remoteRetryPromise = useRef<Promise<MachineFact | undefined> | null>(null);
  const settingsState = useSettings();
  const shareDestination = shareFixtureEnabled ? fixtureDestination(window.location.search) : hubDestination;
  const shareFixtureOutcome = shareParams.get('share-result') === 'partial' ? 'partial' : 'success';
  const shareCandidateResult = useShareCandidates({
    enabled: shareDialogOpen && !shareFixtureEnabled && shareDestination?.state === 'ready',
    window: shareWindow,
  });
  const librarySessions = useMemo(() => latestLogicalSessions(sessions), [sessions]);
  const selectedSession =
    librarySessions.find((session) => sessionKey(session) === selectedSessionKey) ?? null;
  const configuredRemote = machines.some((machine) => machine.sourceId !== LOCAL_SOURCE_ID);
  const remoteSessionCount = librarySessions.filter((session) => session.sourceId !== LOCAL_SOURCE_ID).length;
  const shareCandidates = useMemo(() => {
    const eligible = eligibleSessionCandidates(
      shareFixtureEnabled ? sessions : shareCandidateResult.sessions,
    );
    return eligible.map((session, index) => ({
      session,
      previouslyShared: shareFixtureEnabled && index === 0,
    }));
  }, [sessions, shareCandidateResult.sessions, shareFixtureEnabled]);
  const fullShareCandidates = useMemo(() => fullSessionCandidates(librarySessions), [librarySessions]);
  const synthesisSettingsKey = settingsState.response
    ? [
        settingsState.response.persisted,
        settingsState.response.settings.synthesis.enabled,
        settingsState.response.settings.synthesis.backend,
        settingsState.response.settings.synthesis.model,
      ].join(':')
    : 'loading';

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

  useEffect(() => {
    if (
      selectedSession != null &&
      isLocalSession(selectedSession) &&
      shouldPromptForSynthesisConsent(selectedSession, settingsState.response)
    ) {
      setSettingsDialogMode((current) => current ?? 'synthesis-consent');
    }
  }, [selectedSession, settingsState.response]);

  useEffect(() => {
    if (selectedSessionKey != null && selectedSession == null) setSelectedSessionKey(null);
  }, [selectedSession, selectedSessionKey]);

  const handleRemoteRetry = () => {
    if (remoteRetryPromise.current != null) return remoteRetryPromise.current;
    setRemoteRetryInFlight(true);
    const retry = retryRemoteRefreshAndWait()
      .catch(() => undefined)
      .finally(() => {
        remoteRetryPromise.current = null;
        setRemoteRetryInFlight(false);
        retrySessions();
        if (diagnosticsOpen) refreshDiagnostics();
      });
    remoteRetryPromise.current = retry;
    return retry;
  };

  const handleRemoteConnectionVerified = () => {
    retrySessions();
    if (diagnosticsOpen) refreshDiagnostics();
  };

  const saveSettings = async (...args: Parameters<typeof settingsState.save>) => {
    const ok = await settingsState.save(...args);
    if (ok) handleRemoteRetry();
    return ok;
  };

  const handleThemeChange = (theme: Theme) => {
    const response = settingsState.response;
    if (response == null || response.settings.appearance.theme === theme) return;
    const previousTheme = response.settings.appearance.theme;
    setTheme(theme);
    void settingsState.save({ ...initialSettingsDraft(response), appearance: { theme } }).then((saved) => {
      if (!saved) setTheme(previousTheme);
    });
  };

  const settingsBanner =
    settingsState.response?.valid === false ? (
      <SettingsErrorBanner
        message={`${settingsState.response.error ?? 'settings.json is invalid.'} Synthesis is off and terminal launches are blocked.`}
        actionLabel="Repair settings"
        onOpen={() => setSettingsDialogMode('full-settings')}
      />
    ) : settingsState.loadError != null ? (
      <SettingsErrorBanner
        message={`Settings could not be loaded: ${settingsState.loadError}`}
        actionLabel="View details"
        onOpen={() => setSettingsDialogMode('full-settings')}
      />
    ) : undefined;

  const firstRun = diagnostics?.sources.every(
    (source) => source.state === 'missing' || source.state === 'empty',
  );
  const emptyContent =
    librarySessions.length === 0 && (diagnosticsLoading || diagnosticsLoadFailed || firstRun) ? (
      <FirstRunOnboarding
        diagnostics={diagnostics}
        isLoading={diagnosticsLoading}
        loadFailed={diagnosticsLoadFailed}
        onRefresh={() => {
          retrySessions();
          refreshDiagnostics();
        }}
      />
    ) : undefined;

  return (
    <>
      <CoslashLayout
        sessions={librarySessions}
        machines={machines}
        range={range}
        onRangeChange={setRange}
        selectedSessionKey={selectedSessionKey}
        onSelectSession={(session) => setSelectedSessionKey(sessionKey(session))}
        diagnostics={
          <DiagnosticsDialog
            open={diagnosticsOpen}
            onOpenChange={setDiagnosticsOpen}
            diagnostics={diagnostics}
            isLoading={diagnosticsLoading}
            loadFailed={diagnosticsLoadFailed}
            onRefresh={refreshDiagnostics}
            remoteSessionCount={remoteSessionCount}
          />
        }
        onSettings={() => setSettingsDialogMode('full-settings')}
        theme={settingsState.response?.settings.appearance.theme ?? 'light'}
        onThemeChange={handleThemeChange}
        themeDisabled={
          settingsState.response == null ||
          settingsState.isSaving ||
          settingsState.response.valid === false ||
          requiresFirstRunConsent(settingsState.response)
        }
        onRetry={handleRemoteRetry}
        onRetrySessions={retrySessions}
        retrying={remoteRetryInFlight}
        isLoading={isLoading}
        loadError={loadError}
        emptyContent={emptyContent}
        banner={settingsBanner}
        headerActions={
          shareEnabled ? (
            <>
              {shareDestination?.state === 'ready' && (
                <Badge
                  variant="secondary"
                  className="text-info-fg bg-info-bg shrink-0 gap-1 text-xs font-semibold"
                >
                  <ShieldCheck className="size-3.5" aria-hidden="true" />
                  {shareDestination.destination.workspaceName} paired
                </Badge>
              )}
              <Button variant="outline" size="sm" onClick={() => setShareDialogOpen(true)}>
                Share to Hub
              </Button>
              {shareDestination?.state === 'ready' && fullShareCandidates.length > 0 && (
                <Button variant="outline" size="sm" onClick={() => setFullShareDialogOpen(true)}>
                  Share full revision
                </Button>
              )}
            </>
          ) : undefined
        }
        inspectorOpen={selectedSession != null}
        reviewerOptions={settingsState.response?.options.reviewers ?? []}
        onReviewStarted={refreshSessions}
      />
      <SessionInspector
        session={selectedSession}
        sessionsVersion={sessionsVersion}
        synthesisSettingsKey={synthesisSettingsKey}
        showMachineBadge={configuredRemote}
        machines={machines}
        onRefresh={async () => {
          if (selectedSession != null && !isLocalSession(selectedSession)) await handleRemoteRetry();
          else retrySessions();
        }}
        onClose={() => setSelectedSessionKey(null)}
      />
      {shareEnabled && shareDestination && (
        <ShareToHubDialog
          open={shareDialogOpen}
          onOpenChange={(open) => {
            setShareDialogOpen(open);
            if (!open) setShareWindow('7d');
          }}
          candidates={shareCandidates}
          candidatesLoading={!shareFixtureEnabled && shareCandidateResult.isLoading}
          candidatesError={shareFixtureEnabled ? null : shareCandidateResult.loadError}
          window={shareWindow}
          onWindowChange={setShareWindow}
          destinationResult={shareDestination}
          fixtureMode={shareFixtureEnabled}
          fixtureOutcome={shareFixtureOutcome}
          onDestinationRefresh={refreshHubDestination}
          onOpenSettings={() => {
            setShareDialogOpen(false);
            setShareWindow('7d');
            setSettingsDialogMode('full-settings');
          }}
        />
      )}
      {shareDestination && (
        <FullSessionShareDialog
          open={fullShareDialogOpen}
          onOpenChange={setFullShareDialogOpen}
          candidates={fullShareCandidates}
          destinationResult={shareDestination}
          onDestinationRefresh={refreshHubDestination}
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
        onSave={saveSettings}
        onRemoteConnectionVerified={handleRemoteConnectionVerified}
        onRemoteRetry={handleRemoteRetry}
        remoteRetryInFlight={remoteRetryInFlight}
      />
    </>
  );
}
