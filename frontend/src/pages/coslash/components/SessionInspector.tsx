import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import {
  CheckIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  ExternalLinkIcon,
  EyeIcon,
  InfoIcon,
  LoaderCircleIcon,
  PlayIcon,
  SquareCheckIcon,
  SquareIcon,
  TerminalIcon,
  XIcon,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogTrigger } from '@/components/ui/dialog';
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { CopyableBadge } from '@/pages/coslash/components/CopyableBadge';
import { DiffList } from '@/pages/coslash/components/DiffList';
import { MachineBadge } from '@/pages/coslash/components/MachineBadge';
import { ReviewDialog } from '@/pages/coslash/components/ReviewDialog';
import {
  SessionId,
  SessionName,
  SessionVendorBadge,
  SubagentDialogContent,
  SubagentModelBadge,
  TokenBreakdown,
} from '@/pages/coslash/components/SessionCard';
import { SnapshotPreviewDialog } from '@/pages/coslash/components/SnapshotPreviewDialog';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { useLaunchTerminal } from '@/pages/coslash/hooks/use-launch-terminal';
import {
  decodeSession,
  sessionDetailRequestPath,
  synthesisRequestPath,
  useFileDiff,
  type ExactReadErrorKind,
  type FileSelection,
} from '@/pages/coslash/hooks/use-sessions';
import { ApiAuthenticationError, apiFetch } from '@/pages/coslash/lib/api';
import {
  blocksFromTexts,
  collapseDebriefBlocks,
  parseDebriefText,
  type DebriefBlock,
} from '@/pages/coslash/lib/debrief-text';
import {
  digestDateKey,
  formatDigestDateDivider,
  formatDigestDateRange,
  formatDigestTime,
  formatDuration,
  formatEstimatedCost,
  formatTimeAgo,
  formatTokens,
} from '@/pages/coslash/lib/format';
import { copyHandoffText, cursorHandoffText, handoffBrief } from '@/pages/coslash/lib/handoff';
import { type MachineFact } from '@/pages/coslash/lib/machines';
import { teamPreviewEnabled } from '@/pages/coslash/lib/preview';
import { isReviewSessionName, reviewActionVisible, type ReviewerOption } from '@/pages/coslash/lib/review';
import {
  boardStatusKey,
  canResumeSession,
  displayStatusLabel,
  freshLaunchDisabledHint,
  getModality,
  getSessionOutcome,
  getVendor,
  goalSourceLabel,
  isLocalSession,
  remoteLaunchDisabledHint,
  resolveGoal,
  resumeDisabled,
  resumeDisabledHint,
  sessionKey,
  sessionLocationFact,
  sessionReadiness,
  STATUSES,
  SUBAGENT_STATUSES,
  subagentParentName,
  type DigestEntry,
  type Session,
  type SessionDetail,
  type SessionIdentity,
} from '@/pages/coslash/lib/session';
import { HOUR, MINUTE, promptCacheTiming } from '@/pages/coslash/lib/time';

type SynthesisResponse = {
  synthesis: SessionDetail['synthesis'];
  synthesisPending: boolean;
  synthesisError?: string;
  revision: number;
};

type DetailResponse = {
  sourceId: string;
  agent: string;
  sessionId: string;
  revision: string;
  synthesisRevision?: number;
  cachedOffline: boolean;
  session: Partial<Session>;
};

type DetailErrorKind = Exclude<ExactReadErrorKind, 'too_large'> | 'authentication';

type DetailError = {
  key: string;
  retryToken: number;
  kind: DetailErrorKind;
  message: string;
};

function needsSourceRefresh(kind: DetailErrorKind): boolean {
  return kind === 'stale' || kind === 'missing' || kind === 'corrupt';
}

/* oxlint-disable react/only-export-components -- exported for focused rendering tests */
export function filePanelOpen(
  selection: FileSelection | null,
  session: (SessionIdentity & Pick<Session, 'detailRevision'>) | null,
): boolean {
  return (
    selection != null &&
    session != null &&
    selection.sourceId === session.sourceId &&
    selection.agent === session.agent &&
    selection.sessionId === session.id &&
    selection.revision === session.detailRevision
  );
}

export function detailRequestKey(session: Session): string {
  return `${sessionKey(session)}@${session.detailRevision === '' ? 'summary' : 'full'}`;
}

export function synthesisAttemptKey(session: Session): string {
  return `${detailRequestKey(session)}@${session.detailRevision}`;
}

export function synthesisMatchesSnapshot(
  result: Pick<SynthesisResponse, 'revision'>,
  synthesisRevision: number,
): boolean {
  return synthesisRevision > 0 && result.revision === synthesisRevision;
}

export function refreshSourceAndRetry(
  refresh: () => void | Promise<void>,
  retry: () => void,
  isCurrent: () => boolean,
): Promise<void> {
  return Promise.resolve(refresh()).then(() => {
    if (isCurrent()) retry();
  });
}

export function detailAttemptState(
  loaded: { key: string; retryToken: number } | null,
  error: DetailError | null,
  key: string,
  retryToken: number,
) {
  const currentError = error?.key === key && error.retryToken === retryToken ? error : null;
  const hasSnapshot = loaded?.key === key && currentError?.kind !== 'authentication';
  return {
    hasSnapshot,
    isLoading: currentError == null && (!hasSnapshot || loaded.retryToken !== retryToken),
    error: currentError,
  };
}

export function snapshotMayBeStale(detail: SessionDetail, current: Session): boolean {
  return current.status != null || detail.detailRevision !== current.detailRevision;
}

export function SnapshotStalenessNotice() {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            role="status"
            tabIndex={0}
            aria-label="Session data may be stale"
            className="text-warning-fg inline-flex w-fit items-center gap-1 text-xs"
          >
            <InfoIcon className="size-3" aria-hidden="true" />
            Snapshot may be stale
          </span>
        </TooltipTrigger>
        <TooltipContent>
          Activity recorded after this snapshot is not shown. Close and reopen to inspect newer details.
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function SnapshotRefreshStatus({
  isLoading,
  error,
  kind,
  onRetry,
  onRefresh,
}: {
  isLoading: boolean;
  error: string | null;
  kind?: DetailErrorKind | null;
  onRetry?: () => void;
  onRefresh?: () => void;
}) {
  if (error != null) {
    return (
      <div
        role="alert"
        className="text-destructive flex flex-wrap items-center justify-between gap-2 px-4 py-2 text-xs"
      >
        <span>Snapshot refresh failed: {error}</span>
        {kind != null && needsSourceRefresh(kind) && onRefresh != null ? (
          <Button variant="outline" size="sm" onClick={onRefresh}>
            Refresh sessions
          </Button>
        ) : onRetry != null && kind !== 'authentication' ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            Retry details
          </Button>
        ) : null}
      </div>
    );
  }
  return isLoading ? (
    <div role="status" className="text-muted-foreground px-4 py-2 text-xs">
      Refreshing snapshot…
    </div>
  ) : null;
}

