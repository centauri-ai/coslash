import { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangleIcon,
  CheckIcon,
  ExternalLinkIcon,
  LoaderCircleIcon,
  SearchIcon,
  ShieldCheckIcon,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { isLocalSession, sessionRevision } from '@/pages/coslash/lib/session';
import {
  beginHubPairing,
  pollHubPairing,
  prepareBackup,
  shareSynthesisStatus,
  submitHubShare,
  type PairingResult,
  type ShareSynthesisStatus,
} from './api';
import {
  COMPLETE_BACKUP_DRAFT_STORAGE_KEY,
  restoreDraft,
  retainRetryDraftSelection,
  retryDraftForResult,
  storeDraft,
  updateRetryDraft,
  type RetryDraft,
  type ReviewRecord,
} from './draft';
import {
  BACKUP_SHARE_VERSION,
  backupSelection,
  bindBackupConsent,
  COMPLETE_BACKUP_SUPPORT_MESSAGE,
  consentStillCurrent,
  filterShareCandidates,
  hubRouteURL,
  isCompleteBackupCandidate,
  limitShareSelection,
  localSessionId,
  MAX_SHARE_ITEMS,
  mergeShareItemResults,
  mergeShareItemWorkspaceNames,
  planShareRetry,
  primarySuccessRoute,
  reconcileVisibleSelection,
  RETRY_RULES,
  shareBatchSummary,
  shareResultState,
  toggleCandidate,
  toggleCandidateGroup,
  type BackupPreview,
  type DestinationResult,
  type ShareCandidate,
  type ShareItemResult,
  type ShareResult,
  type ShareWindow,
} from './model';

type Phase = 'select' | 'preparing' | 'review' | 'uploading' | 'result';

export function CompleteBackupSupport({ candidate }: { candidate: ShareCandidate }) {
  if (isCompleteBackupCandidate(candidate)) return null;
  return (
    <span data-testid="complete-backup-unsupported" className="text-warning-fg block pt-1 text-xs">
      {COMPLETE_BACKUP_SUPPORT_MESSAGE}
    </span>
  );
}

const ELIGIBILITY_COPY: Record<
  Exclude<DestinationResult['state'], 'ready'>,
  { title: string; detail: string; action: string }
> = {
  signed_out: {
    title: 'Sign in to share',
    detail: 'Sharing stays off. Sign in, choose a workspace, and pair this device before selecting sessions.',
    action: 'Open cloud settings',
  },
  pairing_required: {
    title: 'Pair this device',
    detail:
      'No workspace-bound device credential is available. Pairing must finish before a destination can be approved.',
    action: 'Open pairing settings',
  },
  credential_dormant: {
    title: 'Paired workspace is not active',
    detail: 'Select the paired workspace, then verify the refreshed destination and member count.',
    action: 'Open workspace settings',
  },
  credential_revoked: {
    title: 'Device access was revoked',
    detail:
      'This credential cannot be retried. Pair the device again; no selected session has been uploaded.',
    action: 'Open pairing settings',
  },
};

function fixtureBackupPreview(
  selection: ReturnType<typeof backupSelection>,
  audienceVersion: string,
  revision: number,
): BackupPreview {
  const hash = 'a'.repeat(64);
  return {
    adapterVersion: 'backup-preview/v1',
    state: 'ready',
    approvalAllowed: true,
    selection,
    bundleId: hash,
    sourceRevision: 'fixture-revision',
    synthesisRevision: revision,
    coverage: {
      artifactCount: 6,
      artifactCounts: [
        { kind: 'raw-transcript', count: 1 },
        { kind: 'parsed-session-record', count: 1 },
        { kind: 'exact-change-body', count: 2 },
        { kind: 'session-enrichment', count: 1 },
        { kind: 'synthesis', count: 1 },
      ],
      totalBytes: 98_304,
      revisionSha256: hash,
      problems: [],
    },
    capability: {
      serverId: 'fixture-hub',
      maxBackupBytes: 1_073_741_824,
      maxBackupChunkBytes: 1_048_576,
      backupWorkspaceBytes: 53_687_091_200,
      backupUploadExpiresSeconds: 86_400,
    },
    audienceVersion,
  };
}

export function CompleteBackupDisclosure({ workspaceName }: { workspaceName: string }) {
  return (
    <section
      data-testid="complete-backup-disclosure"
      className="mt-3 rounded-lg border p-3"
      aria-labelledby="complete-backup-disclosure"
    >
      <h3 id="complete-backup-disclosure" className="text-xs font-bold tracking-wide">
        COMPLETE UNREDACTED BACKUP
      </h3>
      <p className="text-muted-foreground pt-1 text-xs leading-relaxed">
        Raw prompts, commands, tool output, paths, environment fragments, and exact file-change bodies may
        contain credentials or other secrets. coSlash does not redact this backup. Active members of{' '}
        {workspaceName} can access the accepted revision.
      </p>
    </section>
  );
}

export function BackupUploadProgress({ items }: { items: { id: string; label: string; state: string }[] }) {
  return (
    <div role="status" className="min-h-72 overflow-y-auto rounded-lg border">
      {items.map((item) => (
        <div
          key={item.id}
          data-testid="backup-upload-progress"
          className="flex items-center justify-between gap-3 border-b p-3 text-sm last:border-b-0"
        >
          <span className="min-w-0 truncate">{item.label}</span>
          <Badge variant="secondary">{item.state}</Badge>
        </div>
      ))}
    </div>
  );
}

export function ShareCandidatesLoadingStatus({ stage }: { stage: 'loading' | 'refreshing' }) {
  return (
    <div
      data-testid="share-candidates-loading"
      role="status"
      aria-atomic="true"
      className="bg-info-bg text-info-fg flex items-center gap-2 rounded-lg border p-3 text-sm"
    >
      <LoaderCircleIcon className="size-4 shrink-0 animate-spin" aria-hidden="true" />
      <span>
        {stage === 'refreshing'
          ? 'Waiting for connected workspaces to finish refreshing…'
          : 'Loading sessions available to share…'}
      </span>
    </div>
  );
}

export function BackupPreparationProgress({ completed, total }: { completed: number; total: number }) {
  return (
    <div role="status" aria-atomic="true" className="grid min-h-72 place-items-center text-sm">
      Preparing complete backups: {completed} of {total} ready…
    </div>
  );
}

const SYNTHESIS_MESSAGES: Record<Exclude<ShareSynthesisStatus['state'], 'ready' | 'pending'>, string> = {
  revision_changed:
    'The session changed while preparing its debrief. Refresh sessions and review this revision again.',
  missing: 'The selected local session is no longer available. Refresh sessions and try again.',
  ineligible: 'This session is not eligible for Local AI synthesis under the current settings.',
  consent_required:
    'Local AI synthesis needs first-run consent in Settings before this session can be shared.',
  disabled: 'Local AI synthesis is disabled in Settings. Enable it before sharing this session.',
  unavailable: 'The configured local synthesis backend is unavailable. Check Settings and retry.',
  failed: 'Local AI synthesis failed. Check the configured backend, then retry after its cooldown.',
};

async function waitForLocalDebrief(
  session: ShareCandidate['session'],
  signal: AbortSignal,
  onPending: () => void,
): Promise<{ status: ShareSynthesisStatus; generated: boolean }> {
  let generated = false;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (signal.aborted) throw new DOMException('Preparation cancelled', 'AbortError');
    const status = await shareSynthesisStatus(session, signal);
    if (status.state === 'ready') return { status, generated };
    if (status.state !== 'pending') throw new Error(SYNTHESIS_MESSAGES[status.state]);
    generated = true;
    onPending();
    await new Promise<void>((resolve, reject) => {
      const timer = globalThis.setTimeout(() => {
        signal.removeEventListener('abort', stop);
        resolve();
      }, 1000);
      const stop = () => {
        globalThis.clearTimeout(timer);
        reject(new DOMException('Preparation cancelled', 'AbortError'));
      };
      signal.addEventListener('abort', stop, { once: true });
    });
  }
  throw new Error('Local debrief generation is still pending. Retry when the configured backend is ready.');
}

