import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import {
  ChevronDownIcon,
  ChevronRightIcon,
  LoaderCircleIcon,
  SquareCheckIcon,
  SquareIcon,
} from 'lucide-react';
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import { DiffList } from '@/pages/coslash/components/DiffList';
import {
  DetailLoadError,
  SnapshotRefreshStatus,
  SnapshotStalenessNotice,
  SummaryOnlyBanner,
} from '@/pages/coslash/components/session-inspector/DetailStatus';
import { DigestSection } from '@/pages/coslash/components/session-inspector/DigestSection';
import {
  HandoffSection,
  ResumeSessionButton,
} from '@/pages/coslash/components/session-inspector/HandoffSection';
import {
  HeaderMeta,
  SessionInspectorTitle,
} from '@/pages/coslash/components/session-inspector/InspectorHeader';
import { RecapSection } from '@/pages/coslash/components/session-inspector/RecapSection';
import { FieldLabel, SectionLabel } from '@/pages/coslash/components/session-inspector/SectionLabel';
import { useSessionDetail } from '@/pages/coslash/hooks/use-session-detail';
import { useFileDiff, type FileSelection } from '@/pages/coslash/hooks/use-sessions';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  getVendor,
  isLocalSession,
  remoteLaunchDisabledHint,
  resumeDisabledHint,
  sessionDisplayTitle,
  sessionKey,
  type Session,
  type SessionDetail,
} from '@/pages/coslash/lib/session';
import {
  cachedOfflineWarning,
  filePanelOpen,
  refreshSourceAndRetry,
  snapshotMayBeStale,
} from '@/pages/coslash/lib/session-detail';
import { SkillsSection } from './SkillsSection';

/** Must stay in sync with the docked-inspector breakpoint in coslash-layout.css. */
const DOCKED_INSPECTOR_QUERY = '(min-width: 1720px)';
const INSPECTOR_WIDTH_KEY = 'coslash.inspector-width.v1';
const MIN_INSPECTOR_WIDTH = 360;
const MAX_INSPECTOR_VIEWPORT_SHARE = 0.8;
const DEFAULT_INSPECTOR_WIDTH = 'clamp(420px, 32vw, 760px)';
const INSPECTOR_KEYBOARD_STEP = 16;

function maximumInspectorWidth(viewportWidth: number): number {
  return Math.max(MIN_INSPECTOR_WIDTH, Math.round(viewportWidth * MAX_INSPECTOR_VIEWPORT_SHARE));
}

function clampInspectorWidth(width: number, viewportWidth = window.innerWidth): number {
  return Math.round(Math.min(Math.max(width, MIN_INSPECTOR_WIDTH), maximumInspectorWidth(viewportWidth)));
}

function defaultInspectorWidth(viewportWidth: number): number {
  return clampInspectorWidth(Math.min(Math.max(viewportWidth * 0.32, 420), 760), viewportWidth);
}

// oxlint-disable-next-line react/only-export-components -- exported for focused keyboard tests
export function inspectorWidthForKey(
  key: string,
  currentWidth: number,
  viewportWidth: number,
): number | null {
  if (key === 'Home') return MIN_INSPECTOR_WIDTH;
  if (key === 'End') return maximumInspectorWidth(viewportWidth);
  if (key === 'ArrowLeft') {
    return clampInspectorWidth(currentWidth + INSPECTOR_KEYBOARD_STEP, viewportWidth);
  }
  if (key === 'ArrowRight') {
    return clampInspectorWidth(currentWidth - INSPECTOR_KEYBOARD_STEP, viewportWidth);
  }
  return null;
}

function readInspectorWidth(): number | null {
  try {
    const stored = Number(window.localStorage.getItem(INSPECTOR_WIDTH_KEY));
    return Number.isFinite(stored) && stored > 0 ? Math.max(stored, MIN_INSPECTOR_WIDTH) : null;
  } catch {
    return null;
  }
}

function StatCell({ value, label }: { value: string; label: string }) {
  return (
    <div className="bg-coslash-surface p-2">
      <div className="text-base font-bold">{value}</div>
      <div className="text-coslash-muted text-xs">{label}</div>
    </div>
  );
}