export function detailPresentation(session: Session | null): {
  detail: SessionDetail | null;
  summaryOnly: boolean;
} {
  if (session == null) return { detail: null, summaryOnly: false };
  return session.detailRevision === ''
    ? { detail: session, summaryOnly: true }
    : { detail: null, summaryOnly: false };
}

export function cachedOfflineWarning(
  cachedOffline: boolean,
  state: MachineFact['state'] | undefined,
): boolean {
  return state == null ? cachedOffline : state !== 'ok' && state !== 'limited';
}

export function overlayLiveSessionFields(detail: SessionDetail, current: Session): SessionDetail {
  if (detail.detailRevision !== current.detailRevision && current.mtime <= detail.mtime) return detail;
  const currentSubagents = new Map(current.subagents.map((subagent) => [subagent.id, subagent]));
  return {
    ...detail,
    status: current.status,
    branch: current.branch,
    repo: current.repo,
    repoLocalOnly: current.repoLocalOnly,
    reviewPending: current.reviewPending,
    reviewError: current.reviewError,
    completion: current.completion,
    privacy: current.privacy,
    shareEligibility: current.shareEligibility,
    eligibleForAggregates: current.eligibleForAggregates,
    displayStale: current.displayStale,
    lastSeenStatus: current.lastSeenStatus,
    launchable: current.launchable,
    launchBlockReason: current.launchBlockReason,
    ...(isLocalSession(current)
      ? { mtime: current.mtime, commits: current.commits, git: current.git, lastEditAt: current.lastEditAt }
      : {}),
    subagents: detail.subagents.map((subagent) => {
      const live = currentSubagents.get(subagent.id);
      return live == null ? subagent : { ...subagent, status: live.status };
    }),
  };
}

export function SummaryOnlyBanner() {
  return (
    <div role="status" className="text-warning-fg bg-warning-bg mx-4 mb-2 rounded-sm px-3 py-2 text-xs">
      Complete details are unavailable. Showing the bounded summary from the session library; exact file diffs
      are disabled.
    </div>
  );
}

export function DetailLoadError({
  message,
  kind,
  onRetry,
  onRefresh,
}: {
  message: string;
  kind: DetailErrorKind;
  onRetry: () => void;
  onRefresh: () => void;
}) {
  const refreshSessions = needsSourceRefresh(kind);
  return (
    <div role="alert" className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
      <div className="text-danger-fg text-sm">{message}</div>
      {refreshSessions && (
        <Button variant="outline" size="sm" onClick={onRefresh}>
          Refresh sessions
        </Button>
      )}
      {kind === 'other' && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          Retry details
        </Button>
      )}
    </div>
  );
}
/* oxlint-enable react/only-export-components */

