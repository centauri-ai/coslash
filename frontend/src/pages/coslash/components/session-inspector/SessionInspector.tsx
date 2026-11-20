import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from 'react';
import { LoaderCircleIcon } from 'lucide-react';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { DiffList } from '@/pages/coslash/components/DiffList';
import {
  DetailLoadError,
  SnapshotRefreshStatus,
  SnapshotStalenessNotice,
  SummaryOnlyBanner,
} from '@/pages/coslash/components/session-inspector/DetailStatus';
import { InspectorBody, InspectorFooter } from '@/pages/coslash/components/session-inspector/InspectorBody';
import {
  HeaderMeta,
  SessionInspectorTitle,
} from '@/pages/coslash/components/session-inspector/InspectorHeader';
import { useInspectorWidth } from '@/pages/coslash/components/session-inspector/use-inspector-width';
import { useSessionDetail } from '@/pages/coslash/hooks/use-session-detail';
import { useFileDiff, type FileSelection } from '@/pages/coslash/hooks/use-sessions';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  isLocalSession,
  remoteLaunchDisabledHint,
  resumeDisabledHint,
  sessionDisplayTitle,
  sessionKey,
  type Session,
} from '@/pages/coslash/lib/session';
import {
  cachedOfflineWarning,
  filePanelOpen,
  refreshSourceAndRetry,
  snapshotMayBeStale,
} from '@/pages/coslash/lib/session-detail';

/** Must stay in sync with the docked-inspector breakpoint in coslash-layout.css. */
const DOCKED_INSPECTOR_QUERY = '(min-width: 1720px)';

