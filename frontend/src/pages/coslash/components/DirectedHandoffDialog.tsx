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
import { apiFetch } from '@/pages/coslash/lib/api';
import { handoffTargetsPath, type HandoffTarget } from '@/pages/coslash/lib/directed-handoff';
import type { SessionIdentity } from '@/pages/coslash/lib/session';

export function DirectedHandoffDialog({
  source,
  disabledHint,
  onStarted,
}: {
  source: SessionIdentity;
  disabledHint?: string;
  onStarted: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<1 | 2>(1);
  const [targets, setTargets] = useState<HandoffTarget[]>([]);
  const [target, setTarget] = useState('');
  const [kind, setKind] = useState<'review' | 'custom'>('review');
  const [request, setRequest] = useState('');
  const [loading, setLoading] = useState(false);
  const [retryKey, setRetryKey] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const targetsPath = handoffTargetsPath(source);

  /* oxlint-disable react/set-state-in-effect -- synchronize target availability with the open dialog */
  useEffect(() => {
    if (!open) return;
    let active = true;
    setLoading(true);
    setTargets([]);
    setError(null);
    void apiFetch(targetsPath)
      .then(async (response) => {
        if (!response.ok) throw new Error((await response.text()).trim() || 'Could not load agents');
        return (await response.json()) as { targets: HandoffTarget[] };
      })
      .then(({ targets: available }) => {
        if (!active) return;
        setTargets(available);
        setTarget(available[0]?.id ?? '');
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
  }, [open, targetsPath, retryKey]);
  /* oxlint-enable react/set-state-in-effect */

  const changeOpen = (next: boolean) => {
    if (submitting) return;
    setOpen(next);
    if (!next) {
      setStep(1);
      setKind('review');
      setRequest('');
      setError(null);
    }
  };

  const launch = async () => {
    if (submitting || target === '' || (kind === 'custom' && request.trim() === '')) return;
    setSubmitting(true);
    setError(null);
    try {
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
      setOpen(false);
      setStep(1);
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
          variant="outline"
          className="w-fit p-2 text-xs"
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
          <DialogTitle>{step === 1 ? 'Choose destination agent' : 'Choose handoff type'}</DialogTitle>
          <DialogDescription>
            {step === 1
              ? 'Start a fresh agent session on the same host and in the same working directory.'
              : `Send a request to ${targets.find((option) => option.id === target)?.label ?? 'the agent'}.`}
          </DialogDescription>
        </DialogHeader>
        {step === 1 ? (
          <div className="flex flex-col gap-2">
            {loading ? (
              <span className="text-coslash-muted text-sm">Finding installed agents...</span>
            ) : error ? (
              <Button variant="outline" className="w-fit" onClick={() => setRetryKey((key) => key + 1)}>
                Retry finding agents
              </Button>
            ) : targets.length === 0 ? (
              <span className="text-coslash-muted text-sm">
                No supported agents are installed on this host.
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
                setError(null);
                setStep(2);
              }}
              disabled={loading || target === ''}
            >
              Next
            </Button>
          ) : (
            <Button
              onClick={() => void launch()}
              disabled={submitting || (kind === 'custom' && request.trim() === '')}
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