export function ShareToHubDialog({
  open,
  onOpenChange,
  candidates,
  candidatesLoading,
  candidatesError,
  candidatesLoadStage = 'loading',
  onRetryCandidates = () => {},
  window,
  onWindowChange,
  destinationResult,
  onOpenSettings,
  onDestinationRefresh,
  onLocalSynthesisReady = () => {},
  synthesisBackend = 'configured CLI',
  synthesisModel = 'configured model',
  fixtureMode = false,
  fixtureOutcome = 'success',
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  candidates: ShareCandidate[];
  candidatesLoading: boolean;
  candidatesError: string | null;
  candidatesLoadStage?: 'loading' | 'refreshing' | 'ready' | 'error';
  onRetryCandidates?: () => void;
  window: ShareWindow;
  onWindowChange: (window: ShareWindow) => void;
  destinationResult: DestinationResult;
  onOpenSettings: () => void;
  onDestinationRefresh: () => Promise<DestinationResult>;
  onLocalSynthesisReady?: () => void | Promise<ShareCandidate['session'][]>;
  synthesisBackend?: string;
  synthesisModel?: string;
  fixtureMode?: boolean;
  fixtureOutcome?: 'success' | 'partial' | 'private' | 'failed';
}) {
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [phase, setPhase] = useState<Phase>('select');
  const [records, setRecords] = useState<ReviewRecord[]>([]);
  const [reviewed, setReviewed] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [result, setResult] = useState<ShareResult | null>(null);
  const [fixtureAttempt, setFixtureAttempt] = useState(0);
  const [pairing, setPairing] = useState<PairingResult | null>(null);
  const [pairingError, setPairingError] = useState<string | null>(null);
  const [pairingRefreshRequired, setPairingRefreshRequired] = useState(false);
  const [retryReadyAt, setRetryReadyAt] = useState(0);
  const [clock, setClock] = useState(() => Date.now());
  const [progress, setProgress] = useState<
    Record<string, 'queued' | 'resuming' | 'uploading' | 'accepted' | 'private' | 'failed'>
  >({});
  const [resultLabels, setResultLabels] = useState<Record<string, string>>({});
  const [resultWorkspaceNames, setResultWorkspaceNames] = useState<Record<string, string>>({});
  const [priorResults, setPriorResults] = useState<ShareItemResult[]>([]);
  const [resumingDraft, setResumingDraft] = useState(false);
  const [renewedReviewIds, setRenewedReviewIds] = useState<Set<string>>(new Set());
  const [uploadRecords, setUploadRecords] = useState<ReviewRecord[]>([]);
  const [preparedCount, setPreparedCount] = useState(0);
  const [preparationProgress, setPreparationProgress] = useState<Record<string, string>>({});
  const [uploadPending, setUploadPending] = useState(false);
  const previewGeneration = useRef(0);
  const previewAbort = useRef<AbortController | null>(null);
  const uploadGeneration = useRef(0);
  const uploadInFlight = useRef(false);
  const restoredForOpen = useRef(false);

  const visible = useMemo(
    () => filterShareCandidates(candidates, search, window),
    [candidates, search, window],
  );
  const supportedVisible = visible.filter(isCompleteBackupCandidate);
  const selectedCandidates = candidates.filter(
    (candidate) => isCompleteBackupCandidate(candidate) && selected.has(localSessionId(candidate.session)),
  );
  const currentSelection = new Set(selectedCandidates.map(({ session }) => localSessionId(session)));
  const destination = destinationResult.state === 'ready' ? destinationResult.destination : null;
  const reviewStillCurrent =
    destination != null &&
    records.every((record) => {
      const current = candidates.find(
        ({ session }) => localSessionId(session) === record.item.localSessionId,
      );
      return (
        current != null &&
        consentStillCurrent(record.item, current.session, record.preview, destination) &&
        (!isLocalSession(current.session) ||
          record.preview.synthesisRevision === sessionRevision(current.session))
      );
    });
  const groups = useMemo(() => {
    const values = new Map<string, ShareCandidate[]>();
    for (const candidate of visible) {
      const repository = candidate.session.repo ?? '(no repository)';
      values.set(repository, [...(values.get(repository) ?? []), candidate]);
    }
    return [...values.entries()];
  }, [visible]);

  /* oxlint-disable react/set-state-in-effect -- clear transient form state when the controlled dialog closes */
  useEffect(() => {
    if (open) return;
    restoredForOpen.current = false;
    previewGeneration.current += 1;
    previewAbort.current?.abort();
    uploadGeneration.current += 1;
    setSearch('');
    setSelected(new Set());
    setPhase('select');
    setRecords([]);
    setReviewed(false);
    setProblem(null);
    setResult(null);
    setFixtureAttempt(0);
    setPairing(null);
    setPairingError(null);
    setPairingRefreshRequired(false);
    setRetryReadyAt(0);
    setProgress({});
    setResultLabels({});
    setResultWorkspaceNames({});
    setPriorResults([]);
    setResumingDraft(false);
    setRenewedReviewIds(new Set());
    setUploadRecords([]);
    setPreparedCount(0);
  }, [open]);
  /* oxlint-enable react/set-state-in-effect */

  /* oxlint-disable react/set-state-in-effect -- restore a frozen, explicitly reviewed upload after dialog/app restart */
  useEffect(() => {
    if (!open || destination == null || restoredForOpen.current) return;
    restoredForOpen.current = true;
    try {
      const raw = localStorage.getItem(COMPLETE_BACKUP_DRAFT_STORAGE_KEY);
      if (!raw) return;
      const restored = restoreDraft(raw, candidates, destination);
      if (restored == null) {
        localStorage.removeItem(COMPLETE_BACKUP_DRAFT_STORAGE_KEY);
        return;
      }
      setRecords(restored.records);
      setRenewedReviewIds(restored.renewedReviewIds);
      setSelected(
        new Set([...restored.records.map(({ item }) => item.localSessionId), ...restored.renewedReviewIds]),
      );
      setReviewed(restored.reviewed);
      setProblem(
        restored.renewedReviewIds.size > 0
          ? 'Some failed sessions require a refreshed preview and explicit approval.'
          : 'Resuming frozen complete backups with their original review and upload identities.',
      );
      setResumingDraft(restored.records.length > 0);
      setPhase(restored.renewedReviewIds.size > 0 ? 'select' : 'review');
    } catch {
      localStorage.removeItem(COMPLETE_BACKUP_DRAFT_STORAGE_KEY);
    }
  }, [candidates, destination, open]);
  /* oxlint-enable react/set-state-in-effect */

  useEffect(() => {
    if (records.length > 0 || renewedReviewIds.size > 0) {
      storeDraft(records, reviewed && renewedReviewIds.size === 0, renewedReviewIds);
    }
  }, [records, renewedReviewIds, reviewed]);

  useEffect(() => {
    if (result?.state === 'succeeded') localStorage.removeItem(COMPLETE_BACKUP_DRAFT_STORAGE_KEY);
  }, [result?.state]);

  /* oxlint-disable react/set-state-in-effect -- revoke selections whose source is no longer eligible */
  useEffect(() => {
    setSelected((current) => reconcileVisibleSelection(current, visible));
  }, [visible]);
  /* oxlint-enable react/set-state-in-effect */

  useEffect(() => {
    const pairingId = pairing?.pairingId;
    if (!open || fixtureMode || pairing?.state !== 'pending' || !pairingId || pairingRefreshRequired) return;
    let stopped = false;
    let timeout = 0;
    const delay = Math.max(2, pairing.intervalSeconds ?? 2) * 1000;
    const poll = async () => {
      let finished = false;
      try {
        const next = await pollHubPairing(pairingId);
        if (stopped) return;
        if (next.state === 'paired') {
          finished = true;
          try {
            await onDestinationRefresh();
            if (!stopped) setPairing((current) => ({ ...current, ...next }));
          } catch (error) {
            if (!stopped) {
              setPairingRefreshRequired(true);
              setPairingError(
                error instanceof Error ? error.message : 'The Hub destination could not be refreshed.',
              );
            }
          }
        } else if (next.state === 'expired') {
          finished = true;
          setPairing((current) => ({ ...current, ...next }));
        } else {
          setPairing((current) => ({ ...current, ...next }));
        }
      } catch (error) {
        if (!stopped) {
          setPairingError(error instanceof Error ? error.message : 'Device pairing could not finish.');
        }
      } finally {
        if (!stopped && !finished) timeout = globalThis.setTimeout(poll, delay);
      }
    };
    timeout = globalThis.setTimeout(poll, delay);
    return () => {
      stopped = true;
      globalThis.clearTimeout(timeout);
    };
  }, [
    fixtureMode,
    onDestinationRefresh,
    open,
    pairing?.intervalSeconds,
    pairing?.pairingId,
    pairing?.state,
    pairingRefreshRequired,
  ]);

  useEffect(() => {
    if (retryReadyAt <= clock) return;
    const timeout = globalThis.setTimeout(() => setClock(Date.now()), Math.min(1000, retryReadyAt - clock));
    return () => globalThis.clearTimeout(timeout);
  }, [clock, retryReadyAt]);

  /* oxlint-disable react/set-state-in-effect -- revoke consent when its source revision changes */
  useEffect(() => {
    if (!open || phase !== 'review' || records.length === 0 || reviewStillCurrent) return;
    setRecords([]);
    setReviewed(false);
    setProblem('The source revision or destination changed. Review the current selection again.');
    setPhase('select');
  }, [open, phase, records.length, reviewStillCurrent]);
  /* oxlint-enable react/set-state-in-effect */

  const replaceSelection = (next: Set<string>) => {
    previewGeneration.current += 1;
    previewAbort.current?.abort();
    uploadGeneration.current += 1;
    const limited = limitShareSelection(next, candidates);
    const retained = retainRetryDraftSelection({ records, renewedReviewIds }, limited);
    setSelected(limited);
    setRecords(retained.records);
    setRenewedReviewIds(retained.renewedReviewIds);
    setReviewed(false);
    setProblem(null);
    setResult(null);
    setPhase('select');
    setFixtureAttempt(0);
    setRetryReadyAt(0);
    setResumingDraft(false);
    storeDraft(retained.records, false, retained.renewedReviewIds);
  };

  const narrow = (nextSearch: string, nextWindow: ShareWindow) => {
    const nextVisible = filterShareCandidates(candidates, nextSearch, nextWindow);
    replaceSelection(reconcileVisibleSelection(currentSelection, nextVisible));
    setSearch(nextSearch);
    onWindowChange(nextWindow);
  };

  const reviewExactPayloads = async () => {
    if (destination == null || selectedCandidates.length === 0) return;
    const generation = ++previewGeneration.current;
    previewAbort.current?.abort();
    const controller = new AbortController();
    previewAbort.current = controller;
    const activeCandidates = selectedCandidates;
    const prior = new Map(records.map((record) => [record.item.localSessionId, record]));
    setPhase('preparing');
    setProblem(null);
    setPreparedCount(0);
    setPreparationProgress(
      Object.fromEntries(activeCandidates.map(({ session }) => [localSessionId(session), 'Queued'])),
    );
    const markProgress = (id: string, message: string) => {
      if (generation === previewGeneration.current)
        setPreparationProgress((current) => ({ ...current, [id]: message }));
    };
    const markPrepared = () => {
      if (generation === previewGeneration.current) setPreparedCount((current) => current + 1);
    };
    try {
      const nextRecords = new Array<ReviewRecord>(activeCandidates.length);
      const syntheses = new Array<ShareSynthesisStatus | null>(activeCandidates.length).fill(null);
      let nextIndex = 0;
      await Promise.all(
        Array.from({ length: Math.min(4, activeCandidates.length) }, async () => {
          while (nextIndex < activeCandidates.length) {
            const index = nextIndex++;
            const { session } = activeCandidates[index];
            const id = localSessionId(session);
            if (!fixtureMode && isLocalSession(session)) {
              markProgress(id, 'Checking current local debrief');
              const result = await waitForLocalDebrief(session, controller.signal, () =>
                markProgress(id, 'Generating local debrief'),
              );
              syntheses[index] = result.status;
              markProgress(id, result.generated ? 'Local debrief ready' : 'Including current local debrief');
            } else {
              markProgress(id, 'Waiting to prepare complete backup');
            }
          }
        }),
      );
      if (generation !== previewGeneration.current) return;
      if (!fixtureMode && syntheses.some((status) => status != null)) {
        const refreshed = await onLocalSynthesisReady();
        if (Array.isArray(refreshed)) {
          for (let index = 0; index < activeCandidates.length; index += 1) {
            const expected = syntheses[index];
            if (!expected) continue;
            const source = activeCandidates[index].session;
            const current = refreshed.find((item) => localSessionId(item) === localSessionId(source));
            if (!current || sessionRevision(current) !== expected.revision || current.synthesis == null) {
              throw new Error(
                'The refreshed local session no longer has this revision’s debrief. Refresh sessions and retry.',
              );
            }
          }
        }
      }
      if (generation !== previewGeneration.current) return;
      nextIndex = 0;
      await Promise.all(
        Array.from({ length: Math.min(4, activeCandidates.length) }, async () => {
          while (nextIndex < activeCandidates.length) {
            const index = nextIndex++;
            const candidate = activeCandidates[index];
            const { session } = candidate;
            const id = localSessionId(session);
            const synthesis = syntheses[index];
            markProgress(id, 'Preparing complete backup after debrief readiness');
            const existing = prior.get(id);
            if (
              existing &&
              consentStillCurrent(existing.item, session, existing.preview, destination) &&
              (fixtureMode ||
                !isLocalSession(session) ||
                existing.preview.synthesisRevision === synthesis?.revision)
            ) {
              markPrepared();
              nextRecords[index] = existing;
              continue;
            }
            const selection = backupSelection(session);
            const preview = fixtureMode
              ? fixtureBackupPreview(selection, destination.audienceVersion, sessionRevision(session))
              : await prepareBackup(selection, controller.signal);
            if (preview.state !== 'ready') {
              throw new Error(
                `${preview.problem?.message ?? 'A complete backup could not be prepared.'} ${preview.problem?.action ?? ''}`.trim(),
              );
            }
            if (synthesis && preview.synthesisRevision !== synthesis.revision) {
              throw new Error(
                'The complete backup no longer contains this revision’s Local AI debrief. Refresh sessions and retry.',
              );
            }
            if (synthesis) {
              const current = await shareSynthesisStatus(session, controller.signal);
              if (current.state !== 'ready' || current.revision !== synthesis.revision) {
                throw new Error(
                  'The session changed during backup preparation. Refresh sessions and review it again.',
                );
              }
            }
            const item = bindBackupConsent(
              session,
              preview,
              destination,
              `${BACKUP_SHARE_VERSION}:${crypto.randomUUID()}`,
            );
            markPrepared();
            markProgress(id, 'Complete backup ready for review');
            nextRecords[index] = {
              candidate: synthesis
                ? { ...candidate, session: { ...session, synthesis: synthesis.synthesis ?? null } }
                : candidate,
              preview,
              item,
            };
          }
        }),
      );
      if (generation !== previewGeneration.current) return;
      setRecords(nextRecords);
      setRenewedReviewIds(new Set());
      setReviewed(false);
      setPhase('review');
    } catch (error) {
      controller.abort();
      if (generation !== previewGeneration.current) return;
      if (
        error instanceof Error &&
        (error.message.includes('session changed') ||
          error.message.includes('no longer contains') ||
          error.message.includes('no longer available') ||
          error.message.includes('no longer has this revision'))
      ) {
        onRetryCandidates();
      }
      setProblem(error instanceof Error ? error.message : 'The complete backup could not be prepared.');
      setPhase('select');
    }
  };

  const exerciseFixtureResult = () => {
    if (!reviewed || records.length === 0 || !reviewStillCurrent) return;
    storeDraft(records, true);
    const partial = fixtureOutcome === 'partial' && fixtureAttempt === 0 && records.length > 1;
    const results: ShareResult['results'] = records.map((record, index) => {
      if (fixtureOutcome === 'failed' || (partial && index === records.length - 1)) {
        return {
          localSessionId: record.item.localSessionId,
          idempotencyKey: record.item.idempotencyKey,
          state: 'failed',
          deduplicated: false,
          error: { code: 'temporary_unavailable', retryable: true },
        };
      }
      if (fixtureOutcome === 'private') {
        return {
          localSessionId: record.item.localSessionId,
          idempotencyKey: record.item.idempotencyKey,
          state: 'private',
          private: true,
          deduplicated: false,
          sharingNotice: 'This backup is private in My space. Share it from coSlash Hub when you are ready.',
        };
      }
      const suffix = String(index + 1).padStart(12, '0');
      const repositoryId = `80000000-0000-4000-8000-${suffix}`;
      const alreadyAccepted = record.candidate.previouslyShared;
      return {
        localSessionId: record.item.localSessionId,
        idempotencyKey: record.item.idempotencyKey,
        state: alreadyAccepted ? 'already_accepted' : 'accepted',
        revisionId: `70000000-0000-4000-8000-${suffix}`,
        deduplicated: alreadyAccepted,
        sharedAt: '2026-08-18T18:00:00Z',
        route: {
          hubContractVersion: 'session-backup-read/v1',
          repositoryId,
          path: `/v3/session-backups/70000000-0000-4000-8000-${suffix}`,
        },
      };
    });
    showResult({
      contractVersion: BACKUP_SHARE_VERSION,
      requestId: crypto.randomUUID(),
      state: shareResultState(results),
      results,
    });
  };

  const showResult = (next: ShareResult) => {
    const results = mergeShareItemResults(priorResults, next.results);
    const combined: ShareResult = { ...next, state: shareResultState(results), results };
    const workspaceNames = mergeShareItemWorkspaceNames(
      resultWorkspaceNames,
      next.results,
      destination?.workspaceName ?? 'the workspace',
    );
    const delay = Math.max(
      0,
      ...combined.results.map((item) =>
        item.state === 'failed' && item.error.retryable ? (item.error.retryAfterSeconds ?? 0) : 0,
      ),
    );
    const now = Date.now();
    const retryPlan = planShareRetry(combined);
    const retryDraft = retryDraftForResult(records, combined);
    const retryable = new Set([...retryPlan.unchanged, ...retryPlan.renewedReview]);
    setResultLabels(
      Object.fromEntries(
        combined.results.map((item, index) => {
          const candidate =
            records.find((record) => record.item.localSessionId === item.localSessionId)?.candidate ??
            candidates.find(({ session }) => localSessionId(session) === item.localSessionId);
          const label =
            candidate?.session.name?.trim() ||
            candidate?.session.sourceLabel?.trim() ||
            `Session ${index + 1}`;
          return [item.localSessionId, label];
        }),
      ),
    );
    setResultWorkspaceNames(workspaceNames);
    setPriorResults([]);
    setRecords(retryDraft.records);
    setRenewedReviewIds(retryDraft.renewedReviewIds);
    setSelected(retryable);
    setReviewed(retryDraft.renewedReviewIds.size === 0 && retryDraft.records.length > 0);
    storeDraft(
      retryDraft.records,
      retryDraft.renewedReviewIds.size === 0 && retryDraft.records.length > 0,
      retryDraft.renewedReviewIds,
    );
    setResult(combined);
    setClock(now);
    setRetryReadyAt(now + delay * 1000);
    setPhase('result');
  };

  const submitReviewed = async () => {
    if (uploadInFlight.current) return;
    if (fixtureMode) {
      exerciseFixtureResult();
      return;
    }
    if (!reviewed || records.length === 0 || !reviewStillCurrent) return;
    uploadInFlight.current = true;
    setUploadPending(true);
    const generation = ++uploadGeneration.current;
    const activeRecords = records;
    setPhase('uploading');
    setProblem(null);
    setUploadRecords(activeRecords);
    setProgress(
      Object.fromEntries(
        activeRecords.map((record) => [record.item.localSessionId, resumingDraft ? 'resuming' : 'queued']),
      ),
    );
    let pendingDraft: RetryDraft = { records: [...activeRecords], renewedReviewIds: new Set() };
    const persistPending = () => {
      const pendingIds = new Set([
        ...pendingDraft.records.map(({ item }) => item.localSessionId),
        ...pendingDraft.renewedReviewIds,
      ]);
      setRecords(pendingDraft.records);
      setRenewedReviewIds(new Set(pendingDraft.renewedReviewIds));
      setSelected(pendingIds);
      storeDraft(
        pendingDraft.records,
        pendingDraft.renewedReviewIds.size === 0 && pendingDraft.records.length > 0,
        pendingDraft.renewedReviewIds,
      );
    };
    try {
      const results: ShareResult['results'] = [];
      for (const record of activeRecords) {
        if (!resumingDraft) {
          setProgress((current) => ({ ...current, [record.item.localSessionId]: 'uploading' }));
        }
        const response = await submitHubShare({
          contractVersion: BACKUP_SHARE_VERSION,
          requestId: crypto.randomUUID(),
          items: [record.item],
        });
        if (generation !== uploadGeneration.current) return;
        const item = response.results[0];
        if (!item) throw new Error('The Hub returned no item result.');
        results.push(item);
        pendingDraft = updateRetryDraft(pendingDraft, item);
        persistPending();
        setProgress((current) => ({
          ...current,
          [record.item.localSessionId]:
            item.state === 'failed' ? 'failed' : item.state === 'private' ? 'private' : 'accepted',
        }));
      }
      showResult({
        contractVersion: BACKUP_SHARE_VERSION,
        requestId: crypto.randomUUID(),
        state: shareResultState(results),
        results,
      });
      setResumingDraft(false);
      setUploadRecords([]);
    } catch (error) {
      if (generation !== uploadGeneration.current) return;
      persistPending();
      setReviewed(pendingDraft.renewedReviewIds.size === 0 && pendingDraft.records.length > 0);
      setResumingDraft(pendingDraft.records.length > 0);
      setProblem(error instanceof Error ? error.message : 'The Hub share request failed.');
      setPhase(pendingDraft.renewedReviewIds.size > 0 ? 'select' : 'review');
      setUploadRecords([]);
    } finally {
      uploadInFlight.current = false;
      setUploadPending(false);
    }
  };

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      previewGeneration.current += 1;
      previewAbort.current?.abort();
      uploadGeneration.current += 1;
    }
    onOpenChange(nextOpen);
  };

  const beginPairing = async () => {
    setPairingError(null);
    setPairingRefreshRequired(false);
    try {
      const next = await beginHubPairing();
      setPairing(next);
      const target = next.verificationUriComplete || next.verificationUri;
      if (target) globalThis.open(target, '_blank', 'noopener,noreferrer');
    } catch (error) {
      setPairingError(error instanceof Error ? error.message : 'Device pairing could not start.');
    }
  };

  const retryDestinationRefresh = async () => {
    setPairingError(null);
    try {
      await onDestinationRefresh();
      setPairing((current) => (current ? { ...current, state: 'paired' } : { state: 'paired' }));
      setPairingRefreshRequired(false);
    } catch (error) {
      setPairingError(error instanceof Error ? error.message : 'The Hub destination could not be refreshed.');
    }
  };

  const retryFailed = async () => {
    if (!result || retryReadyAt > clock) return;
    const plan = planShareRetry(result);
    const retry = new Set([...plan.unchanged, ...plan.renewedReview]);
    if (retry.size === 0) return;
    setProblem(null);
    if (plan.refreshDestination.size > 0) {
      try {
        await onDestinationRefresh();
      } catch (error) {
        setProblem(error instanceof Error ? error.message : 'The Hub destination could not be refreshed.');
        return;
      }
    }
    setPriorResults(result.results);
    setSelected(retry);
    setRecords((current) => current.filter((record) => plan.unchanged.has(record.item.localSessionId)));
    setRenewedReviewIds(plan.renewedReview);
    setReviewed(plan.renewedReview.size === 0);
    setProblem(
      plan.renewedReview.size > 0
        ? 'The failed sessions require a refreshed preview and explicit approval.'
        : null,
    );
    setResult(null);
    setRetryReadyAt(0);
    setFixtureAttempt((current) => current + 1);
    if (plan.renewedReview.size > 0) setResumingDraft(false);
    setPhase(plan.renewedReview.size > 0 ? 'select' : 'review');
  };

  const restartFailed = () => {
    replaceSelection(currentSelection);
    setRecords([]);
    setRenewedReviewIds(new Set());
    setPriorResults([]);
    setResultWorkspaceNames({});
  };

  const eligibility = destinationResult.state === 'ready' ? null : ELIGIBILITY_COPY[destinationResult.state];
  const route = result ? primarySuccessRoute(result) : null;
  const retryPlan = result ? planShareRetry(result) : null;
  const retryCount = retryPlan ? new Set([...retryPlan.unchanged, ...retryPlan.renewedReview]).size : 0;
  const failedCount = result?.results.filter((item) => item.state === 'failed').length ?? 0;
  const summary =
    result == null
      ? null
      : shareBatchSummary(result, destination?.workspaceName ?? 'the workspace', resultWorkspaceNames);
  const retryWait = Math.max(0, Math.ceil((retryReadyAt - clock) / 1000));

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        data-testid="share-to-hub-dialog"
        className="coslash-shell flex max-h-[calc(100vh-2rem)] w-[calc(100%-2rem)] max-w-none! flex-col overflow-x-hidden overflow-y-hidden sm:w-[min(56rem,calc(100vw-2rem))]"
      >
        <DialogHeader className="min-w-0">
          <div className="flex items-center gap-2">
            <DialogTitle>Share to Hub</DialogTitle>
            <Badge variant="secondary">
              {fixtureMode ? 'FIXTURE BUILD · NO UPLOAD' : 'LIVE · EXPLICIT APPROVAL'}
            </Badge>
          </div>
          <DialogDescription>
            {fixtureMode
              ? 'Select sessions, review complete-backup inventory, and exercise retry and result states.'
              : 'Select local or SSH Codex sessions, review each complete unredacted backup, then upload only what you approve.'}
          </DialogDescription>
        </DialogHeader>

        {open && destination != null && candidatesLoading && phase === 'select' && (
          <ShareCandidatesLoadingStatus
            stage={candidatesLoadStage === 'refreshing' ? 'refreshing' : 'loading'}
          />
        )}

        {open && uploadPending && phase !== 'uploading' && (
          <div
            role="status"
            aria-atomic="true"
            className="bg-info-bg text-info-fg rounded-lg border p-3 text-sm"
          >
            A share request is still finishing. Wait for it to complete before retrying.
          </div>
        )}

        {destination == null ? (
          <div
            role="status"
            className="flex min-h-72 flex-col items-center justify-center rounded-xl border p-8 text-center"
          >
            <AlertTriangleIcon className="text-warning-fg size-7" />
            <h3 className="mt-3 font-semibold">{eligibility?.title}</h3>
            <p className="text-coslash-muted mt-2 max-w-md text-sm">{eligibility?.detail}</p>
            {!fixtureMode && pairing?.state === 'pending' ? (
              <div className="mt-5 rounded-lg border p-4">
                <p className="text-sm font-semibold">
                  {pairingRefreshRequired ? 'Pairing approved' : `Approve code ${pairing.userCode}`}
                </p>
                <p className="text-coslash-muted pt-1 text-xs">
                  {pairingRefreshRequired
                    ? 'Refresh the destination to finish enabling sharing.'
                    : 'A Hub sign-in window was opened. This page will update after approval.'}
                </p>
                {pairingRefreshRequired ? (
                  <Button className="mt-3" size="sm" onClick={retryDestinationRefresh}>
                    Retry destination refresh
                  </Button>
                ) : (
                  (pairing.verificationUriComplete ?? pairing.verificationUri) && (
                    <a
                      className="text-info-fg mt-3 inline-block text-sm font-semibold underline"
                      href={pairing.verificationUriComplete ?? pairing.verificationUri}
                      target="_blank"
                      rel="noreferrer"
                    >
                      Open approval page
                    </a>
                  )
                )}
              </div>
            ) : (
              <Button className="mt-5" onClick={fixtureMode ? onOpenSettings : beginPairing}>
                {fixtureMode ? eligibility?.action : 'Pair this device'}
              </Button>
            )}
            {pairing?.state === 'expired' && (
              <p className="text-warning-fg mt-3 text-sm">Pairing expired. Start again.</p>
            )}
            {pairingError && (
              <p className="text-danger-fg mt-3 text-sm" role="alert">
                {pairingError}
              </p>
            )}
          </div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden">
            <div className="bg-info-bg text-info-fg flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3">
              <div>
                <div className="text-sm font-semibold">{destination.workspaceName}</div>
                <div className="pt-0.5 text-xs">
                  {destination.currentMemberCount}{' '}
                  {destination.currentMemberCount === 1 ? 'member' : 'members'} can see approved revisions
                </div>
              </div>
              <div className="flex items-center gap-1.5 text-xs font-semibold">
                <ShieldCheckIcon className="size-4" /> Paired destination
              </div>
            </div>

            {phase === 'select' && (
              <>
                <div className="flex flex-wrap items-center gap-2">
                  <div className="relative min-w-48 flex-1">
                    <SearchIcon className="text-coslash-muted pointer-events-none absolute top-2 left-2.5 size-4" />
                    <Input
                      aria-label="Filter shareable sessions"
                      className="pl-8"
                      placeholder="Filter sessions"
                      value={search}
                      disabled={candidatesLoading}
                      onChange={(event) => narrow(event.target.value, window)}
                    />
                  </div>
                  <div className="bg-coslash-soft flex rounded-lg p-1" aria-label="Share time window">
                    {(['7d', '30d', 'all'] as const).map((value) => (
                      <Button
                        key={value}
                        size="sm"
                        variant={window === value ? 'secondary' : 'ghost'}
                        onClick={() => narrow(search, value)}
                      >
                        {value === '7d' ? '7 days' : value === '30d' ? '30 days' : 'All time'}
                      </Button>
                    ))}
                  </div>
                </div>

                {problem && (
                  <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg border p-3 text-sm">
                    {problem} Sharing remains off.
                    {(problem.includes('Settings') || problem.includes('backend')) && (
                      <Button className="mt-2 block" variant="outline" size="sm" onClick={onOpenSettings}>
                        Open Local AI settings
                      </Button>
                    )}
                  </div>
                )}

                {candidatesError && (
                  <div
                    role="alert"
                    className="bg-warning-bg text-warning-fg flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 text-sm"
                  >
                    {candidatesError}
                    <Button variant="outline" size="sm" onClick={onRetryCandidates}>
                      Retry loading sessions
                    </Button>
                  </div>
                )}

                <div className="flex items-center justify-between gap-3 text-sm">
                  <span>
                    {selectedCandidates.length
                      ? `${selectedCandidates.length} / ${MAX_SHARE_ITEMS} selected`
                      : 'Nothing selected'}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => replaceSelection(toggleCandidateGroup(currentSelection, visible))}
                    disabled={candidatesLoading || candidatesError != null || supportedVisible.length === 0}
                  >
                    {supportedVisible.length > 0 &&
                    supportedVisible.every(({ session }) => currentSelection.has(localSessionId(session)))
                      ? 'Clear filtered'
                      : 'Select all filtered'}
                  </Button>
                </div>

                {selectedCandidates.some(({ session }) => isLocalSession(session)) && (
                  <p className="text-coslash-muted text-xs" role="note">
                    Before preparing a selected Local session, coSlash will use {synthesisBackend} (
                    {synthesisModel}) to generate any missing current debrief. This may consume your selected
                    CLI account’s usage. Generation does not approve an upload.
                  </p>
                )}

                <div className="min-h-0 flex-1 overflow-y-auto rounded-lg border">
                  {!candidatesLoading && candidatesError == null && groups.length === 0 && (
                    <div className="text-coslash-muted p-8 text-center text-sm">
                      {search.trim()
                        ? 'No sessions match this filter.'
                        : 'No eligible sessions are available in this time window.'}
                    </div>
                  )}
                  {!candidatesLoading &&
                    candidatesError == null &&
                    groups.map(([repository, rows]) => {
                      const supportedRows = rows.filter(isCompleteBackupCandidate);
                      const allSelected =
                        supportedRows.length > 0 &&
                        supportedRows.every(({ session }) => currentSelection.has(localSessionId(session)));
                      return (
                        <section key={repository} className="border-b last:border-b-0">
                          <div className="bg-coslash-soft flex items-center justify-between gap-3 px-3 py-2">
                            <span className="font-mono text-xs font-semibold">{repository}</span>
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => replaceSelection(toggleCandidateGroup(currentSelection, rows))}
                              disabled={supportedRows.length === 0}
                            >
                              {allSelected ? 'Clear repository' : 'Select repository'}
                            </Button>
                          </div>
                          {rows.map((candidate) => {
                            const key = localSessionId(candidate.session);
                            const supported = isCompleteBackupCandidate(candidate);
                            return (
                              <label
                                key={key}
                                className={
                                  supported
                                    ? 'hover:bg-coslash-soft flex cursor-pointer items-start gap-3 border-t px-3 py-3 first:border-t-0'
                                    : 'bg-coslash-soft flex cursor-not-allowed items-start gap-3 border-t px-3 py-3 first:border-t-0'
                                }
                              >
                                <input
                                  type="checkbox"
                                  className="mt-1 size-4"
                                  checked={currentSelection.has(key)}
                                  disabled={
                                    !supported ||
                                    (!currentSelection.has(key) && currentSelection.size >= MAX_SHARE_ITEMS)
                                  }
                                  onChange={() => {
                                    if (supported) replaceSelection(toggleCandidate(currentSelection, key));
                                  }}
                                />
                                <span className="min-w-0 flex-1">
                                  <span className="block truncate text-sm font-medium">
                                    {candidate.session.name ?? candidate.session.id}
                                  </span>
                                  <span className="text-coslash-muted block truncate pt-0.5 text-xs">
                                    {candidate.session.sourceLabel} · {candidate.session.agent} ·{' '}
                                    {candidate.session.branch ?? 'no branch'} · revision{' '}
                                    {sessionRevision(candidate.session)}
                                  </span>
                                  <CompleteBackupSupport candidate={candidate} />
                                </span>
                                {candidate.previouslyShared && (
                                  <Badge variant="secondary">Previously shared · re-share</Badge>
                                )}
                              </label>
                            );
                          })}
                        </section>
                      );
                    })}
                </div>
              </>
            )}

            {phase === 'preparing' && (
              <div
                className="min-h-0 flex-1 overflow-y-auto rounded-lg border p-3"
                role="status"
                aria-live="polite"
              >
                <p className="text-sm font-semibold">
                  Preparing complete backups: {preparedCount} of {selectedCandidates.length} ready
                </p>
                <ul className="pt-3 text-sm">
                  {selectedCandidates.map(({ session }) => (
                    <li key={localSessionId(session)} className="flex justify-between gap-3 border-t py-2">
                      <span className="min-w-0 truncate">{session.name ?? session.id}</span>
                      <span>{preparationProgress[localSessionId(session)] ?? 'Queued'}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {phase === 'uploading' && (
              <BackupUploadProgress
                items={uploadRecords.map((record) => ({
                  id: record.item.localSessionId,
                  label: record.candidate.session.name ?? record.candidate.session.id,
                  state: progress[record.item.localSessionId] ?? 'queued',
                }))}
              />
            )}

            {phase === 'review' && (
              <div className="min-h-0 flex-1 overflow-y-auto">
                <div className="bg-warning-bg text-warning-fg rounded-lg border p-3 text-sm">
                  Review binds each complete-backup hash, exact byte count, destination, audience version, and
                  advertised capacity to {destination.workspaceName}. Any change requires a new review.
                </div>
                <CompleteBackupDisclosure workspaceName={destination.workspaceName} />
                <div className="mt-3 space-y-3">
                  {records.map((record) => (
                    <details
                      key={record.item.localSessionId}
                      data-testid="complete-backup-review-item"
                      className="rounded-lg border p-3"
                      open={records.length === 1}
                    >
                      <summary className="cursor-pointer text-sm font-semibold">
                        {record.candidate.session.name ?? record.candidate.session.id} ·{' '}
                        {record.preview.coverage.totalBytes.toLocaleString()} bytes ·{' '}
                        {record.preview.coverage.artifactCount} artifacts
                      </summary>
                      <div className="text-coslash-muted mt-2 font-mono text-xs break-all">
                        {record.item.consent.completeBackupSha256}
                      </div>
                      <dl className="mt-3 grid gap-2 text-xs sm:grid-cols-2">
                        {record.preview.coverage.artifactCounts.map(({ kind, count }) => (
                          <div
                            key={kind}
                            className="bg-coslash-soft flex justify-between gap-3 rounded-md p-2"
                          >
                            <dt>{kind}</dt>
                            <dd className="font-mono">{count}</dd>
                          </div>
                        ))}
                        <div className="bg-coslash-soft flex justify-between gap-3 rounded-md p-2">
                          <dt>Per-backup capacity</dt>
                          <dd className="font-mono">
                            {record.item.consent.maxBackupBytes.toLocaleString()} bytes
                          </dd>
                        </div>
                        <div className="bg-coslash-soft flex justify-between gap-3 rounded-md p-2">
                          <dt>Workspace capacity</dt>
                          <dd className="font-mono">
                            {record.item.consent.backupWorkspaceBytes.toLocaleString()} bytes
                          </dd>
                        </div>
                      </dl>
                      {record.candidate.session.synthesis && (
                        <div className="pt-3 text-xs">
                          <div className="font-semibold">Local AI debrief included</div>
                          <div className="pt-2">Goals</div>
                          <ol className="list-inside list-decimal">
                            {record.candidate.session.synthesis.goals.map((goal, index) => (
                              <li key={index}>{goal}</li>
                            ))}
                          </ol>
                          <div className="pt-2">Outcome: {record.candidate.session.synthesis.outcome}</div>
                          <div className="pt-2">Key decisions</div>
                          <ol className="list-inside list-decimal">
                            {record.candidate.session.synthesis.keyDecisions.map((decision, index) => (
                              <li key={index}>{decision}</li>
                            ))}
                          </ol>
                          <div className="pt-2">Next step: {record.candidate.session.synthesis.nextStep}</div>
                        </div>
                      )}
                    </details>
                  ))}
                </div>
                <label className="mt-4 flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm">
                  <input
                    type="checkbox"
                    className="mt-0.5 size-4"
                    checked={reviewed}
                    onChange={(event) => setReviewed(event.target.checked)}
                  />
                  <span>
                    I approve these exact, unredacted complete {records.length === 1 ? 'backup' : 'backups'}{' '}
                    for {destination.workspaceName} and its {destination.currentMemberCount}{' '}
                    {destination.currentMemberCount === 1 ? 'active member' : 'active members'}.
                  </span>
                </label>
              </div>
            )}

            {phase === 'result' && result && (
              <div className="min-h-0 flex-1 overflow-y-auto">
                {problem && (
                  <div
                    role="alert"
                    className="bg-warning-bg text-warning-fg mb-3 rounded-lg border p-3 text-sm"
                  >
                    {problem}
                  </div>
                )}
                <div
                  data-testid="share-result-summary"
                  role={failedCount > 0 ? 'alert' : 'status'}
                  aria-atomic="true"
                  className={
                    summary?.tone === 'success'
                      ? 'bg-success-bg text-success-fg rounded-lg border p-4'
                      : 'bg-warning-bg text-warning-fg rounded-lg border p-4'
                  }
                >
                  <div className="flex items-center gap-2 font-semibold">
                    {summary?.tone === 'success' ? (
                      <CheckIcon className="size-4" />
                    ) : (
                      <AlertTriangleIcon className="size-4" />
                    )}
                    {summary?.title}
                  </div>
                  <p className="pt-2 text-sm">{summary?.detail}</p>
                </div>
                <div className="mt-3 rounded-lg border">
                  {result.results.map((item, index) => (
                    <div
                      key={item.localSessionId}
                      data-testid="complete-backup-result-item"
                      className="border-b p-3 text-sm last:border-b-0"
                    >
                      <div className="flex items-center justify-between gap-3">
                        <span className="min-w-0 truncate font-semibold">
                          {resultLabels[item.localSessionId] ?? `Session ${index + 1}`}
                        </span>
                        <Badge variant="secondary">
                          {item.state === 'failed'
                            ? !item.error.retryable
                              ? 'Cannot retry'
                              : RETRY_RULES[item.error.code].renewedReview
                                ? 'New review required'
                                : 'Same-key retry available'
                            : item.state === 'private'
                              ? 'Completed privately'
                              : item.state === 'already_accepted'
                                ? `Already shared with ${resultWorkspaceNames[item.localSessionId] ?? destination?.workspaceName ?? 'the workspace'}`
                                : `Shared with ${resultWorkspaceNames[item.localSessionId] ?? destination?.workspaceName ?? 'the workspace'}`}
                        </Badge>
                      </div>
                      {item.state === 'failed' ? (
                        <div className="text-coslash-muted mt-2 space-y-1 text-xs">
                          <p>{RETRY_RULES[item.error.code].reason}</p>
                          <p>Next step: {RETRY_RULES[item.error.code].action}</p>
                        </div>
                      ) : item.state === 'private' ? (
                        <div className="text-coslash-muted mt-2 space-y-1 text-xs">
                          <p>{item.sharingNotice}</p>
                          <p>
                            This backup is not visible to members of {destination.workspaceName}. Open Hub to
                            review it, then explicitly share it if you choose.
                          </p>
                        </div>
                      ) : null}
                    </div>
                  ))}
                </div>
                {route && (
                  <div className="mt-3 rounded-lg border p-3 text-sm">
                    <div className="flex items-center gap-2 font-semibold">
                      <ExternalLinkIcon className="size-4" /> Canonical Hub handoff
                    </div>
                    <div className="text-coslash-muted mt-2 font-mono text-xs break-all">{route.path}</div>
                    {fixtureMode || !destinationResult.hubUrl ? (
                      <p className="text-coslash-muted mt-2 text-xs">Fixture mode stays local.</p>
                    ) : (
                      <a
                        className="text-info-fg mt-3 inline-flex items-center gap-2 text-sm font-semibold underline"
                        href={hubRouteURL(destinationResult.hubUrl, route.path)}
                        target="_blank"
                        rel="noreferrer"
                      >
                        Open in Team Hub <ExternalLinkIcon className="size-4" />
                      </a>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          {phase === 'select' && destinationResult.state === 'ready' && (
            <Button
              onClick={reviewExactPayloads}
              disabled={candidatesLoading || candidatesError != null || selectedCandidates.length === 0}
            >
              {problem ? 'Retry debrief and review' : 'See what gets shared'}
            </Button>
          )}
          {phase === 'review' && destinationResult.state === 'ready' && (
            <>
              <Button variant="outline" onClick={() => setPhase('select')}>
                Back to selection
              </Button>
              <Button onClick={submitReviewed} disabled={!reviewed || uploadPending}>
                {fixtureMode ? 'Exercise fixture result' : 'Approve and upload complete backup'}
              </Button>
            </>
          )}
          {phase === 'result' && result && retryPlan != null && retryCount > 0 && (
            <Button onClick={retryFailed} disabled={retryWait > 0}>
              {retryWait > 0
                ? `Retry in ${retryWait}s`
                : retryPlan.renewedReview.size > 0
                  ? 'Review failed sessions again'
                  : 'Retry failed with same key'}
            </Button>
          )}
          {phase === 'result' && result && failedCount > 0 && retryCount === 0 && (
            <Button onClick={restartFailed}>Back to selection</Button>
          )}
          {phase === 'result' && result && failedCount === 0 && (
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Done
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
