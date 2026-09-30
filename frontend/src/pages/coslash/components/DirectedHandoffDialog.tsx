import { useEffect, useState } from 'react';
import { LoaderCircleIcon, TerminalIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { launchFreshSession } from '@/pages/coslash/hooks/use-launch-terminal';
import { apiFetch } from '@/pages/coslash/lib/api';
import {
  handoffKindAvailable,
  handoffTargetsPath,
  type HandoffTarget,
} from '@/pages/coslash/lib/directed-handoff';
import { handoffBrief } from '@/pages/coslash/lib/handoff';
import { freshLaunchDisabledHint, isLocalSession, type SessionDetail } from '@/pages/coslash/lib/session';

export function DirectedHandoffDialog({
  source,
  disabledHint,
  onStarted,
}: {
  source: SessionDetail;
  disabledHint?: string;
  onStarted: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<1 | 2>(1);
  const [targets, setTargets] = useState<HandoffTarget[]>([]);
  const [target, setTarget] = useState('');
  const [kind, setKind] = useState<'fresh' | 'review' | 'custom'>('fresh');
  const [request, setRequest] = useState('');
  const [loading, setLoading] = useState(false);
  const [retryKey, setRetryKey] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const targetsPath = handoffTargetsPath(source);
  const freshHint =
    disabledHint ??
    freshLaunchDisabledHint(source) ??
    (!isLocalSession(source) && (source.displayStale || source.launchable === false)
      ? 'This remote session is unavailable for launch.'
      : undefined);

  /* oxlint-disable react/set-state-in-effect -- synchronize target availability with the open dialog */
  useEffect(() => {
    if (!open || step !== 2 || kind === 'fresh') return;
    let active = true;
    setLoading(true);
    setTargets([]);
    setTarget('');
    setError(null);
    void apiFetch(targetsPath)
      .then(async (response) => {
        if (!response.ok) throw new Error((await response.text()).trim() || 'Could not load agents');
        return (await response.json()) as { targets: HandoffTarget[] };
      })
      .then(({ targets: available }) => {
        if (!active) return;
        const supported = available.filter((option) => handoffKindAvailable(option.id, kind));
        setTargets(supported);
        setTarget(supported[0]?.id ?? '');
      })
      .catch((failure: unknown) => {
        if (active) setError(failure instanceof Error ? failure.message : String(failure));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [open, step, kind, targetsPath, retryKey]);
  /* oxlint-enable react/set-state-in-effect */

  const changeOpen = (next: boolean) => {
    if (submitting) return;
    setOpen(next);
    if (!next) {
      setStep(1);
      setKind('fresh');
      setRequest('');
      setError(null);
    }
  };

  const launch = async () => {
    if (
      submitting ||
      (kind === 'fresh' ? freshHint != null : target === '' || !handoffKindAvailable(target, kind)) ||
      (kind === 'custom' && request.trim() === '')
    )
      return;
    setSubmitting(true);
    setError(null);
    try {
      if (kind === 'fresh') {
        await launchFreshSession(source, handoffBrief(source));
      } else {
        const response = await apiFetch('/api/directed-handoffs', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            sourceId: source.sourceId,
            agent: source.agent,
            id: source.id,
            targetAgent: target,
            kind,
            request: kind === 'custom' ? request.trim() : undefined,
          }),
        });
        if (!response.ok)
          throw new Error((await response.text()).trim() || `Handoff failed (${response.status})`);
      }
      setOpen(false);
      setStep(1);
      setKind('fresh');
      setRequest('');
      onStarted();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
      onStarted();
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        <Button
          className="bg-brand text-brand-foreground w-fit p-2 text-xs hover:bg-[color-mix(in_oklch,var(--brand),var(--foreground)_10%)]"
          disabled={disabledHint != null}
          title={disabledHint}
          onClick={(event) => event.currentTarget.blur()}
        >
          <TerminalIcon />
          <span>Start fresh with handoff</span>
        </Button>
      </DialogTrigger>
      <DialogContent className="coslash-shell">
        <DialogHeader>
          <DialogTitle>{step === 1 ? 'Choose handoff type' : 'Choose destination agent'}</DialogTitle>
          <DialogDescription>
            Start a fresh agent session on the same host and in the same working directory.
          </DialogDescription>
        </DialogHeader>
        {step === 2 ? (
          <div className="flex flex-col gap-2">
            {loading ? (
              <span className="text-coslash-muted text-sm">Finding installed agents...</span>
            ) : error && targets.length === 0 ? (
              <Button variant="outline" className="w-fit" onClick={() => setRetryKey((key) => key + 1)}>
                Retry finding agents
              </Button>
            ) : targets.length === 0 ? (
              <span className="text-coslash-muted text-sm">
                No installed agents support this handoff type.
              </span>
            ) : (
              targets.map((option) => (
                <label
                  key={option.id}
                  className="hover:bg-coslash-soft flex cursor-pointer items-center gap-2 rounded-lg border p-3"
                >
                  <input
                    type="radio"
                    name="handoff-target"
                    value={option.id}
                    checked={target === option.id}
                    onChange={() => setTarget(option.id)}
                  />
                  <span className="text-sm font-semibold">{option.label}</span>
                </label>
              ))
            )}
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            <label className="hover:bg-coslash-soft flex cursor-pointer items-start gap-2 rounded-lg border p-3">
              <input
                type="radio"
                name="handoff-kind"
                checked={kind === 'fresh'}
                onChange={() => setKind('fresh')}
              />
              <span>
                <strong className="block text-sm">Fresh handoff</strong>
                <span className="text-coslash-muted text-xs">
                  Open the original agent with handoff notes, ready for your next message.
                </span>
              </span>
            </label>
            <label className="hover:bg-coslash-soft flex cursor-pointer items-start gap-2 rounded-lg border p-3">
              <input
                type="radio"
                name="handoff-kind"
                checked={kind === 'review'}
                onChange={() => setKind('review')}
              />
              <span>
                <strong className="block text-sm">Review</strong>
                <span className="text-coslash-muted text-xs">
                  Review current working tree changes and show findings.
                </span>
              </span>
            </label>
            <label className="hover:bg-coslash-soft flex cursor-pointer items-start gap-2 rounded-lg border p-3">
              <input
                type="radio"
                name="handoff-kind"
                checked={kind === 'custom'}
                onChange={() => setKind('custom')}
              />
              <span>
                <strong className="block text-sm">Custom request</strong>
                <span className="text-coslash-muted text-xs">Give the agent a natural language task.</span>
              </span>
            </label>
            {kind === 'custom' && (
              <label className="text-sm font-medium">
                Request
                <textarea
                  className="border-coslash-line bg-coslash-surface mt-2 min-h-28 w-full rounded-lg border p-3 text-sm"
                  value={request}
                  onChange={(event) => setRequest(event.target.value)}
                  maxLength={16384}
                  placeholder="What should the agent do?"
                />
              </label>
            )}
          </div>
        )}
        {error && (
          <span role="alert" className="text-danger-fg text-xs">
            {error}
          </span>
        )}
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => (step === 1 ? changeOpen(false) : setStep(1))}
            disabled={submitting}
          >
            {step === 1 ? 'Cancel' : 'Back'}
          </Button>
          {step === 1 ? (
            <Button
              onClick={() => {
                if (kind === 'fresh') void launch();
                else {
                  setError(null);
                  setTargets([]);
                  setTarget('');
                  setLoading(true);
                  setStep(2);
                }
              }}
              title={kind === 'fresh' ? freshHint : undefined}
              disabled={
                submitting ||
                (kind === 'fresh' && freshHint != null) ||
                (kind === 'custom' && request.trim() === '')
              }
            >
              {submitting && <LoaderCircleIcon className="animate-spin" />}
              {submitting ? 'Starting' : kind === 'fresh' ? 'Start fresh' : 'Next'}
            </Button>
          ) : (
            <Button
              onClick={() => void launch()}
              disabled={
                submitting ||
                loading ||
                target === '' ||
                kind === 'fresh' ||
                !handoffKindAvailable(target, kind) ||
                (kind === 'custom' && request.trim() === '')
              }
            >
              {submitting && <LoaderCircleIcon className="animate-spin" />}
              {submitting ? 'Starting' : 'Start handoff'}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
