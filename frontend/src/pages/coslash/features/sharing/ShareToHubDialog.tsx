import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangleIcon, CheckIcon, ExternalLinkIcon, SearchIcon, ShieldCheckIcon } from 'lucide-react';
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
import { apiFetch } from '@/pages/coslash/lib/api';
import {
  canonicalPayloadText,
  formatCanonicalJson,
  isSnapshotPreview,
  previewRequestPath,
  type SnapshotPreview,
} from '@/pages/coslash/lib/preview';
import {
  beginHubPairing,
  pollHubPairing,
  submitHubShare,
  type HubDestinationResult,
  type PairingResult,
} from './api';
import {
  bindPreviewConsent,
  consentStillCurrent,
  filterShareCandidates,
  HUB_SHARE_VERSION,
  localSessionId,
  planShareRetry,
  primarySuccessRoute,
  reconcileVisibleSelection,
  RETRY_RULES,
  toggleCandidate,
  toggleCandidateGroup,
  type DestinationResult,
  type ShareCandidate,
  type ShareItemRequest,
  type ShareResult,
  type ShareWindow,
} from './model';

type ReviewRecord = {
  candidate: ShareCandidate;
  preview: SnapshotPreview;
  item: ShareItemRequest;
  payload: string;
};

type Phase = 'select' | 'loading' | 'review' | 'result';

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