function useSessionDetail(
  session: Session | null,
  detailRetryToken: number,
  sessionsVersion: number,
  synthesisSettingsKey: string,
): {
  detail: SessionDetail | null;
  isLoading: boolean;
  loadError: string | null;
  loadErrorKind: DetailErrorKind | null;
  cachedOffline: boolean;
  summaryOnly: boolean;
} {
  const [loadedDetail, setLoadedDetail] = useState<{
    key: string;
    retryToken: number;
    detail: SessionDetail;
    synthesisRevision: number;
    cachedOffline: boolean;
  } | null>(null);
  const [detailError, setDetailError] = useState<DetailError | null>(null);
  const [loadedSynthesis, setLoadedSynthesis] = useState<({ key: string } & SynthesisResponse) | null>(null);
  const pollDeadline = useRef<{ key: string; deadline: number } | null>(null);
  const detailKey = session == null ? null : detailRequestKey(session);
  const snapshot = loadedDetail?.key === detailKey ? loadedDetail.detail : null;
  const synthesisRevision = snapshot == null ? 0 : (loadedDetail?.synthesisRevision ?? 0);
  const synthesisKey = snapshot == null ? null : synthesisAttemptKey(snapshot);
  const sessionRef = useRef(session);

  useEffect(() => {
    sessionRef.current = session;
  }, [session]);

  useEffect(() => {
    const current = sessionRef.current;
    if (current == null || detailKey == null) return;
    if (current.detailRevision === '') return;

    const controller = new AbortController();
    const load = async () => {
      try {
        const response = await apiFetch(sessionDetailRequestPath(current, 'latest'), {
          signal: controller.signal,
        });
        if (!response.ok) {
          let code = '';
          try {
            code = ((await response.json()) as { code?: string }).code ?? '';
          } catch {
            // The status remains enough to show an honest generic state.
          }
          const failure: Omit<DetailError, 'key' | 'retryToken'> =
            code === 'session_detail_stale'
              ? {
                  kind: 'stale',
                  message:
                    'This session changed before its details loaded. Refresh sessions to inspect the latest revision.',
                }
              : code === 'session_detail_missing'
                ? { kind: 'missing', message: 'Complete details for this session are unavailable.' }
                : code === 'session_detail_corrupt'
                  ? {
                      kind: 'corrupt',
                      message:
                        'Cached session details could not be read. Refresh the source to restore a last-good copy.',
                    }
                  : { kind: 'other', message: `Could not load session details (${response.status}).` };
          throw failure;
        }
        const body = (await response.json()) as DetailResponse;
        if (
          body.sourceId !== current.sourceId ||
          body.agent !== current.agent ||
          body.sessionId !== current.id ||
          typeof body.revision !== 'string' ||
          body.revision === '' ||
          body.session == null ||
          typeof body.session !== 'object'
        ) {
          throw {
            kind: 'corrupt',
            message: 'Session details did not match the selected revision.',
          } satisfies Omit<DetailError, 'key' | 'retryToken'>;
        }
        const detail = decodeSession({
          ...current,
          ...body.session,
          repo: body.session.repo ?? current.repo,
          sourceId: current.sourceId,
          sourceLabel: current.sourceLabel,
          sourceClass: current.sourceClass,
          logicalSessionId: current.logicalSessionId,
          revision: current.revision,
          detailRevision: body.revision,
          fullRevision: isLocalSession(current) ? current.fullRevision : body.revision,
          eligibleForAggregates: current.eligibleForAggregates,
          displayStale: current.displayStale,
          launchable: current.launchable,
          launchBlockReason: current.launchBlockReason,
        });
        if (!controller.signal.aborted) {
          const loaded = {
            key: detailKey,
            retryToken: detailRetryToken,
            detail,
            synthesisRevision: body.synthesisRevision ?? 0,
            cachedOffline: body.cachedOffline,
          };
          setLoadedDetail(loaded);
          setDetailError(null);
        }
      } catch (error: unknown) {
        if (controller.signal.aborted) return;
        if (error instanceof ApiAuthenticationError) {
          setLoadedDetail(null);
          setLoadedSynthesis(null);
          setDetailError({
            key: detailKey,
            retryToken: detailRetryToken,
            kind: 'authentication',
            message: error.message,
          });
          return;
        }
        const failure = error as Partial<Omit<DetailError, 'key' | 'retryToken'>>;
        setDetailError({
          key: detailKey,
          retryToken: detailRetryToken,
          kind: failure.kind ?? 'other',
          message: failure.message ?? 'Could not load session details.',
        });
      }
    };
    void load();
    return () => controller.abort();
  }, [detailKey, detailRetryToken]);

  useEffect(() => {
    const current = session;
    if (current == null || synthesisKey == null || snapshot == null) {
      pollDeadline.current = null;
      return;
    }
    if (!isLocalSession(current)) {
      pollDeadline.current = null;
      return;
    }
    if (pollDeadline.current?.key !== synthesisKey) {
      pollDeadline.current = null;
    }
    const identity = {
      sourceId: current.sourceId,
      agent: current.agent,
      id: current.id,
    };
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      if (controller.signal.aborted) return;
      try {
        const res = await apiFetch(synthesisRequestPath(identity), {
          signal: controller.signal,
        });
        if (!res.ok) return;
        const result = (await res.json()) as SynthesisResponse;
        if (controller.signal.aborted) return;
        if (!synthesisMatchesSnapshot(result, synthesisRevision)) return;
        if (result.synthesis == null && result.synthesisPending) {
          pollDeadline.current ??= { key: synthesisKey, deadline: Date.now() + 2 * MINUTE };
          if (Date.now() < pollDeadline.current.deadline) {
            timer = setTimeout(load, 3_000);
            setLoadedSynthesis({ key: synthesisKey, ...result });
          } else {
            setLoadedSynthesis({ key: synthesisKey, ...result, synthesisPending: false });
          }
        } else {
          pollDeadline.current = null;
          setLoadedSynthesis({ key: synthesisKey, ...result });
        }
      } catch (error: unknown) {
        // Detail loading owns the visible request error. Synthesis failures
        // remain represented by the endpoint's synthesisError field.
        if (!controller.signal.aborted && error instanceof ApiAuthenticationError) return;
      }
    };
    void load();
    return () => {
      controller.abort();
      if (timer != null) clearTimeout(timer);
    };
  }, [synthesisKey, snapshot, synthesisRevision, session, sessionsVersion, synthesisSettingsKey]);

  const presentation = detailPresentation(session);
  if (session == null || detailKey == null) {
    return {
      detail: null,
      isLoading: false,
      loadError: null,
      loadErrorKind: null,
      cachedOffline: false,
      summaryOnly: false,
    };
  }
  if (presentation.summaryOnly) {
    return {
      detail: presentation.detail,
      isLoading: false,
      loadError: null,
      loadErrorKind: null,
      cachedOffline: false,
      summaryOnly: true,
    };
  }
  const attempt = detailAttemptState(loadedDetail, detailError, detailKey, detailRetryToken);
  if (!attempt.hasSnapshot || loadedDetail == null) {
    return {
      detail: null,
      isLoading: attempt.isLoading,
      loadError: attempt.error?.message ?? null,
      loadErrorKind: attempt.error?.kind ?? null,
      cachedOffline: false,
      summaryOnly: false,
    };
  }
  const synthesis =
    loadedSynthesis?.key === synthesisKey && synthesisMatchesSnapshot(loadedSynthesis, synthesisRevision)
      ? loadedSynthesis
      : null;
  const detail = overlayLiveSessionFields(loadedDetail.detail, session);
  return {
    detail:
      synthesis == null
        ? detail
        : {
            ...detail,
            synthesis: synthesis.synthesis,
            synthesisPending: synthesis.synthesisPending,
            synthesisError: synthesis.synthesisError,
          },
    isLoading: attempt.isLoading,
    loadError: attempt.error?.message ?? null,
    loadErrorKind: attempt.error?.kind ?? null,
    cachedOffline: loadedDetail.cachedOffline,
    summaryOnly: false,
  };
}

function contextFillReadiness(detail: SessionDetail): { value: string; tone: string } | null {
  if (detail.contextTokens == null) return null;
  if (detail.contextWindow == null) {
    return {
      value: `${formatTokens(detail.contextTokens)} used - window not available`,
      tone: 'text-coslash-muted',
    };
  }
  const pct = Math.round((detail.contextTokens / detail.contextWindow) * 100);
  return { value: `${pct}% of ${formatTokens(detail.contextWindow)}`, tone: fillTone(pct) };
}

function branchDriftReadiness(git: SessionDetail['git']): { value: string; tone: string } | null {
  if (git == null) return null;
  return {
    value: `${git.ahead} ahead, ${git.behind} behind ${git.baseBranch}.`,
    tone: driftTone(git.behind),
  };
}

function treeStaleReadiness(lastEditAt: number | null): { value: string; tone: string } | null {
  if (lastEditAt == null) return null;
  return { value: `Last edit ${formatTimeAgo(lastEditAt)}.`, tone: staleTone(Date.now() - lastEditAt) };
}

function fillTone(pct: number): string {
  if (pct >= 85) return 'text-danger-fg';
  if (pct >= 65) return 'text-warning-fg';
  return 'text-success-fg';
}

function driftTone(behind: number): string {
  if (behind > 15) return 'text-danger-fg';
  if (behind > 5) return 'text-warning-fg';
  return 'text-success-fg';
}

function staleTone(ageMs: number): string {
  if (ageMs > 72 * HOUR) return 'text-danger-fg';
  if (ageMs > 3 * HOUR) return 'text-warning-fg';
  return 'text-success-fg';
}

