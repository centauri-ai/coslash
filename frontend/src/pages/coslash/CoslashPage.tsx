import { useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { setTheme, type Theme } from '@/lib/theme';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { DiagnosticsDialog } from '@/pages/coslash/components/DiagnosticsDialog';
import { FirstRunOnboarding } from '@/pages/coslash/components/FirstRunOnboarding';
import { LocalUpdateBanner } from '@/pages/coslash/components/LocalUpdateBanner';
import { SessionInspector } from '@/pages/coslash/components/SessionInspector';
import { SettingsDialog, type SettingsDialogMode } from '@/pages/coslash/components/SettingsDialog';
import { chipOf, syncMode } from '@/pages/coslash/features/sync/model';
import { SyncHeader } from '@/pages/coslash/features/sync/SyncHeader';
import { useSyncStatus } from '@/pages/coslash/features/sync/use-sync-status';
import { useDiagnostics } from '@/pages/coslash/hooks/use-diagnostics';
import { useDirectedHandoffs } from '@/pages/coslash/hooks/use-directed-handoffs';
import { useSessions } from '@/pages/coslash/hooks/use-sessions';
import { useSettings } from '@/pages/coslash/hooks/use-settings';
import { apiFetch } from '@/pages/coslash/lib/api';
import { handoffSelection, newestHandoffs, type DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import { isLocalUpdate, type LocalUpdate } from '@/pages/coslash/lib/local-update';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { retryRemoteRefreshAndWait } from '@/pages/coslash/lib/remote-api';
import { buildReviewIndex, remoteReviewAvailability, type ReviewerOption } from '@/pages/coslash/lib/review';
import { isLocalSession, LOCAL_SOURCE_ID, sessionKey } from '@/pages/coslash/lib/session';
import { latestLogicalSessions } from '@/pages/coslash/lib/session-library';
import {
  loadSessionViewPreferences,
  type SessionRange,
  type SessionView,
} from '@/pages/coslash/lib/session-view-preferences';
import {
  initialSettingsDraft,
  requiresFirstRunConsent,
  shouldPromptForSynthesisConsent,
} from '@/pages/coslash/lib/settings';
import type { TimeWindow } from '@/pages/coslash/lib/time-window';

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
  const [view, setView] = useState<SessionView>(() => loadSessionViewPreferences().view);
  const [localUpdate, setLocalUpdate] = useState<LocalUpdate | null>(null);
  const apiWindow = view === 'insights' ? 'all' : apiWindowForRange(range);
  const { sessions, machines, isLoading, loadError, sessionsVersion, retrySessions, refreshSessions } =
    useSessions({
      localWindow: apiWindow,
      remoteWindow: apiWindow,
    });
  const { handoffs, error: handoffsError, refresh: refreshHandoffs } = useDirectedHandoffs();
  const latestHandoffs = useMemo(() => newestHandoffs(handoffs), [handoffs]);
  const [{ selectedSessionKey, pendingTargetKey }, select] = useReducer(handoffSelection, {
    selectedSessionKey: null,
    pendingTargetKey: null,
  });
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  const [settingsDialogMode, setSettingsDialogMode] = useState<SettingsDialogMode | null>(null);
  const [remoteRetryInFlight, setRemoteRetryInFlight] = useState(false);
  const diagnosticsEnabled = diagnosticsOpen || (!isLoading && loadError == null && sessions.length === 0);
  const {
    diagnostics,
    isLoading: diagnosticsLoading,
    loadFailed: diagnosticsLoadFailed,
    refresh: refreshDiagnostics,
  } = useDiagnostics(diagnosticsEnabled);
  const remoteRetryPromise = useRef<Promise<MachineFact | undefined> | null>(null);
  const settingsState = useSettings();
  const syncStatusState = useSyncStatus();
  const syncStatus = syncStatusState.status;
  const librarySessions = useMemo(
    () =>
      latestLogicalSessions(sessions).map((session) => {
        if (!isLocalSession(session) || !syncStatusState.hasStatus) return session;
        const state = syncStatus.sessions[sessionKey(session)] ?? 'not_in_hub';
        return { ...session, hubSyncChip: chipOf(syncMode(syncStatus.state), state) };
      }),
    [sessions, syncStatus, syncStatusState.hasStatus],
  );
  const reviewIndex = useMemo(() => buildReviewIndex(librarySessions), [librarySessions]);
  const selectedSession =
    librarySessions.find((session) => sessionKey(session) === selectedSessionKey) ?? null;
  /* oxlint-disable react/set-state-in-effect -- select a target after a session refresh finds it */
  useEffect(() => {
    if (pendingTargetKey && librarySessions.some((session) => sessionKey(session) === pendingTargetKey))
      select({ type: 'found', key: pendingTargetKey });
  }, [librarySessions, pendingTargetKey]);
  /* oxlint-enable react/set-state-in-effect */

  const selectSession = (key: string | null) => {
    select({ type: 'select', key });
  };

  const openHandoffTarget = (handoff: DirectedHandoff) => {
    if (!handoff.targetSessionId) return;
    const targetKey = sessionKey({
      sourceId: handoff.sourceId,
      agent: handoff.targetAgent,
      id: handoff.targetSessionId,
    });
    if (librarySessions.some((session) => sessionKey(session) === targetKey)) selectSession(targetKey);
    else {
      select({ type: 'pending', key: targetKey });
      retrySessions();
    }
  };
  const configuredRemote = machines.some((machine) => machine.sourceId !== LOCAL_SOURCE_ID);
  const remoteMachine = machines.find((machine) => machine.sourceId !== LOCAL_SOURCE_ID);
  const remoteSourceId = remoteMachine?.sourceId;
  const remoteState = remoteMachine?.state;
  const [remoteReviewRetry, setRemoteReviewRetry] = useState(0);
  const remoteReviewKey = remoteSourceId ? `${remoteSourceId}:${remoteState}:${remoteReviewRetry}` : '';
  const [remoteReviewCheck, setRemoteReviewCheck] = useState<{
    key: string;
    state: 'ready' | 'offline' | 'error';
    reviewers: ReviewerOption[];
  } | null>(null);
  useEffect(() => {
    if (!remoteSourceId) return;
    const controller = new AbortController();
    void apiFetch(`/api/reviews/options?${new URLSearchParams({ source: remoteSourceId })}`, {
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) throw new Error('Could not check remote reviewers');
        return response.json() as Promise<{ state: 'ready' | 'offline'; reviewers: ReviewerOption[] }>;
      })
      .then((result) => {
        if (!controller.signal.aborted) setRemoteReviewCheck({ key: remoteReviewKey, ...result });
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setRemoteReviewCheck({ key: remoteReviewKey, state: 'error', reviewers: [] });
      });
    return () => controller.abort();
  }, [remoteSourceId, remoteReviewKey]);
  const currentRemoteReview = remoteReviewCheck?.key === remoteReviewKey ? remoteReviewCheck : null;
  const remoteReviewerOptions = currentRemoteReview?.reviewers ?? [];
  const { reason: remoteReviewUnavailableReason, retryable: canRetryRemoteReviewers } =
    remoteReviewAvailability(remoteMachine, currentRemoteReview);
  const remoteSessionCount = librarySessions.filter((session) => session.sourceId !== LOCAL_SOURCE_ID).length;
  const synthesisSettingsKey = settingsState.response
    ? [
        settingsState.response.persisted,
        settingsState.response.settings.synthesis.enabled,
        settingsState.response.settings.synthesis.backend,
        settingsState.response.settings.synthesis.model,
      ].join(':')
    : 'loading';

  /* oxlint-disable react/set-state-in-effect -- poll V4 update guidance without blocking rendering */
  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const response = await apiFetch('/api/hub/v4-update');
        if (!response.ok) return;
        const next: unknown = await response.json();
        if (active && isLocalUpdate(next)) setLocalUpdate(next);
      } catch {
        // Keep the last acknowledged update guidance during a transient local read failure.
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 60_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);
  /* oxlint-enable react/set-state-in-effect */

  /* oxlint-disable react/set-state-in-effect -- open required synthesis consent when selection changes */
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
  /* oxlint-enable react/set-state-in-effect */

  /* oxlint-disable react/set-state-in-effect -- clear a selection removed by a session refresh */
  useEffect(() => {
    if (selectedSessionKey != null && selectedSession == null)
      select({ type: 'clear-missing', key: selectedSessionKey });
  }, [selectedSession, selectedSessionKey]);
  /* oxlint-enable react/set-state-in-effect */

  const handleRemoteRetry = () => {
    if (remoteRetryPromise.current != null) return remoteRetryPromise.current;
    setRemoteRetryInFlight(true);
    const retry = retryRemoteRefreshAndWait()
      .catch(() => undefined)
      .finally(() => {
        remoteRetryPromise.current = null;
        setRemoteRetryInFlight(false);
        setRemoteReviewRetry((retry) => retry + 1);
        retrySessions();
        if (diagnosticsOpen) refreshDiagnostics();
      });
    remoteRetryPromise.current = retry;
    return retry;
  };

  const retrySessionsAndReviewers = () => {
    setRemoteReviewRetry((retry) => retry + 1);
    retrySessions();
  };

  const handleRemoteConnectionVerified = () => {
    retrySessionsAndReviewers();
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
          retrySessionsAndReviewers();
          refreshDiagnostics();
        }}
      />
    ) : undefined;

  return (
    <>
      <LocalUpdateBanner update={localUpdate} />
      <CoslashLayout
        sessions={librarySessions}
        reviewIndex={reviewIndex}
        latestHandoffs={latestHandoffs}
        onOpenHandoffTarget={openHandoffTarget}
        machines={machines}
        range={range}
        onRangeChange={setRange}
        onViewChange={(view) => {
          select({ type: 'view', view });
          setView(view);
        }}
        selectedSessionKey={selectedSessionKey}
        onSelectSession={(session) => selectSession(sessionKey(session))}
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
        onRetrySessions={retrySessionsAndReviewers}
        retrying={remoteRetryInFlight}
        isLoading={isLoading}
        loadError={loadError}
        emptyContent={emptyContent}
        banner={
          <>
            {settingsBanner}
            {handoffsError && (
              <SettingsErrorBanner
                message={handoffsError}
                actionLabel="Retry"
                onOpen={() => void refreshHandoffs()}
              />
            )}
          </>
        }
        headerActions={
          <>
            <SyncHeader
              status={syncStatus}
              loaded={syncStatusState.hasStatus}
              onSettings={() => setSettingsDialogMode('full-settings')}
            />
            {selectedSession?.hubSyncChip === 'in_hub' && isLocalSession(selectedSession) && (
              <Badge variant="secondary" className="shrink-0 text-xs font-semibold">
                Already in My space
              </Badge>
            )}
          </>
        }
        inspectorOpen={selectedSession != null}
        reviewerOptions={settingsState.response?.options.reviewers ?? []}
        remoteReviewerOptions={remoteReviewerOptions}
        remoteReviewUnavailableReason={remoteReviewUnavailableReason}
        canRetryRemoteReviewers={canRetryRemoteReviewers}
        onRetryRemoteReviewers={() => setRemoteReviewRetry((retry) => retry + 1)}
        onReviewStarted={refreshSessions}
      />
      <SessionInspector
        session={selectedSession}
        handoff={selectedSession ? latestHandoffs.get(sessionKey(selectedSession)) : undefined}
        onHandoffStarted={() => void refreshHandoffs()}
        onOpenTarget={openHandoffTarget}
        sessionsVersion={sessionsVersion}
        synthesisSettingsKey={synthesisSettingsKey}
        showMachineBadge={configuredRemote}
        machines={machines}
        onRefresh={async () => {
          if (selectedSession != null && !isLocalSession(selectedSession)) await handleRemoteRetry();
          else retrySessions();
        }}
        onClose={() => selectSession(null)}
      />
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
        syncStatus={syncStatusState.hasStatus ? syncStatus : undefined}
        syncStatusLoadState={syncStatusState.loadState}
      />
    </>
  );
}