function ArtifactStats({ detail }: { detail: SessionDetail }) {
  const newFiles = detail.fileEdits.filter((fileEdit) => fileEdit.isNew).length;
  const cells: [string, string][] = [
    [String(detail.fileEdits.length), newFiles ? `files (${newFiles} new)` : 'files'],
    [String(detail.prs), 'PRs'],
    [String(detail.subagents.length), 'subagents'],
  ];

  return (
    <div>
      <SectionLabel title="ARTIFACTS" note="what this session produced" />
      <div className="bg-coslash-line grid grid-cols-3 gap-px overflow-hidden rounded-lg border">
        {cells.map(([value, label]) => (
          <StatCell key={label} value={value} label={label} />
        ))}
      </div>
    </div>
  );
}

function FilesChangedList({
  detail,
  onSelectFile,
}: {
  detail: SessionDetail;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number], trigger: HTMLButtonElement) => void) | null;
}) {
  if (detail.fileEdits.length === 0) return null;
  const newFiles = detail.fileEdits.filter((fileEdit) => fileEdit.isNew).length;
  return (
    <div>
      <FieldLabel>{`FILES CHANGED · ${detail.fileEdits.length} TOTAL${newFiles ? `, ${newFiles} NEW` : ''}`}</FieldLabel>
      <div className="rounded-sm border p-2">
        {detail.fileEdits.map((fileEdit) => (
          <div key={fileEdit.path} className="flex items-center justify-between gap-2 py-1 font-mono text-xs">
            {onSelectFile == null ? (
              <span
                title={fileEdit.path}
                className={cn('text-coslash-muted min-w-0 truncate text-left', {
                  'text-success-fg': fileEdit.isNew,
                })}
              >
                {fileEdit.path.split('/').pop()}
              </span>
            ) : (
              <button
                type="button"
                title={fileEdit.path}
                className={cn(
                  'text-coslash-muted min-w-0 cursor-pointer truncate text-left hover:underline',
                  {
                    'text-success-fg': fileEdit.isNew,
                  },
                )}
                onClick={(event) => onSelectFile(fileEdit, event.currentTarget)}
              >
                {fileEdit.path.split('/').pop()}
              </button>
            )}
            <span className="whitespace-nowrap">
              <span className="text-success-fg">+{fileEdit.adds}</span>{' '}
              <span className="text-danger-fg">−{fileEdit.dels}</span>{' '}
              <span className="text-coslash-muted">
                · {fileEdit.edits} {fileEdit.edits === 1 ? 'edit' : 'edits'}
              </span>
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function CommitsAndTodos({ detail }: { detail: SessionDetail }) {
  return (
    <div className="flex gap-4 pt-4">
      <div className="min-w-0 flex-1">
        <FieldLabel>COMMITS</FieldLabel>
        {detail.commits.length === 0 ? (
          <div className="text-coslash-muted text-xs">—</div>
        ) : (
          detail.commits.map((commit, index) => (
            <div key={index} className="truncate py-1 font-mono text-xs">
              {commit}
            </div>
          ))
        )}
      </div>
      <div className="min-w-0 flex-1">
        <FieldLabel>TODOS</FieldLabel>
        {detail.todos.length === 0 ? (
          <div className="text-coslash-muted text-xs">none</div>
        ) : (
          detail.todos.map((todo, index) => (
            <div
              key={index}
              className={cn('flex items-start gap-1 py-1 text-xs', {
                'text-coslash-muted': todo.done,
              })}
            >
              {todo.done ? (
                <SquareCheckIcon className="size-4 shrink-0" />
              ) : (
                <SquareIcon className="size-4 shrink-0" />
              )}
              <span className="min-w-0">{todo.text}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function CommandsSection({ detail }: { detail: SessionDetail }) {
  const [open, setOpen] = useState(false);
  if (detail.commands.length === 0) return null;

  return (
    <div className="pt-4">
      <button
        type="button"
        aria-expanded={open}
        className="flex w-full cursor-pointer items-center gap-2"
        onClick={() => setOpen(!open)}
      >
        {open ? (
          <ChevronDownIcon className="text-coslash-muted size-3" />
        ) : (
          <ChevronRightIcon className="text-coslash-muted size-3" />
        )}
        <span className="text-coslash-muted pb-1 text-xs font-semibold tracking-wide">
          {`${detail.commands.length} COMMANDS`}
        </span>
        <span className="flex-1 border-b" />
      </button>
      {open && (
        <div className="bg-coslash-soft max-h-44 overflow-x-hidden overflow-y-auto rounded-lg p-3">
          <pre className="text-coslash-ink font-mono text-xs leading-relaxed wrap-break-word whitespace-pre-wrap">
            {detail.commands.join('\n')}
          </pre>
        </div>
      )}
    </div>
  );
}

function InspectorBody({
  detail,
  exactDetailsAvailable,
  onSelectFile,
  remoteLaunchable,
  remoteLaunchHint,
  handoff,
  onHandoffStarted,
  onOpenTarget,
}: {
  detail: SessionDetail;
  exactDetailsAvailable: boolean;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number], trigger: HTMLButtonElement) => void) | null;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
  handoff?: DirectedHandoff;
  onHandoffStarted: () => void;
  onOpenTarget: (handoff: DirectedHandoff) => void;
}) {
  const currentSessionKey = sessionKey(detail);
  const [tabSelection, setTabSelection] = useState<{
    sessionKey: string;
    tab: 'session' | 'files' | 'skills';
  }>(() => ({ sessionKey: currentSessionKey, tab: 'session' }));
  if (tabSelection.sessionKey !== currentSessionKey) {
    setTabSelection({ sessionKey: currentSessionKey, tab: 'session' });
  }
  const selectedTab = tabSelection.sessionKey === currentSessionKey ? tabSelection.tab : 'session';
  const selectedTabAvailable =
    selectedTab === 'session' ||
    (selectedTab === 'files' && detail.fileEdits.length > 0) ||
    (selectedTab === 'skills' && (detail.skills?.length ?? 0) > 0);
  if (!selectedTabAvailable) setTabSelection({ sessionKey: currentSessionKey, tab: 'session' });
  const activeTab = selectedTabAvailable ? selectedTab : 'session';
  const tabClassName =
    'text-coslash-muted h-auto flex-none rounded-none px-0 py-3 data-[state=active]:text-coslash-ink data-[state=active]:after:opacity-100 group-data-horizontal/tabs:after:bottom-0';
  // scroll on the outer div, layout on the inner one — flex children of a
  // scroll container shrink to fit instead of overflowing, which collapses
  // the overflow-hidden stat grids
  return (
    <Tabs
      key={currentSessionKey}
      value={activeTab}
      onValueChange={(value) =>
        setTabSelection({ sessionKey: currentSessionKey, tab: value as 'session' | 'files' | 'skills' })
      }
      className="min-h-0 flex-1 gap-0"
    >
      <TabsList
        variant="line"
        aria-label="Session views"
        className="w-full shrink-0 justify-start gap-4 border-b px-4 py-0 group-data-horizontal/tabs:h-auto"
      >
        <TabsTrigger value="session" className={tabClassName}>
          Session
        </TabsTrigger>
        {detail.fileEdits.length > 0 && (
          <TabsTrigger value="files" className={tabClassName}>
            Files · {detail.fileEdits.length}
          </TabsTrigger>
        )}
        {(detail.skills?.length ?? 0) > 0 && (
          <TabsTrigger value="skills" className={tabClassName}>
            Skills
          </TabsTrigger>
        )}
      </TabsList>
      <div className="flex-1 overflow-x-hidden overflow-y-auto">
        <TabsContent
          value="session"
          forceMount
          className="flex flex-col gap-2 px-4 py-4 data-[state=inactive]:hidden"
        >
          <HandoffSection
            detail={detail}
            exactDetailsAvailable={exactDetailsAvailable}
            remoteLaunchable={remoteLaunchable}
            remoteLaunchHint={remoteLaunchHint}
            handoff={handoff}
            onHandoffStarted={onHandoffStarted}
            onOpenTarget={onOpenTarget}
          />
          <RecapSection detail={detail} />
          <ArtifactStats detail={detail} />
          <CommandsSection detail={detail} />
          <CommitsAndTodos detail={detail} />
          <DigestSection detail={detail} />
        </TabsContent>
        <TabsContent value="files" forceMount className="px-4 py-4 data-[state=inactive]:hidden">
          <FilesChangedList detail={detail} onSelectFile={onSelectFile} />
        </TabsContent>
        <TabsContent value="skills" forceMount className="px-4 pb-4 data-[state=inactive]:hidden">
          <SkillsSection skills={detail.skills} />
        </TabsContent>
      </div>
    </Tabs>
  );
}

function InspectorFooter({
  detail,
  remoteLaunchable,
  remoteResumeHint,
}: {
  detail: SessionDetail;
  remoteLaunchable: boolean;
  remoteResumeHint?: string;
}) {
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';

  return (
    <SheetFooter className="bg-coslash-soft flex-row items-center justify-between gap-4 border-t">
      <div className="flex min-w-0 flex-col">
        <span className="text-xs">
          {opensCursor ? 'Open workspace in Cursor' : 'Resume this exact session'}
        </span>
        <span className="text-coslash-muted text-xs font-light">
          {isLocalSession(detail)
            ? opensCursor
              ? 'Cursor does not expose an IDE deep link, so this opens the workspace without restoring this chat.'
              : `Reopens this session in ${getVendor(detail.agent).label} with its full context.`
            : detail.agent === 'pi'
              ? 'Pi CLI resume is available locally only.'
              : remoteLaunchable
                ? `Opens this session in ${getVendor(detail.agent).label} through SSH.`
                : 'Available when the remote SSH host is connected.'}
        </span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <ResumeSessionButton detail={detail} disabledHint={remoteResumeHint} />
      </div>
    </SheetFooter>
  );
}

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
  const resizeRef = useRef<{
    pointerId: number;
    startX: number;
    startWidth: number;
    currentWidth: number;
  } | null>(null);
  const [selectedDiffState, setSelectedDiff] = useState<FileSelection | null>(null);
  const diffTriggerRef = useRef<HTMLButtonElement | null>(null);
  const selectedDiff = filePanelOpen(selectedDiffState, detail) ? selectedDiffState : null;
  const [fileDiffRetryToken, setFileDiffRetryToken] = useState(0);
  const [modal, setModal] = useState(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? !window.matchMedia(DOCKED_INSPECTOR_QUERY).matches
      : true,
  );
  const [width, setWidth] = useState<number | null>(readInspectorWidth);
  const [viewportWidth, setViewportWidth] = useState(() =>
    typeof window === 'undefined' ? 1375 : window.innerWidth,
  );

  const persistWidth = (nextWidth: number) => {
    setWidth(nextWidth);
    try {
      window.localStorage.setItem(INSPECTOR_WIDTH_KEY, String(nextWidth));
    } catch {
      // a blocked store just means the width resets next session
    }
  };

  const startResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    const startWidth = clampInspectorWidth(contentRef.current?.offsetWidth ?? MIN_INSPECTOR_WIDTH);
    resizeRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startWidth,
      currentWidth: startWidth,
    };
  };
  const continueResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    const resize = resizeRef.current;
    if (resize == null || resize.pointerId !== event.pointerId) return;
    resize.currentWidth = clampInspectorWidth(resize.startWidth + resize.startX - event.clientX);
    setWidth(resize.currentWidth);
  };
  const finishResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    const resize = resizeRef.current;
    if (resize == null || resize.pointerId !== event.pointerId) return;
    resizeRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    persistWidth(resize.currentWidth);
  };
  const resizeWithKeyboard = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const currentWidth = contentRef.current?.offsetWidth ?? width ?? MIN_INSPECTOR_WIDTH;
    const nextWidth = inspectorWidthForKey(event.key, currentWidth, viewportWidth);
    if (nextWidth == null) return;
    event.preventDefault();
    persistWidth(nextWidth);
  };
  const {
    changes: fileChanges,
    isLoading: fileDiffLoading,
    loadError: fileDiffError,
    loadErrorKind: fileDiffErrorKind,
  } = useFileDiff(selectedDiff, fileDiffRetryToken);
  const isOpen = session != null;
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
    const updateViewportWidth = () => setViewportWidth(window.innerWidth);
    media.addEventListener('change', updateModal);
    window.addEventListener('resize', updateViewportWidth);
    return () => {
      media.removeEventListener('change', updateModal);
      window.removeEventListener('resize', updateViewportWidth);
    };
  }, []);

  const inspectorWidth =
    width == null ? DEFAULT_INSPECTOR_WIDTH : `min(${width}px, ${MAX_INSPECTOR_VIEWPORT_SHARE * 100}vw)`;
  const ariaWidth = clampInspectorWidth(width ?? defaultInspectorWidth(viewportWidth), viewportWidth);
  const ariaMaximumWidth = maximumInspectorWidth(viewportWidth);

  useEffect(() => {
    if (!isOpen) return;
    const root = document.documentElement;
    root.style.setProperty('--coslash-inspector-width', inspectorWidth);
    return () => {
      root.style.removeProperty('--coslash-inspector-width');
    };
  }, [isOpen, inspectorWidth]);
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
          aria-valuemin={MIN_INSPECTOR_WIDTH}
          aria-valuemax={ariaMaximumWidth}
          aria-valuenow={ariaWidth}
          onPointerDown={startResize}
          onPointerMove={continueResize}
          onPointerUp={finishResize}
          onPointerCancel={finishResize}
          onLostPointerCapture={finishResize}
          onKeyDown={resizeWithKeyboard}
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