function SectionLabel({ title, note }: { title: string; note?: string }) {
  return (
    <div className="flex items-baseline gap-2 pb-2">
      <span className="text-brand text-xs font-bold tracking-widest">{title}</span>
      {note && <span className="text-coslash-muted text-xs">{note}</span>}
    </div>
  );
}

function FieldLabel({ children }: { children: string }) {
  return <div className="text-coslash-muted pb-1 text-xs font-semibold tracking-wide">{children}</div>;
}

export function SessionModelUsage({
  agent,
  model,
  observedModels,
  tokens,
}: Pick<SessionDetail, 'agent' | 'model' | 'observedModels' | 'tokens'>) {
  const vendor = getVendor(agent);
  if (model == null) return <span className={cn('font-bold', vendor.fg)}>unknown model</span>;

  const otherModels = [...new Set([...(observedModels ?? []), ...Object.keys(tokens)])].filter(
    (tokenModel) => tokenModel !== model,
  );
  if (otherModels.length === 0) return <span className={cn('font-bold', vendor.fg)}>{model}</span>;

  return (
    <span className="inline-flex items-center gap-1">
      <span className="inline-flex items-center gap-1">
        {otherModels.map((otherModel) => (
          <span key={otherModel} aria-hidden="true" className="bg-coslash-neutral-dot size-2 rounded-full" />
        ))}
        <span aria-hidden="true" className={cn('size-2 rounded-full bg-current', vendor.fg)} />
      </span>
      <span className={cn('font-bold', vendor.fg)}>{model}</span>
      <span className="text-coslash-muted">
        {otherModels.length === 1 ? `after ${otherModels[0]}` : `with ${otherModels.length} other models`}
      </span>
    </span>
  );
}

function HeaderMeta({ detail, showMachineBadge }: { detail: SessionDetail; showMachineBadge: boolean }) {
  const status = STATUSES[boardStatusKey(detail)];

  return (
    <div className="flex flex-col gap-2 pt-2">
      <div className="text-coslash-muted flex flex-wrap items-center justify-between gap-x-2 gap-y-1 font-mono text-xs">
        <div className="flex min-w-40 flex-1 items-center gap-1 overflow-hidden">
          <Badge variant="secondary">{sessionLocationFact(detail)}</Badge>
          <span>/</span>
          {detail.branch == null || detail.branch.trim() === '' ? (
            <Badge variant="secondary">—</Badge>
          ) : (
            <CopyableBadge
              value={detail.branch}
              ariaLabel={`Copy branch ${detail.branch}`}
              copiedLabel="Branch name copied"
              className="shrink"
            >
              <span className="truncate">{detail.branch}</span>
            </CopyableBadge>
          )}
          {showMachineBadge && <MachineBadge label={detail.sourceLabel} />}
        </div>
        <span
          className={cn(
            'flex shrink-0 items-center gap-1 font-sans font-semibold whitespace-nowrap',
            status.fg,
          )}
        >
          {!detail.displayStale && <span className={cn('size-2 rounded-full', status.dot)} />}
          {displayStatusLabel(detail)} · {getModality(detail.entrypoint)}
        </span>
      </div>
      <div className="bg-coslash-soft rounded-lg border p-2 font-mono text-xs">
        <div className="flex flex-wrap items-baseline justify-between gap-1">
          <SessionModelUsage
            agent={detail.agent}
            model={detail.model}
            observedModels={detail.observedModels}
            tokens={detail.tokens}
          />
          <span className="font-bold">
            <UnpricedModelWarning unpriced={detail.unpricedModels}>
              {formatEstimatedCost(detail.cost)}
            </UnpricedModelWarning>
          </span>
        </div>
        <div className="text-coslash-muted pt-1">
          {formatDuration(detail.durationMs)} · {detail.turns} turns · {detail.toolUses} tools ·{' '}
          {detail.errors} errors
        </div>
        <TokenBreakdown tokens={detail.tokens} />
      </div>
    </div>
  );
}

export function SessionInspectorTitle({
  detail,
  showMachineBadge,
}: {
  detail: Pick<SessionDetail, 'agent' | 'id' | 'name' | 'sourceLabel'>;
  showMachineBadge: boolean;
}) {
  return (
    <div className="flex items-center gap-2 pr-10">
      <SessionVendorBadge agent={detail.agent} />
      <div className="min-w-0 flex-1">
        <SessionName name={detail.name} />
      </div>
      <SessionId id={detail.id} shortened />
      {showMachineBadge && <MachineBadge label={detail.sourceLabel} />}
    </div>
  );
}

function ReadinessCell({ label, value, tone }: { label: string; value?: string; tone?: string }) {
  return (
    <div className="bg-coslash-surface min-w-0 p-2">
      <div className="text-coslash-muted text-xs wrap-break-word">{label}</div>
      <div className={cn('pt-1 text-xs font-semibold wrap-break-word', tone)}>{value ?? '—'}</div>
    </div>
  );
}

function CacheWindowMark({ within, label }: { within: boolean; label: string }) {
  return (
    <span
      className={cn('flex items-center gap-1 text-xs', within ? 'text-success-fg' : 'text-coslash-muted')}
    >
      {within ? <CheckIcon className="size-3 shrink-0" /> : <XIcon className="size-3 shrink-0" />}
      {label}
    </span>
  );
}

// Cache TTL refreshes on every request, so warmth keys off the transcript's
// last write: within 5 min both windows hold, within 1 hr only the 1-hr one.
function PromptCacheCell({ lastAccessAt, className }: { lastAccessAt: number; className?: string }) {
  const [now, setNow] = useState(Date.now);
  const { within5m, within1h, nextRefreshAt } = promptCacheTiming(lastAccessAt, now);

  useEffect(() => {
    if (nextRefreshAt == null) return;
    const timeout = globalThis.setTimeout(() => setNow(Date.now()), Math.max(0, nextRefreshAt - Date.now()));
    return () => globalThis.clearTimeout(timeout);
  }, [nextRefreshAt]);

  return (
    <div className={cn('bg-coslash-surface p-2', className)}>
      <div className="text-coslash-muted text-xs">Prompt cache</div>
      <div className="flex flex-wrap items-baseline gap-1 pt-1">
        <span className={cn('text-xs font-semibold', within1h ? 'text-success-fg' : 'text-warning-fg')}>
          {within1h ? 'warm' : 'cold'}
        </span>
        <span className="text-coslash-muted text-xs">{formatTimeAgo(lastAccessAt)}</span>
      </div>
      <div className="flex flex-wrap gap-2 pt-1">
        <CacheWindowMark within={within5m} label="5 min" />
        <CacheWindowMark within={within1h} label="1 hr" />
      </div>
    </div>
  );
}

