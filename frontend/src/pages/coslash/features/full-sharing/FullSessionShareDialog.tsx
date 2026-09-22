import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangleIcon, CheckIcon, ExternalLinkIcon, ShieldCheckIcon } from 'lucide-react';
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
import { hubRouteURL, type DestinationResult } from '@/pages/coslash/features/sharing/model';
import type { Session } from '@/pages/coslash/lib/session';
import { fetchFullSessionPreview, submitFullSessionShare } from './api';
import {
  bindFullSessionReview,
  fullSessionResultNeedsReview,
  fullSessionReviewStillCurrent,
  fullSessionSelection,
  type FullSessionPreview,
  type FullSessionShareRequest,
  type FullSessionShareResult,
} from './model';

type Phase = 'select' | 'loading' | 'review' | 'result';

const ERROR_COPY: Record<string, string> = {
  invalid_full_session_request: 'The approved upload request is invalid.',
  full_session_invalid: 'The exact revision no longer satisfies the full-session contract.',
  full_session_too_large: 'The exact revision exceeds the Hub upload limit.',
  incompatible_server: 'This Hub does not accept full-session v2 revisions.',
  review_binding_changed: 'The content, destination, or audience changed. A new review is required.',
  idempotency_conflict: 'This retry key is already bound to different content.',
  unauthorized: 'The paired device is no longer authorized for this workspace.',
  rate_limited: 'The Hub is rate limiting uploads. Retry the same approved revision later.',
  network_unavailable: 'The Hub could not be reached. The same approved revision can be retried.',
  timeout: 'The upload outcome could not be confirmed. Retry with the same key.',
  temporary_unavailable: 'The Hub is temporarily unavailable. Retry the same approved revision later.',
};

function formatBytes(value: number): string {
  return `${value.toLocaleString()} bytes`;
}