export function ShareToHubDialog({
  open,
  onOpenChange,
  candidates,
  destinationResult,
  onOpenSettings,
  onDestinationRefresh,
  fixtureMode = false,
  fixtureOutcome = 'success',
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  candidates: ShareCandidate[];
  destinationResult: HubDestinationResult;
  onOpenSettings: () => void;
  onDestinationRefresh: () => Promise<HubDestinationResult>;
  fixtureMode?: boolean;
  fixtureOutcome?: 'success' | 'partial';
}) {
  const [search, setSearch] = useState('');
  const [window, setWindow] = useState<ShareWindow>('7d');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [phase, setPhase] = useState<Phase>('select');
  const [records, setRecords] = useState<ReviewRecord[]>([]);
  const [reviewed, setReviewed] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [result, setResult] = useState<ShareResult | null>(null);
  const [fixtureAttempt, setFixtureAttempt] = useState(0);
  const [pairing, setPairing] = useState<PairingResult | null>(null);
  const [pairingError, setPairingError] = useState<string | null>(null);
  const previewGeneration = useRef(0);

  const visible = useMemo(
    () => filterShareCandidates(candidates, search, window),
    [candidates, search, window],
  );
  const selectedCandidates = candidates.filter(({ session }) => selected.has(localSessionId(session)));
  const currentSelection = new Set(selectedCandidates.map(({ session }) => localSessionId(session)));
  const destination = destinationResult.state === 'ready' ? destinationResult.destination : null;
  const reviewStillCurrent =
    destination != null &&
    records.every((record) => {
      const current = candidates.find(
        ({ session }) => localSessionId(session) === record.item.localSessionId,
      );
      return (
        current != null && consentStillCurrent(record.item, current.session, record.preview, destination)
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

  useEffect(() => {
    if (open) return;
    previewGeneration.current += 1;
    setSearch('');
    setWindow('7d');
    setSelected(new Set());
    setPhase('select');
    setRecords([]);
    setReviewed(false);
    setProblem(null);
    setResult(null);
    setFixtureAttempt(0);
    setPairing(null);
    setPairingError(null);
  }, [open]);

  useEffect(() => {
    setSelected((current) => {
      const next = reconcileVisibleSelection(current, candidates);
      return next.size === current.size ? current : next;
    });
  }, [candidates]);

  useEffect(() => {
    const pairingId = pairing?.pairingId;
    if (!open || fixtureMode || pairing?.state !== 'pending' || !pairingId) return;
    let stopped = false;
    const interval = globalThis.setInterval(
      async () => {
        try {
          const next = await pollHubPairing(pairingId);
          if (stopped) return;
          setPairing((current) => ({ ...current, ...next }));
          if (next.state === 'paired') {
            globalThis.clearInterval(interval);
            await onDestinationRefresh();
          }
        } catch (error) {
          if (!stopped) {
            setPairingError(error instanceof Error ? error.message : 'Device pairing could not finish.');
          }
        }
      },
      Math.max(2, pairing.intervalSeconds ?? 2) * 1000,
    );
    return () => {
      stopped = true;
      globalThis.clearInterval(interval);
    };
  }, [fixtureMode, onDestinationRefresh, open, pairing?.intervalSeconds, pairing?.pairingId, pairing?.state]);

  useEffect(() => {
    if (phase !== 'review' || records.length === 0 || reviewStillCurrent) return;
    setRecords([]);
    setReviewed(false);
    setProblem('The source revision or destination changed. Review the current selection again.');
    setPhase('select');
  }, [phase, records.length, reviewStillCurrent]);

  const replaceSelection = (next: Set<string>) => {
    setSelected(next);
    setRecords((current) => current.filter((record) => next.has(record.item.localSessionId)));
    setReviewed(false);
    setProblem(null);
    setResult(null);
    setPhase('select');
    setFixtureAttempt(0);
  };

  const narrow = (nextSearch: string, nextWindow: ShareWindow) => {
    const nextVisible = filterShareCandidates(candidates, nextSearch, nextWindow);
    replaceSelection(reconcileVisibleSelection(currentSelection, nextVisible));
    setSearch(nextSearch);
    setWindow(nextWindow);
  };

  const reviewExactPayloads = async () => {
    if (destination == null || selectedCandidates.length === 0) return;
    const generation = ++previewGeneration.current;
    const prior = new Map(records.map((record) => [record.item.localSessionId, record]));
    setPhase('loading');
    setProblem(null);
    try {
      const nextRecords = await Promise.all(
        selectedCandidates.map(async (candidate) => {
          const { session } = candidate;
          const existing = prior.get(localSessionId(session));
          if (existing && consentStillCurrent(existing.item, session, existing.preview, destination)) {
            return existing;
          }
          const response = await apiFetch(previewRequestPath(session.id, session.mtime));
          if (!response.ok) throw new Error(`Preview request failed (${response.status}).`);
          const value: unknown = await response.json();
          if (!isSnapshotPreview(value)) throw new Error('Preview response is outside snapshot-preview/v1.');
          const preview: SnapshotPreview = value;
          const item = bindPreviewConsent(
            session,
            preview,
            destination,
            `${HUB_SHARE_VERSION}:${crypto.randomUUID()}`,
          );
          return { candidate, preview, item, payload: formatCanonicalJson(canonicalPayloadText(preview)) };
        }),
      );
      if (generation !== previewGeneration.current) return;
      setRecords(nextRecords);
      setReviewed(false);
      setPhase('review');
    } catch (error) {
      if (generation !== previewGeneration.current) return;
      setProblem(error instanceof Error ? error.message : 'The exact preview could not be built.');
      setPhase('select');
    }
  };

  const exerciseFixtureResult = () => {
    if (!reviewed || records.length === 0 || !reviewStillCurrent) return;
    const partial = fixtureOutcome === 'partial' && fixtureAttempt === 0 && records.length > 1;
    const results: ShareResult['results'] = records.map((record, index) => {
      if (partial && index === records.length - 1) {
        return {
          localSessionId: record.item.localSessionId,
          idempotencyKey: record.item.idempotencyKey,
          state: 'failed',
          deduplicated: false,
          error: { code: 'temporary_unavailable', retryable: true, retryAfterSeconds: 5 },
        };
      }
      const suffix = String(index + 1).padStart(12, '0');
      const repositoryId = `80000000-0000-4000-8000-${suffix}`;
      const alreadyAccepted = record.candidate.previouslyShared;
      return {
        localSessionId: record.item.localSessionId,
        idempotencyKey: record.item.idempotencyKey,
        state: alreadyAccepted ? 'already_accepted' : 'accepted',
        sessionId: `60000000-0000-4000-8000-${suffix}`,
        revisionId: `70000000-0000-4000-8000-${suffix}`,
        deduplicated: alreadyAccepted,
        sharedAt: '2026-08-18T18:00:00Z',
        briefState: 'pending',
        route: {
          hubContractVersion: 'hub-read/v1',
          repositoryId,
          canonicalWeekStart: '2026-08-17',
          path: `/repos/${repositoryId}/sessions/2026-08-17`,
        },
      };
    });
    setResult({
      contractVersion: HUB_SHARE_VERSION,
      requestId: crypto.randomUUID(),
      state: partial ? 'partial' : 'succeeded',
      results,
    });
    setPhase('result');
  };

  const submitReviewed = async () => {
    if (fixtureMode) {
      exerciseFixtureResult();
      return;
    }
    if (!reviewed || records.length === 0 || !reviewStillCurrent) return;
    setPhase('loading');
    setProblem(null);
    try {
      const response = await submitHubShare({
        contractVersion: HUB_SHARE_VERSION,
        requestId: crypto.randomUUID(),
        items: records.map((record) => record.item),
      });
      setResult(response);
      setPhase('result');
    } catch (error) {
      setProblem(error instanceof Error ? error.message : 'The Hub share request failed.');
      setPhase('review');
    }
  };

  const beginPairing = async () => {
    setPairingError(null);
    try {
      const next = await beginHubPairing();
      setPairing(next);
      const target = next.verificationUriComplete || next.verificationUri;
      if (target) globalThis.open(target, '_blank', 'noopener,noreferrer');
    } catch (error) {
      setPairingError(error instanceof Error ? error.message : 'Device pairing could not start.');
    }
  };

  const retryPartial = () => {
    if (!result) return;
    const plan = planShareRetry(result);
    const retry = new Set([...plan.unchanged, ...plan.renewedReview]);
    if (retry.size === 0) return;
    setSelected(retry);
    setRecords((current) => current.filter((record) => plan.unchanged.has(record.item.localSessionId)));
    setReviewed(plan.renewedReview.size === 0);
    setProblem(
      plan.renewedReview.size > 0
        ? 'The failed sessions require a refreshed preview and explicit approval.'
        : null,
    );
    setResult(null);
    setFixtureAttempt((current) => current + 1);
    setPhase(plan.renewedReview.size > 0 ? 'select' : 'review');
  };

  const eligibility = destinationResult.state === 'ready' ? null : ELIGIBILITY_COPY[destinationResult.state];
  const route = result ? primarySuccessRoute(result) : null;
  const retryPlan = result ? planShareRetry(result) : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100vh-2rem)] w-[min(56rem,calc(100vw-2rem))] max-w-none! flex-col overflow-hidden">
        <DialogHeader>
          <div className="flex items-center gap-2">
            <DialogTitle>Share to Hub</DialogTitle>
            <Badge variant="secondary">
              {fixtureMode ? 'FIXTURE BUILD · NO UPLOAD' : 'LIVE · EXPLICIT APPROVAL'}
            </Badge>
          </div>
          <DialogDescription>
            {fixtureMode
              ? "Select sessions, review C2's exact canonical bytes, and exercise the T2-I4 retry/result states."
              : "Select sessions, review C2's exact canonical bytes, then upload only the revisions you approve."}
          </DialogDescription>
        </DialogHeader>

        {destination == null ? (
          <div
            role="status"
            className="flex min-h-72 flex-col items-center justify-center rounded-xl border p-8 text-center"
          >
            <AlertTriangleIcon className="text-warning-fg size-7" />
            <h3 className="mt-3 font-semibold">{eligibility?.title}</h3>
            <p className="text-muted-foreground mt-2 max-w-md text-sm">{eligibility?.detail}</p>
            {!fixtureMode && pairing?.state === 'pending' ? (
              <div className="mt-5 rounded-lg border p-4">
                <p className="text-sm font-semibold">Approve code {pairing.userCode}</p>
                <p className="text-muted-foreground pt-1 text-xs">
                  A Hub sign-in window was opened. This page will update after approval.
                </p>
                {pairing.verificationUriComplete && (
                  <a
                    className="text-info-fg mt-3 inline-block text-sm font-semibold underline"
                    href={pairing.verificationUriComplete}
                    target="_blank"
                    rel="noreferrer"
                  >
                    Open approval page
                  </a>
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
              <p className="text-destructive mt-3 text-sm" role="alert">
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
                    <SearchIcon className="text-muted-foreground pointer-events-none absolute top-2 left-2.5 size-4" />
                    <Input
                      aria-label="Filter shareable sessions"
                      className="pl-8"
                      placeholder="Filter sessions"
                      value={search}
                      onChange={(event) => narrow(event.target.value, window)}
                    />
                  </div>
                  <div className="bg-muted flex rounded-lg p-1" aria-label="Share time window">
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
                  </div>
                )}

                <div className="flex items-center justify-between gap-3 text-sm">
                  <span>
                    {selectedCandidates.length ? `${selectedCandidates.length} selected` : 'Nothing selected'}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => replaceSelection(toggleCandidateGroup(currentSelection, visible))}
                    disabled={visible.length === 0}
                  >
                    {visible.length > 0 &&
                    visible.every(({ session }) => currentSelection.has(localSessionId(session)))
                      ? 'Clear filtered'
                      : 'Select all filtered'}
                  </Button>
                </div>

                <div className="min-h-0 flex-1 overflow-y-auto rounded-lg border">
                  {groups.length === 0 && (
                    <div className="text-muted-foreground p-8 text-center text-sm">
                      No sessions match this filter.
                    </div>
                  )}
                  {groups.map(([repository, rows]) => {
                    const allSelected = rows.every(({ session }) =>
                      currentSelection.has(localSessionId(session)),
                    );
                    return (
                      <section key={repository} className="border-b last:border-b-0">
                        <div className="bg-muted/60 flex items-center justify-between gap-3 px-3 py-2">
                          <span className="font-mono text-xs font-semibold">{repository}</span>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => replaceSelection(toggleCandidateGroup(currentSelection, rows))}
                          >
                            {allSelected ? 'Clear repository' : 'Select repository'}
                          </Button>
                        </div>
                        {rows.map((candidate) => {
                          const key = localSessionId(candidate.session);
                          return (
                            <label
                              key={key}
                              className="hover:bg-muted/30 flex cursor-pointer items-start gap-3 border-t px-3 py-3 first:border-t-0"
                            >
                              <input
                                type="checkbox"
                                className="mt-1 size-4"
                                checked={currentSelection.has(key)}
                                onChange={() => replaceSelection(toggleCandidate(currentSelection, key))}
                              />
                              <span className="min-w-0 flex-1">
                                <span className="block truncate text-sm font-medium">
                                  {candidate.session.name ?? candidate.session.id}
                                </span>
                                <span className="text-muted-foreground block truncate pt-0.5 text-xs">
                                  {candidate.session.agent} · {candidate.session.branch ?? 'no branch'} ·
                                  revision {candidate.session.mtime}
                                </span>
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

            {phase === 'loading' && (
              <div role="status" className="grid min-h-72 place-items-center text-sm">
                {records.length > 0 && !fixtureMode
                  ? 'Uploading approved canonical snapshots…'
                  : 'Building exact canonical previews…'}
              </div>
            )}

            {phase === 'review' && (
              <div className="min-h-0 flex-1 overflow-y-auto">
                <div className="bg-warning-bg text-warning-fg rounded-lg border p-3 text-sm">
                  Review binds these exact source revisions and payload hashes to {destination.workspaceName}.
                  Any change requires a new review.
                </div>
                <div className="mt-3 space-y-3">
                  {records.map((record) => (
                    <details
                      key={record.item.localSessionId}
                      className="rounded-lg border p-3"
                      open={records.length === 1}
                    >
                      <summary className="cursor-pointer text-sm font-semibold">
                        {record.candidate.session.name ?? record.candidate.session.id} ·{' '}
                        {record.preview.payloadBytes?.toLocaleString()} bytes
                      </summary>
                      <div className="text-muted-foreground mt-2 font-mono text-xs break-all">
                        {record.item.consent.contentHash}
                      </div>
                      <pre className="bg-muted mt-3 max-h-72 overflow-auto rounded-lg border p-3 font-mono text-[11px] leading-relaxed whitespace-pre">
                        {record.payload}
                      </pre>
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
                    I approve these exact {records.length} {records.length === 1 ? 'revision' : 'revisions'}{' '}
                    for {destination.workspaceName} and its {destination.currentMemberCount}{' '}
                    {destination.currentMemberCount === 1 ? 'member' : 'members'}.
                  </span>
                </label>
              </div>
            )}

            {phase === 'result' && result && (
              <div className="min-h-0 flex-1 overflow-y-auto">
                <div
                  className={
                    result.state === 'succeeded'
                      ? 'bg-success-bg text-success-fg rounded-lg border p-4'
                      : 'bg-warning-bg text-warning-fg rounded-lg border p-4'
                  }
                >
                  <div className="flex items-center gap-2 font-semibold">
                    {result.state === 'succeeded' ? (
                      <CheckIcon className="size-4" />
                    ) : (
                      <AlertTriangleIcon className="size-4" />
                    )}
                    {result.state === 'succeeded'
                      ? fixtureMode
                        ? 'Fixture share accepted'
                        : 'Share accepted'
                      : fixtureMode
                        ? 'Fixture batch partially accepted'
                        : 'Share partially accepted'}
                  </div>
                  <p className="pt-2 text-sm">
                    Accepted uploads are visible immediately; their brief remains pending. No synthesis
                    completion is claimed.
                  </p>
                </div>
                <div className="mt-3 rounded-lg border">
                  {result.results.map((item) => (
                    <div
                      key={item.localSessionId}
                      className="flex items-center justify-between gap-3 border-b p-3 text-sm last:border-b-0"
                    >
                      <span className="min-w-0 truncate font-mono text-xs">{item.localSessionId}</span>
                      {item.state === 'failed' ? (
                        <Badge variant="secondary">
                          {item.error.code} ·{' '}
                          {!item.error.retryable || !RETRY_RULES[item.error.code].retryable
                            ? 'not retryable'
                            : RETRY_RULES[item.error.code].renewedReview
                              ? 'review required'
                              : 'retry preserved'}
                        </Badge>
                      ) : (
                        <Badge variant="secondary">
                          {item.state} · brief {item.briefState}
                        </Badge>
                      )}
                    </div>
                  ))}
                </div>
                {route && (
                  <div className="mt-3 rounded-lg border p-3 text-sm">
                    <div className="flex items-center gap-2 font-semibold">
                      <ExternalLinkIcon className="size-4" /> Canonical Hub handoff
                    </div>
                    <div className="text-muted-foreground mt-2 font-mono text-xs break-all">{route.path}</div>
                    {fixtureMode || !destinationResult.hubUrl ? (
                      <p className="text-muted-foreground mt-2 text-xs">
                        The integrated client navigates to this C3-owned route. Fixture mode stays local.
                      </p>
                    ) : (
                      <a
                        className="text-info-fg mt-3 inline-flex items-center gap-2 text-sm font-semibold underline"
                        href={new URL(route.path, destinationResult.hubUrl).toString()}
                        target="_blank"
                        rel="noreferrer"
                      >
                        Open shared sessions <ExternalLinkIcon className="size-4" />
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
            <Button onClick={reviewExactPayloads} disabled={selectedCandidates.length === 0}>
              See what gets shared
            </Button>
          )}
          {phase === 'review' && destinationResult.state === 'ready' && (
            <>
              <Button variant="outline" onClick={() => setPhase('select')}>
                Back to selection
              </Button>
              <Button onClick={submitReviewed} disabled={!reviewed}>
                {fixtureMode ? 'Exercise fixture result' : 'Approve and share'}
              </Button>
            </>
          )}
          {phase === 'result' &&
            result?.state === 'partial' &&
            retryPlan != null &&
            retryPlan.unchanged.size + retryPlan.renewedReview.size > 0 && (
              <Button onClick={retryPartial}>
                {retryPlan.renewedReview.size > 0
                  ? 'Review failed sessions again'
                  : 'Retry failed with same key'}
              </Button>
            )}
          {phase === 'result' && result?.state === 'succeeded' && (
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Done
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