/* Brand-filled CTA. Replaces the Button default variant's fill, text, and hover as
   one set: overriding only the fill leaves the variant's hover:bg-primary in place,
   which drops the button to the shadcn neutral on hover. Hover mixes toward
   --foreground so it gains contrast against the surface in either theme. */
const brandCta =
  'bg-brand text-brand-foreground hover:bg-[color-mix(in_oklch,var(--brand),var(--foreground)_10%)]';

function LaunchError({ message }: { message: string | null }) {
  if (message == null) return null;
  return <span className="text-danger-fg text-xs">{message}</span>;
}

function DisabledLaunchTooltip({ hint, children }: { hint?: string; children: ReactNode }) {
  if (hint == null) return children;
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span tabIndex={0}>{children}</span>
        </TooltipTrigger>
        <TooltipContent className="coslash-shell">{hint}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

function ResumeSessionButton({ detail, disabledHint }: { detail: SessionDetail; disabledHint?: string }) {
  const { launch, launchError } = useLaunchTerminal(detail);
  const disabled = resumeDisabled(detail, disabledHint);
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';
  const recommended = canResumeSession(detail) && sessionReadiness(detail).key === 'resume';

  return (
    <div className="flex flex-col gap-1">
      <DisabledLaunchTooltip hint={disabledHint}>
        <Button
          variant={recommended ? 'default' : 'outline'}
          className={cn('w-fit p-2 text-xs', { [brandCta]: recommended })}
          onClick={() => launch(opensCursor ? 'open' : 'resume')}
          disabled={disabled}
        >
          {opensCursor ? <ExternalLinkIcon /> : <PlayIcon />}
          <span>{opensCursor ? 'Open Cursor' : 'Resume'}</span>
        </Button>
      </DisabledLaunchTooltip>
      <LaunchError message={launchError} />
    </div>
  );
}

function StartNewSessionButton({
  detail,
  brief,
  onCopy,
  disabledHint,
}: {
  detail: SessionDetail;
  brief: string;
  onCopy: (text?: string) => Promise<boolean>;
  disabledHint?: string;
}) {
  const { launch, launchError } = useLaunchTerminal(detail);
  const cursorHint = freshLaunchDisabledHint(detail);
  const effectiveHint = cursorHint ?? disabledHint;
  const disabled =
    effectiveHint != null ||
    (!isLocalSession(detail) && (detail.displayStale || detail.launchable === false));
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';
  const requiresClipboard = isLocalSession(detail) && detail.agent === 'cursor';
  const recommended = sessionReadiness(detail).key === 'fresh';

  const startNewSession = async () => {
    const copied = await onCopy(requiresClipboard ? cursorHandoffText(brief) : brief);
    if (requiresClipboard && !copied) return;
    launch(opensCursor ? 'open' : 'new', brief);
  };

  return (
    <div className="flex flex-col gap-1">
      <DisabledLaunchTooltip hint={effectiveHint}>
        <Button
          variant={recommended ? 'default' : 'outline'}
          className={cn('w-fit p-2 text-xs', { [brandCta]: recommended })}
          onClick={() => void startNewSession()}
          disabled={disabled}
        >
          {opensCursor ? <ExternalLinkIcon /> : <TerminalIcon />}
          <span>{opensCursor ? 'Open Cursor with handoff' : 'Start fresh with handoff'}</span>
        </Button>
      </DisabledLaunchTooltip>
      <LaunchError message={launchError} />
    </div>
  );
}

function HandoffSection({
  detail,
  exactDetailsAvailable,
  remoteLaunchable,
  remoteLaunchHint,
}: {
  detail: SessionDetail;
  exactDetailsAvailable: boolean;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<string | null>(null);
  const contextFill = contextFillReadiness(detail);
  const branchDrift = branchDriftReadiness(detail.git);
  const treeStale = treeStaleReadiness(detail.lastEditAt);

  const brief = handoffBrief(detail);

  const copyBrief = async (text = brief) => {
    setCopyError(null);
    try {
      await copyHandoffText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
      return true;
    } catch {
      setCopied(false);
      setCopyError('Could not copy the handoff. Allow clipboard access and try again.');
      return false;
    }
  };

  return (
    <div className="@container flex flex-col gap-2">
      <SectionLabel title="RESUME OR HAND OFF" />
      <div className="bg-coslash-line grid grid-cols-3 gap-px overflow-hidden rounded-lg border @[560px]:grid-cols-5">
        <ReadinessCell label="Context used" value={contextFill?.value} tone={contextFill?.tone} />
        <ReadinessCell label="Compactions" value={String(detail.compactions)} />
        <ReadinessCell label="Branch" value={branchDrift?.value} tone={branchDrift?.tone} />
        <ReadinessCell label="Working tree" value={treeStale?.value} tone={treeStale?.tone} />
        <PromptCacheCell lastAccessAt={detail.mtime} className="col-span-2 @[560px]:col-span-1" />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <StartNewSessionButton
          detail={detail}
          brief={brief}
          onCopy={copyBrief}
          disabledHint={!isLocalSession(detail) && !remoteLaunchable ? remoteLaunchHint : undefined}
        />
        <Button variant="outline" className="w-fit p-2 text-xs" onClick={() => void copyBrief()}>
          <span>Copy handoff</span>
        </Button>
        {copied && <span className="text-coslash-muted text-xs">copied to clipboard</span>}
        {copyError && (
          <span role="alert" className="text-danger-fg text-xs">
            {copyError}
          </span>
        )}
      </div>
      {!isLocalSession(detail) && (
        <div className="text-coslash-muted text-xs">
          {!exactDetailsAvailable
            ? 'This inspector uses the bounded session-library summary. Complete commands and exact file diffs are unavailable for this session.'
            : remoteLaunchable
              ? 'Remote terminal actions open through SSH. Exact cached details, commands, and file diffs stay available locally; synthesis, preview, and Hub sharing remain local-only.'
              : 'Remote terminal actions are available when SSH reconnects. Exact cached details, commands, and file diffs stay available locally; synthesis, preview, and Hub sharing remain local-only.'}
        </div>
      )}
    </div>
  );
}

function RecapSection({ detail }: { detail: SessionDetail }) {
  const goal = resolveGoal(detail);
  const synthesizing = detail.synthesis == null && detail.synthesisPending;
  const synthesisPlaceholder = (
    <div className="text-coslash-muted flex items-center gap-1 pt-1 text-xs">
      <LoaderCircleIcon className="size-3 animate-spin" />
      <span>Synthesizing…</span>
    </div>
  );
  const outcome = getSessionOutcome(detail);

  return (
    <div>
      <SectionLabel title="DEBRIEF" />
      <div className="rounded-lg border p-3">
        {detail.synthesisError && (
          <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg p-2 text-xs">
            {detail.synthesisError} Using the transcript-derived debrief instead.
          </div>
        )}
        <div className={cn('flex items-center gap-2', { 'pt-3': detail.synthesisError != null })}>
          <span className="text-coslash-muted text-xs">GOAL</span>
          <Badge variant="secondary" className="text-xs">
            {goalSourceLabel(goal.source)}
          </Badge>
        </div>
        <div className="pt-2">
          <DebriefProse key={`${sessionKey(detail)}:goal`} blocks={blocksFromTexts(goal.texts)} tone="goal" />
        </div>
        <div className="border-b pt-3" />
        <div className="text-coslash-muted pt-3 text-xs">OUTCOME</div>
        {synthesizing ? (
          synthesisPlaceholder
        ) : (
          <div className="pt-1">
            <DebriefProse
              key={`${sessionKey(detail)}:outcome`}
              blocks={outcome ? parseDebriefText(outcome) : []}
              empty="—"
              tone="outcome"
            />
          </div>
        )}
        <div className="text-coslash-muted pt-3 text-xs">KEY DECISIONS</div>
        {synthesizing ? (
          synthesisPlaceholder
        ) : detail.synthesis?.keyDecisions.length ? (
          <div className="flex flex-col gap-1 pt-1">
            {detail.synthesis.keyDecisions.map((decision) => (
              <div key={decision} className="flex items-start gap-2 text-xs">
                <span className="bg-coslash-muted mt-1 size-1 shrink-0 rounded-full" />
                <span>{decision}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="pt-1 text-xs">—</div>
        )}
      </div>
    </div>
  );
}

const DEBRIEF_PREVIEW_UNITS = 3;
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

function DebriefProse({
  blocks,
  empty = '—',
  tone,
}: {
  blocks: DebriefBlock[];
  empty?: string;
  tone: 'goal' | 'outcome';
}) {
  const [expanded, setExpanded] = useState(false);
  if (blocks.length === 0) return <div className="text-xs">{empty}</div>;

  const preview = collapseDebriefBlocks(blocks, DEBRIEF_PREVIEW_UNITS);
  const canExpand = preview.truncated;
  const shown = expanded ? blocks : preview.blocks;

  return (
    <div className="flex flex-col gap-2">
      <div
        key={expanded ? 'full' : 'preview'}
        className={cn('border-coslash-line border-l-2 pl-3', {
          'border-l-brand': tone === 'goal',
          'border-l-recap': tone === 'outcome',
        })}
      >
        <DebriefBlocks blocks={shown} tone={tone} />
      </div>
      {canExpand && (
        <div
          className="text-brand flex w-fit cursor-pointer items-center gap-1 text-xs select-none"
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
          <span>
            {expanded
              ? 'show less'
              : preview.hiddenCount > 0
                ? `show full · ${preview.hiddenCount} more`
                : 'show full'}
          </span>
        </div>
      )}
    </div>
  );
}

function DebriefBlocks({ blocks, tone }: { blocks: DebriefBlock[]; tone: 'goal' | 'outcome' }) {
  return (
    <div className="flex flex-col gap-2">
      {blocks.map((block, index) => {
        if (block.kind === 'heading') {
          return (
            <div key={`h-${index}`} className="text-xs font-semibold tracking-wide">
              {block.text}
            </div>
          );
        }
        if (block.kind === 'list') {
          if (block.ordered) {
            return (
              <ol
                key={`l-${index}`}
                className={cn('list-decimal space-y-1.5 pl-4 text-xs', {
                  italic: tone === 'goal',
                })}
              >
                {block.items.map((item, itemIndex) => (
                  <li key={`${index}-${itemIndex}`} className="leading-relaxed">
                    {item}
                  </li>
                ))}
              </ol>
            );
          }
          return (
            <ul
              key={`l-${index}`}
              className={cn('flex list-none flex-col gap-1.5 text-xs', {
                italic: tone === 'goal',
              })}
            >
              {block.items.map((item, itemIndex) => (
                <li key={`${index}-${itemIndex}`} className="flex items-start gap-2">
                  <span className="bg-coslash-muted mt-1.5 size-1 shrink-0 rounded-full" />
                  <span className="min-w-0 flex-1 leading-relaxed">{item}</span>
                </li>
              ))}
            </ul>
          );
        }
        return (
          <p
            key={`p-${index}`}
            className={cn('text-xs leading-relaxed', {
              italic: tone === 'goal',
            })}
          >
            {block.text}
          </p>
        );
      })}
    </div>
  );
}

type DigestCategory = DigestEntry['category'];

const DEFAULT_HIDDEN_CATEGORIES: DigestCategory[] = [];

const DIGEST_CATEGORIES: Record<DigestCategory, { label: string; fg: string; dot: string }> = {
  first_prompt: { label: 'FIRST PROMPT', fg: 'text-success-fg', dot: 'bg-success-fg' },
  question: { label: 'QUESTION', fg: 'text-question', dot: 'bg-question' },
  subagent: { label: 'Subagent', fg: 'text-subagent', dot: 'bg-subagent' },
  todos: { label: 'TODOS', fg: 'text-coslash-muted', dot: 'bg-coslash-muted' },
  recap: { label: 'RECAP', fg: 'text-recap', dot: 'bg-recap' },
  plan: { label: 'PLAN', fg: 'text-recap', dot: 'bg-recap' },
  user: { label: 'USER TURN', fg: 'text-brand', dot: 'bg-brand' },
  compaction: { label: 'COMPACTION', fg: 'text-compaction', dot: 'bg-compaction' },
};

function CategoryChip({
  category,
  count,
  active,
  onToggle,
}: {
  category: DigestCategory;
  count: number;
  active: boolean;
  onToggle: () => void;
}) {
  const meta = DIGEST_CATEGORIES[category];
  return (
    <button
      type="button"
      aria-pressed={active}
      className={cn('flex cursor-pointer items-center gap-1 rounded-full border px-2 py-1 select-none', {
        'bg-coslash-soft': active,
        'border-dashed': !active,
      })}
      onClick={onToggle}
    >
      <span className={cn('size-2 rounded-full', active ? meta.dot : 'bg-coslash-neutral-dot')} />
      <span
        className={cn('text-xs font-semibold', {
          'text-coslash-muted': !active,
        })}
      >
        {meta.label}
      </span>
      <span className="text-coslash-muted text-xs">{count}</span>
    </button>
  );
}

function DigestRow({ entry, endsDay }: { entry: DigestEntry; endsDay?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const meta = DIGEST_CATEGORIES[entry.category];
  const collapsible =
    (entry.category === 'recap' || entry.category === 'plan') && entry.description.length > 120;

  return (
    <div className={cn('border-coslash-line flex items-baseline gap-2 py-1', { 'border-b': !endsDay })}>
      <span className={cn('w-24 shrink-0 text-xs font-bold tracking-wide', meta.fg)}>{meta.label}</span>
      <div className="min-w-0 flex-1">
        <div
          className={cn('text-xs wrap-break-word', {
            'line-clamp-1': collapsible && !expanded,
            'whitespace-pre-wrap': entry.category === 'plan',
          })}
        >
          {entry.description}
        </div>
        {entry.answer != null && (
          <div className="border-coslash-line text-coslash-muted border-l-2 pl-2 text-xs wrap-break-word">
            {entry.answer}
          </div>
        )}
        {collapsible && (
          <button
            type="button"
            aria-expanded={expanded}
            className="text-brand flex cursor-pointer items-center gap-1 pt-1 text-xs"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
            <span>{expanded ? 'collapse' : `expand full ${entry.category}`}</span>
          </button>
        )}
      </div>
      <span className="text-coslash-muted flex shrink-0 flex-col items-end font-mono text-xs whitespace-nowrap">
        <span>turn {entry.turn}</span>
        {entry.time != null && entry.time > 0 && (
          <span className="text-coslash-muted">{formatDigestTime(entry.time)}</span>
        )}
      </span>
    </div>
  );
}

function SubagentDigestRow({ subagentId, detail }: { subagentId: string; detail: SessionDetail }) {
  const subagent = detail.subagents.find((candidate) => candidate.id === subagentId);
  if (!subagent) {
    throw new Error(`digest references subagent ${subagentId}, which is not on the session`);
  }
  const parentName = subagentParentName(subagent, detail.subagents, detail.name);
  const status = SUBAGENT_STATUSES[subagent.status].label;
  return (
    <Dialog>
      <DialogTrigger asChild>
        <div className="bg-subagent-card flex cursor-pointer items-baseline gap-2 rounded-lg border p-2">
          <span className="text-subagent w-24 shrink-0 text-xs font-bold tracking-wide">Subagent</span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="min-w-0 truncate text-sm font-semibold">{subagent.name}</span>
              <SubagentModelBadge model={subagent.model} />
            </div>
            <div className="text-coslash-muted truncate pt-1 text-xs">
              {subagent.result === '' ? status : `${status}: ${subagent.result}`}
            </div>
          </div>
          <div className="text-brand flex shrink-0 items-center gap-1 text-xs">
            <span>open</span>
            <ExternalLinkIcon className="size-3" />
          </div>
        </div>
      </DialogTrigger>
      <SubagentDialogContent subagent={subagent} parentName={parentName} />
    </Dialog>
  );
}

function DateDivider({ label }: { label: string }) {
  return (
    <div className="text-coslash-muted grid grid-cols-[1fr_auto_1fr] items-center gap-2 pt-2 text-xs font-bold tracking-wide">
      <span className="bg-coslash-line h-px" />
      <span>{label}</span>
      <span className="bg-coslash-line h-px" />
    </div>
  );
}

export function DigestSection({ detail }: { detail: SessionDetail }) {
  const [hiddenCategories, setHiddenCategories] = useState<Set<DigestCategory>>(
    () => new Set(DEFAULT_HIDDEN_CATEGORIES),
  );
  const digest = detail.digest;
  if (digest.length === 0) return null;

  const counts = new Map<DigestCategory, number>();
  for (const entry of digest) counts.set(entry.category, (counts.get(entry.category) ?? 0) + 1);

  const toggleCategory = (category: DigestCategory) => {
    setHiddenCategories((prev) => {
      const next = new Set(prev);
      if (next.has(category)) next.delete(category);
      else next.add(category);
      return next;
    });
  };

  const visible = digest.filter((entry) => !hiddenCategories.has(entry.category));
  const times = digest.map((e) => e.time ?? 0).filter((t) => t > 0);
  const dateRange = formatDigestDateRange(times);

  const startsDayIndices = new Set<number>();
  const endsDayIndices = new Set<number>();
  let lastDate = '';
  for (let i = 0; i < visible.length; i++) {
    const entryDate = visible[i].time != null && visible[i].time! > 0 ? digestDateKey(visible[i].time!) : '';
    if (entryDate !== '' && entryDate !== lastDate) {
      startsDayIndices.add(i);
    }
    if (i > 0 && entryDate !== '' && lastDate !== '' && entryDate !== lastDate) {
      endsDayIndices.add(i - 1);
    }
    if (entryDate !== '') {
      lastDate = entryDate;
    }
  }

  const rows = visible.map((entry, index) => {
    return (
      <div key={index}>
        {startsDayIndices.has(index) && <DateDivider label={formatDigestDateDivider(entry.time!)} />}
        {entry.category === 'subagent' ? (
          <SubagentDigestRow subagentId={entry.subagentId!} detail={detail} />
        ) : (
          <DigestRow entry={entry} endsDay={endsDayIndices.has(index) || index === visible.length - 1} />
        )}
      </div>
    );
  });

  return (
    <div>
      <div className="flex items-baseline justify-between gap-2 pb-2">
        <div className="flex items-baseline gap-2">
          <span className="text-coslash-muted text-xs font-semibold tracking-wide">TIMELINE</span>
          <span className="text-coslash-muted text-xs">key events from this session</span>
        </div>
        <span className="text-coslash-muted text-xs">{dateRange}</span>
      </div>
      <div className="flex flex-wrap gap-1 pb-2">
        {(Object.keys(DIGEST_CATEGORIES) as DigestCategory[])
          .filter((category) => counts.has(category))
          .map((category) => (
            <CategoryChip
              key={category}
              category={category}
              count={counts.get(category)!}
              active={!hiddenCategories.has(category)}
              onToggle={() => toggleCategory(category)}
            />
          ))}
      </div>
      <div className="flex flex-col gap-1">{rows}</div>
    </div>
  );
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
        <div className="bg-coslash-surface" />
      </div>
    </div>
  );
}

function FilesChangedList({
  detail,
  onSelectFile,
}: {
  detail: SessionDetail;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number]) => void) | null;
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
                onClick={() => onSelectFile(fileEdit)}
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
      <div className="flex cursor-pointer items-center gap-2" onClick={() => setOpen(!open)}>
        {open ? (
          <ChevronDownIcon className="text-coslash-muted size-3" />
        ) : (
          <ChevronRightIcon className="text-coslash-muted size-3" />
        )}
        <FieldLabel>{`${detail.commands.length} COMMANDS`}</FieldLabel>
        <div className="flex-1 border-b" />
      </div>
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
}: {
  detail: SessionDetail;
  exactDetailsAvailable: boolean;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number]) => void) | null;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
}) {
  // scroll on the outer div, layout on the inner one — flex children of a
  // scroll container shrink to fit instead of overflowing, which collapses
  // the overflow-hidden stat grids
  return (
    <div className="flex-1 overflow-x-hidden overflow-y-auto pb-2">
      <div className="flex flex-col gap-2 px-4">
        <HandoffSection
          detail={detail}
          exactDetailsAvailable={exactDetailsAvailable}
          remoteLaunchable={remoteLaunchable}
          remoteLaunchHint={remoteLaunchHint}
        />
        <RecapSection detail={detail} />
        <DigestSection detail={detail} />
        <ArtifactStats detail={detail} />
        <CommandsSection detail={detail} />
        <FilesChangedList detail={detail} onSelectFile={onSelectFile} />
        <CommitsAndTodos detail={detail} />
      </div>
    </div>
  );
}