export function FullSessionShareDialog({
  open,
  onOpenChange,
  candidates,
  destinationResult,
  onDestinationRefresh,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  candidates: Session[];
  destinationResult: DestinationResult;
  onDestinationRefresh: () => Promise<DestinationResult>;
}) {
  const [phase, setPhase] = useState<Phase>('select');
  const [selectedKey, setSelectedKey] = useState('');
  const [preview, setPreview] = useState<FullSessionPreview | null>(null);
  const [request, setRequest] = useState<FullSessionShareRequest | null>(null);
  const [reviewed, setReviewed] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [result, setResult] = useState<FullSessionShareResult | null>(null);
  const attemptGeneration = useRef(0);

  const destination = destinationResult.state === 'ready' ? destinationResult.destination : null;
  const selected = useMemo(
    () => candidates.find((session) => `${session.sourceId}:${session.agent}:${session.id}` === selectedKey),
    [candidates, selectedKey],
  );
  const reviewCurrent =
    destination != null &&
    preview != null &&
    request != null &&
    fullSessionReviewStillCurrent(request, selected, preview, destination);

  const reset = () => {
    setPhase('select');
    setSelectedKey('');
    setPreview(null);
    setRequest(null);
    setReviewed(false);
    setProblem(null);
    setResult(null);
  };

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      attemptGeneration.current += 1;
      reset();
    }
    onOpenChange(nextOpen);
  };

  useEffect(() => {
    if (phase !== 'review' || reviewCurrent) return;
    // oxlint-disable-next-line react/set-state-in-effect -- external session/destination changes must revoke consent immediately
    setPhase('select');
    setPreview(null);
    setRequest(null);
    setReviewed(false);
    setProblem('The content, destination, or audience changed. Review the current binding again.');
  }, [phase, reviewCurrent]);

  const buildReview = async () => {
    if (!selected || !destination) return;
    const generation = ++attemptGeneration.current;
    setPhase('loading');
    setProblem(null);
    try {
      const nextPreview = await fetchFullSessionPreview(fullSessionSelection(selected));
      if (generation !== attemptGeneration.current) return;
      if (nextPreview.state !== 'ready') {
        setProblem(
          nextPreview.problem
            ? `${nextPreview.problem.message} ${nextPreview.problem.action}`
            : 'The full-session preview is not approvable.',
        );
        setPhase('select');
        return;
      }
      const nextRequest = bindFullSessionReview(
        selected,
        nextPreview,
        destination,
        `full-session-share/v1:${crypto.randomUUID()}`,
      );
      setPreview(nextPreview);
      setRequest(nextRequest);
      setReviewed(false);
      setPhase('review');
    } catch (error) {
      if (generation !== attemptGeneration.current) return;
      setProblem(error instanceof Error ? error.message : 'The full-session preview could not be built.');
      setPhase('select');
    }
  };

  const upload = async () => {
    if (!request || !reviewed || !reviewCurrent) return;
    const generation = ++attemptGeneration.current;
    setPhase('loading');
    setProblem(null);
    try {
      const next = await submitFullSessionShare(request);
      if (generation !== attemptGeneration.current) return;
      if (fullSessionResultNeedsReview(next)) {
        try {
          await onDestinationRefresh();
        } catch (error) {
          if (generation !== attemptGeneration.current) return;
          setResult(next);
          setProblem(error instanceof Error ? error.message : 'The Hub destination could not be refreshed.');
          setPhase('result');
          return;
        }
        if (generation !== attemptGeneration.current) return;
        setPreview(null);
        setRequest(null);
        setReviewed(false);
        setResult(null);
        setProblem(ERROR_COPY.review_binding_changed);
        setPhase('select');
        return;
      }
      setResult(next);
      setPhase('result');
    } catch (error) {
      if (generation !== attemptGeneration.current) return;
      setProblem(error instanceof Error ? error.message : 'The full-session upload failed.');
      setPhase('review');
    }
  };

  const retryDestinationRefresh = async () => {
    const generation = ++attemptGeneration.current;
    setProblem(null);
    try {
      await onDestinationRefresh();
      if (generation !== attemptGeneration.current) return;
      setPreview(null);
      setRequest(null);
      setReviewed(false);
      setResult(null);
      setProblem(ERROR_COPY.review_binding_changed);
      setPhase('select');
    } catch (error) {
      if (generation !== attemptGeneration.current) return;
      setProblem(error instanceof Error ? error.message : 'The Hub destination could not be refreshed.');
    }
  };

  const retry = async () => {
    if (!result?.error?.retryable || !request || !reviewCurrent) {
      setPhase('select');
      setReviewed(false);
      return;
    }
    await upload();
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="coslash-shell flex max-h-[calc(100vh-2rem)] w-[calc(100%-2rem)] max-w-none! flex-col overflow-hidden sm:w-[min(64rem,calc(100vw-2rem))]">
        <DialogHeader className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <DialogTitle>Share full revision</DialogTitle>
            <Badge variant="secondary">FULL V2 · EXPLICIT APPROVAL</Badge>
          </div>
          <DialogDescription>
            Review one exact complete record, including every file-change body, before it leaves this device.
          </DialogDescription>
        </DialogHeader>

        {destination == null ? (
          <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg border p-4 text-sm">
            The paired Hub destination is not ready. No full-session content was loaded or uploaded.
          </div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden">
            <div className="bg-info-bg text-info-fg flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3">
              <div>
                <div className="font-semibold">{destination.workspaceName}</div>
                <div className="pt-0.5 text-xs">
                  {destination.currentMemberCount}{' '}
                  {destination.currentMemberCount === 1 ? 'active member' : 'active members'} can read the
                  approved revision
                </div>
              </div>
              <div className="flex items-center gap-1.5 text-xs font-semibold">
                <ShieldCheckIcon className="size-4" /> Exact destination and audience binding
              </div>
            </div>

            {problem && (
              <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg border p-3 text-sm">
                {problem} Sharing remains off.
              </div>
            )}

            {phase === 'select' && (
              <div className="min-h-0 flex-1 overflow-y-auto rounded-lg border">
                {candidates.length === 0 ? (
                  <div className="text-coslash-muted p-8 text-center text-sm">
                    No complete C01-backed SSH revision is currently eligible for full sharing.
                  </div>
                ) : (
                  candidates.map((session) => {
                    const key = `${session.sourceId}:${session.agent}:${session.id}`;
                    return (
                      <label
                        key={key}
                        className="hover:bg-coslash-soft flex cursor-pointer items-start gap-3 border-b p-3 last:border-b-0"
                      >
                        <input
                          type="radio"
                          name="full-session-selection"
                          className="mt-1 size-4"
                          checked={selectedKey === key}
                          onChange={() => {
                            setSelectedKey(key);
                            setProblem(null);
                          }}
                        />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-semibold">
                            {session.name ?? session.id}
                          </span>
                          <span className="text-coslash-muted block truncate pt-1 text-xs">
                            {session.sourceLabel} · {session.repo ?? 'unknown repository'} ·{' '}
                            {session.branch ?? 'no branch'}
                          </span>
                          <span className="text-coslash-muted block truncate pt-1 font-mono text-[11px]">
                            revision {session.fullRevision}
                          </span>
                        </span>
                      </label>
                    );
                  })
                )}
              </div>
            )}

            {phase === 'loading' && (
              <div role="status" className="grid min-h-72 place-items-center text-sm">
                {request
                  ? 'Uploading the approved canonical envelope…'
                  : 'Loading the exact complete revision…'}
              </div>
            )}

            {phase === 'review' && preview?.envelope && request && (
              <div className="min-h-0 flex-1 overflow-y-auto pr-1">
                <div className="bg-warning-bg text-warning-fg rounded-lg border p-3" role="alert">
                  <div className="flex items-center gap-2 font-semibold">
                    <AlertTriangleIcon className="size-4 shrink-0" /> Embedded-secret risk
                  </div>
                  <p className="pt-1 text-sm">
                    Full revisions are not redacted. Prompts, commands, paths, subagent results, and
                    file-change bodies may contain credentials or other secrets. Review all content below.
                  </p>
                </div>
                <dl className="bg-coslash-line mt-3 grid gap-px overflow-hidden rounded-lg border text-sm sm:grid-cols-2">
                  {[
                    ['Destination', destination.workspaceName],
                    [
                      'Audience',
                      `${destination.currentMemberCount} active ${destination.currentMemberCount === 1 ? 'member' : 'members'}`,
                    ],
                    ['Canonical record', formatBytes(preview.recordBytes!)],
                    ['Total upload envelope', formatBytes(preview.payloadBytes!)],
                    ['Repository', preview.envelope.repository.canonical],
                    ['Revision', preview.selection.revisionId],
                  ].map(([label, value]) => (
                    <div key={label} className="bg-coslash-surface min-w-0 p-3">
                      <dt className="text-coslash-muted text-xs font-semibold">{label}</dt>
                      <dd className="mt-1 truncate font-mono text-xs" title={value}>
                        {value}
                      </dd>
                    </div>
                  ))}
                </dl>
                <div className="mt-3 rounded-lg border p-3">
                  <div className="text-xs font-semibold">CANONICAL RECORD SHA-256</div>
                  <div className="text-coslash-muted mt-1 font-mono text-xs break-all">
                    {preview.recordSha256}
                  </div>
                </div>
                <section className="mt-3" aria-labelledby="full-session-content">
                  <h3 id="full-session-content" className="text-xs font-bold tracking-wide">
                    COMPLETE INCLUDED CONTENT
                  </h3>
                  <p className="text-coslash-muted pt-1 text-xs">
                    This formatted canonical envelope includes every section and every ordered file-change
                    body sent to Hub.
                  </p>
                  <pre className="bg-coslash-soft mt-2 max-h-[42vh] max-w-full overflow-auto rounded-lg border p-3 font-mono text-[11px] leading-relaxed whitespace-pre">
                    {JSON.stringify(preview.envelope, null, 2)}
                  </pre>
                </section>
                <label className="mt-3 flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm">
                  <input
                    type="checkbox"
                    className="mt-0.5 size-4"
                    checked={reviewed}
                    onChange={(event) => setReviewed(event.target.checked)}
                  />
                  <span>
                    I reviewed this exact revision, hash, repository, destination,{' '}
                    {destination.currentMemberCount}-member audience, size, and embedded-secret risk. Upload
                    it to {destination.workspaceName}.
                  </span>
                </label>
              </div>
            )}

            {phase === 'result' && result && (
              <div className="min-h-0 flex-1 overflow-y-auto">
                {result.state === 'failed' ? (
                  <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg border p-4">
                    <div className="flex items-center gap-2 font-semibold">
                      <AlertTriangleIcon className="size-4" /> Upload not accepted
                    </div>
                    <p className="pt-2 text-sm">
                      {ERROR_COPY[result.error?.code ?? 'temporary_unavailable']}
                    </p>
                    <p className="pt-1 text-xs">No session content is included in this error.</p>
                  </div>
                ) : (
                  <div className="bg-success-bg text-success-fg rounded-lg border p-4">
                    <div className="flex items-center gap-2 font-semibold">
                      <CheckIcon className="size-4" />{' '}
                      {result.state === 'already_accepted'
                        ? 'Revision already accepted'
                        : 'Full revision accepted'}
                    </div>
                    <p className="pt-2 text-sm">
                      Hub accepted {formatBytes(result.byteCount ?? 0)} with the reviewed content hash.
                    </p>
                  </div>
                )}
                {result.route && destinationResult.hubUrl && (
                  <a
                    className="text-info-fg mt-4 inline-flex items-center gap-2 text-sm font-semibold underline"
                    href={hubRouteURL(destinationResult.hubUrl, result.route.path)}
                    target="_blank"
                    rel="noreferrer"
                  >
                    Open exact revision in Hub <ExternalLinkIcon className="size-4" />
                  </a>
                )}
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          {phase === 'select' && destination && (
            <Button onClick={buildReview} disabled={!selected}>
              Review complete revision
            </Button>
          )}
          {phase === 'review' && (
            <>
              <Button variant="outline" onClick={() => setPhase('select')}>
                Back
              </Button>
              <Button onClick={upload} disabled={!reviewed || !reviewCurrent}>
                Approve and upload full revision
              </Button>
            </>
          )}
          {phase === 'result' && result?.error?.code === 'review_binding_changed' && (
            <Button onClick={retryDestinationRefresh}>Retry destination refresh</Button>
          )}
          {phase === 'result' &&
            result?.state === 'failed' &&
            result.error?.code !== 'review_binding_changed' &&
            (result.error?.retryable ? (
              <Button onClick={retry}>Retry with same approval and key</Button>
            ) : (
              <Button onClick={() => setPhase('select')}>Back to selection</Button>
            ))}
          {phase === 'result' && result?.state !== 'failed' && (
            <Button variant="outline" onClick={() => handleOpenChange(false)}>
              Done
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