export function SessionInspector({
  session,
  sessionsVersion,
  synthesisSettingsKey,
  synthesisCostVersion,
  showMachineBadge = false,
  machines,
  onRefresh,
  onClose,
  handoff,
  onHandoffStarted,
  onOpenTarget,
}: {
  session: Session | null;
  sessionsVersion: number;
  synthesisSettingsKey: string;
  synthesisCostVersion: string | null;
  showMachineBadge?: boolean;
  machines: MachineFact[];
  onRefresh: () => void | Promise<void>;
  onClose: () => void;
  handoff?: DirectedHandoff;
  onHandoffStarted: () => void;
  onOpenTarget: (handoff: DirectedHandoff) => void;
}) {
  const [detailRetryToken, setDetailRetryToken] = useState(0);
  const detailAttemptIdentity = session == null ? null : `${sessionKey(session)}@${detailRetryToken}`;
  const detailAttemptRef = useRef(detailAttemptIdentity);
  useLayoutEffect(() => {
    detailAttemptRef.current = detailAttemptIdentity;
  }, [detailAttemptIdentity]);
  const { detail, isLoading, loadError, loadErrorKind, cachedOffline, summaryOnly, synthesisSettledCount } =
    useSessionDetail(session, detailRetryToken, sessionsVersion, synthesisSettingsKey);
  const contentRef = useRef<HTMLDivElement>(null);
  const [selectedDiffState, setSelectedDiff] = useState<FileSelection | null>(null);
  const diffTriggerRef = useRef<HTMLButtonElement | null>(null);
  const selectedDiff = filePanelOpen(selectedDiffState, detail) ? selectedDiffState : null;
  const [fileDiffRetryToken, setFileDiffRetryToken] = useState(0);
  const [modal, setModal] = useState(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? !window.matchMedia(DOCKED_INSPECTOR_QUERY).matches
      : true,
  );
  const {
    changes: fileChanges,
    isLoading: fileDiffLoading,
    loadError: fileDiffError,
    loadErrorKind: fileDiffErrorKind,
  } = useFileDiff(selectedDiff, fileDiffRetryToken);
  const isOpen = session != null;
  const { inspectorWidth, separatorProps } = useInspectorWidth(contentRef, isOpen);
  const remoteLaunchable =
    detail != null &&
    !isLocalSession(detail) &&
    detail.cwd.trim() !== '' &&
    detail.launchable &&
    machines.some(
      (machine) =>
        machine.sourceId === detail.sourceId && (machine.state === 'ok' || machine.state === 'limited'),
    );
  const remoteMachine =
    detail == null ? undefined : machines.find((machine) => machine.sourceId === detail.sourceId);
  const showCachedOffline = cachedOfflineWarning(cachedOffline, remoteMachine?.state);
  const remoteLaunchHint =
    detail == null || isLocalSession(detail) || remoteLaunchable
      ? undefined
      : remoteLaunchDisabledHint(remoteMachine?.state, detail.launchBlockReason);
  const remoteResumeHint =
    detail == null ? undefined : resumeDisabledHint(detail, remoteLaunchable, remoteLaunchHint);

  const refreshDetailSource = () => {
    const startingIdentity = detailAttemptIdentity;
    void refreshSourceAndRetry(
      onRefresh,
      () => setDetailRetryToken((token) => token + 1),
      () => startingIdentity != null && detailAttemptRef.current === startingIdentity,
    );
  };

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const media = window.matchMedia(DOCKED_INSPECTOR_QUERY);
    const updateModal = () => setModal(!media.matches);
    media.addEventListener('change', updateModal);
    return () => media.removeEventListener('change', updateModal);
  }, []);

  return (
    <Sheet
      modal={modal}
      open={isOpen}
      onOpenChange={(open) => {
        if (!open) {
          setSelectedDiff(null);
          setDetailRetryToken((token) => token + 1);
          onClose();
        }
      }}
    >
      <SheetContent
        ref={contentRef}
        tabIndex={-1}
        className="coslash-shell w-full! max-w-none! gap-0 outline-none sm:w-[var(--coslash-inspector-w)]!"
        style={{ '--coslash-inspector-w': inspectorWidth } as CSSProperties}
        showCloseButton={true}
        onInteractOutside={(event) => {
          if (!modal) event.preventDefault();
        }}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          contentRef.current?.focus();
        }}
      >
        <div
          role="separator"
          tabIndex={0}
          aria-orientation="vertical"
          aria-label="Resize inspector"
          {...separatorProps}
          className="hover:bg-coslash-accent focus-visible:bg-coslash-accent absolute inset-y-0 left-0 z-20 hidden w-1.5 cursor-col-resize touch-none transition-colors outline-none sm:block"
        />
        {session != null && <SheetTitle className="sr-only">{sessionDisplayTitle(session)}</SheetTitle>}
        {isOpen && detail == null && isLoading && (
          <div
            role="status"
            className="text-coslash-muted flex flex-1 items-center justify-center gap-2 text-xs"
          >
            <LoaderCircleIcon className="size-4 animate-spin" />
            Loading exact session details…
          </div>
        )}
        {isOpen && detail == null && loadError != null && loadErrorKind != null && (
          <DetailLoadError
            message={loadError}
            kind={loadErrorKind}
            onRetry={() => setDetailRetryToken((token) => token + 1)}
            onRefresh={refreshDetailSource}
          />
        )}
        {isOpen && session != null && detail != null && (
          <>
            <SheetHeader>
              <div className="flex min-w-0 flex-col gap-2">
                <SessionInspectorTitle detail={detail} showMachineBadge={showMachineBadge} />
                <HeaderMeta
                  detail={detail}
                  showMachineBadge={false}
                  synthesisCostVersion={synthesisCostVersion}
                  synthesisSettledCount={synthesisSettledCount}
                />
                {session != null && snapshotMayBeStale(detail, session) && <SnapshotStalenessNotice />}
              </div>
            </SheetHeader>
            <SnapshotRefreshStatus
              isLoading={isLoading}
              error={loadError}
              kind={loadErrorKind}
              onRetry={
                loadErrorKind === 'authentication'
                  ? undefined
                  : () => setDetailRetryToken((token) => token + 1)
              }
              onRefresh={refreshDetailSource}
            />
            {showCachedOffline && (
              <div
                role="status"
                className="text-warning-fg bg-warning-bg mx-4 mb-2 rounded-sm px-3 py-2 text-xs"
              >
                Showing the last complete cached details; the SSH workspace is not fully synced.
              </div>
            )}
            {summaryOnly && <SummaryOnlyBanner />}
            <InspectorBody
              detail={detail}
              exactDetailsAvailable={!summaryOnly}
              remoteLaunchable={remoteLaunchable}
              remoteLaunchHint={remoteLaunchHint}
              handoff={handoff}
              onHandoffStarted={onHandoffStarted}
              onOpenTarget={onOpenTarget}
              onSelectFile={
                summaryOnly
                  ? null
                  : (fileEdit, trigger) => {
                      diffTriggerRef.current = trigger;
                      setSelectedDiff({
                        sourceId: detail.sourceId,
                        agent: detail.agent,
                        sessionId: detail.id,
                        revision: detail.detailRevision,
                        path: fileEdit.path,
                        changeIds: fileEdit.changeIds ?? [],
                      });
                    }
              }
            />
            <InspectorFooter
              detail={detail}
              remoteLaunchable={remoteLaunchable}
              remoteResumeHint={remoteResumeHint}
            />
          </>
        )}
      </SheetContent>
      <Sheet
        open={filePanelOpen(selectedDiff, detail)}
        onOpenChange={(open) => {
          if (!open) setSelectedDiff(null);
        }}
      >
        <SheetContent
          className="coslash-shell w-full! max-w-none! gap-0 sm:w-1/2!"
          showCloseButton={true}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            const trigger = diffTriggerRef.current;
            (trigger?.isConnected ? trigger : contentRef.current)?.focus();
          }}
        >
          {selectedDiff != null && (
            <>
              <SheetHeader className="border-b">
                <div className="flex min-w-0 items-center gap-2 pr-10">
                  <span className="text-brand shrink-0 text-xs font-semibold tracking-wide">
                    FILE CHANGES
                  </span>
                  <SheetTitle className="min-w-0 flex-1 truncate font-mono text-xs" title={selectedDiff.path}>
                    {selectedDiff.path}
                  </SheetTitle>
                </div>
              </SheetHeader>
              <DiffList
                changes={fileChanges}
                isLoading={fileDiffLoading}
                loadError={fileDiffError}
                showRefresh={
                  fileDiffErrorKind === 'stale' ||
                  fileDiffErrorKind === 'missing' ||
                  fileDiffErrorKind === 'corrupt'
                }
                showRetry={fileDiffErrorKind === 'other'}
                onRetry={() => setFileDiffRetryToken((token) => token + 1)}
                onRefresh={() => {
                  setSelectedDiff(null);
                  refreshDetailSource();
                }}
              />
            </>
          )}
        </SheetContent>
      </Sheet>
    </Sheet>
  );
}