function InspectorFooter({
  detail,
  remoteLaunchable,
  remoteResumeHint,
  reviewerOptions,
  remoteReviewUnavailableReason,
  onReviewStarted,
}: {
  detail: SessionDetail;
  remoteLaunchable: boolean;
  remoteResumeHint?: string;
  reviewerOptions: readonly ReviewerOption[];
  remoteReviewUnavailableReason?: string;
  onReviewStarted: () => void;
}) {
  const [previewOpen, setPreviewOpen] = useState(false);
  const showTeamPreview =
    isLocalSession(detail) && teamPreviewEnabled(window.location.search) && !detail.repoLocalOnly;
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';
  const showReview = reviewActionVisible(detail, isReviewSessionName(detail.name));

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
            : remoteLaunchable
              ? `Opens this session in ${getVendor(detail.agent).label} through SSH.`
              : 'Available when the remote SSH host is connected.'}
        </span>
        {showReview && remoteReviewUnavailableReason && (
          <span className="text-warning-fg text-xs">{remoteReviewUnavailableReason}</span>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {showReview && (
          <ReviewDialog
            origin={detail}
            reviewerOptions={reviewerOptions}
            unavailableReason={isLocalSession(detail) ? undefined : remoteReviewUnavailableReason}
            active={detail.reviewPending}
            reviewError={detail.reviewError}
            onStarted={onReviewStarted}
          />
        )}
        {showTeamPreview && (
          <>
            <Button variant="outline" onClick={() => setPreviewOpen(true)}>
              <EyeIcon /> Team sharing preview
            </Button>
            <SnapshotPreviewDialog
              detail={detail}
              open={previewOpen}
              onOpenChange={setPreviewOpen}
              previewOnly
            />
          </>
        )}
        <ResumeSessionButton detail={detail} disabledHint={remoteResumeHint} />
      </div>
    </SheetFooter>
  );
}

export function SessionInspector({
  session,
  sessionsVersion,
  synthesisSettingsKey,
  showMachineBadge = false,
  machines,
  onRefresh,
  onClose,
  reviewerOptions,
  remoteReviewerOptions,
  remoteReviewUnavailableReason,
  onReviewStarted,
}: {
  session: Session | null;
  sessionsVersion: number;
  synthesisSettingsKey: string;
  showMachineBadge?: boolean;
  machines: MachineFact[];
  onRefresh: () => void | Promise<void>;
  onClose: () => void;
  reviewerOptions: readonly ReviewerOption[];
  remoteReviewerOptions: readonly ReviewerOption[];
  remoteReviewUnavailableReason?: string;
  onReviewStarted: () => void;
}) {
  const [detailRetryToken, setDetailRetryToken] = useState(0);
  const detailAttemptIdentity = session == null ? null : `${sessionKey(session)}@${detailRetryToken}`;
  const detailAttemptRef = useRef(detailAttemptIdentity);
  useLayoutEffect(() => {
    detailAttemptRef.current = detailAttemptIdentity;
  }, [detailAttemptIdentity]);
  const { detail, isLoading, loadError, loadErrorKind, cachedOffline, summaryOnly } = useSessionDetail(
    session,
    detailRetryToken,
    sessionsVersion,
    synthesisSettingsKey,
  );
  const contentRef = useRef<HTMLDivElement>(null);
  const resizeRef = useRef<{
    pointerId: number;
    startX: number;
    startWidth: number;
    currentWidth: number;
  } | null>(null);
  const [selectedDiffState, setSelectedDiff] = useState<FileSelection | null>(null);
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
    detail.launchable !== false &&
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
        {session != null && <SheetTitle className="sr-only">{session.name ?? 'Untitled session'}</SheetTitle>}
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
        {isOpen && detail != null && (
          <>
            <SheetHeader>
              <div className="flex min-w-0 flex-col gap-2">
                <SessionInspectorTitle detail={detail} showMachineBadge={showMachineBadge} />
                <HeaderMeta detail={detail} showMachineBadge={false} />
                {session != null && snapshotMayBeStale(detail, session) && <SnapshotStalenessNotice />}
                <div className="border-b p-1" />
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
              onSelectFile={
                summaryOnly
                  ? null
                  : (fileEdit) =>
                      setSelectedDiff({
                        sourceId: detail.sourceId,
                        agent: detail.agent,
                        sessionId: detail.id,
                        revision: detail.detailRevision,
                        path: fileEdit.path,
                        changeIds: fileEdit.changeIds ?? [],
                      })
              }
            />
            <InspectorFooter
              detail={detail}
              remoteLaunchable={remoteLaunchable}
              remoteResumeHint={remoteResumeHint}
              reviewerOptions={isLocalSession(detail) ? reviewerOptions : remoteReviewerOptions}
              remoteReviewUnavailableReason={remoteReviewUnavailableReason}
              onReviewStarted={onReviewStarted}
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
        <SheetContent className="coslash-shell w-full! max-w-none! gap-0 sm:w-1/2!" showCloseButton={true}>
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
