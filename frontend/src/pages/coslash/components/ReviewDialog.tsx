import { useState, type RefObject } from 'react';
import { LoaderCircleIcon, ScanSearchIcon } from 'lucide-react';
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
import { availableReviewers, reviewRequestPath, type ReviewerOption } from '@/pages/coslash/lib/review';
import { type SessionIdentity, type VendorKey } from '@/pages/coslash/lib/session';

export type ReviewLaunchState = 'idle' | 'launching' | 'error';

export function ReviewDialogContent({
  reviewers,
  selected,
  state,
  error,
  onSelect,
  onClose,
  onStart,
}: {
  reviewers: readonly ReviewerOption[];
  selected: VendorKey;
  state: ReviewLaunchState;
  error: string | null;
  onSelect: (reviewer: VendorKey) => void;
  onClose: () => void;
  onStart: () => void;
}) {
  const launching = state === 'launching';
  return (
    <>
      <DialogHeader>
        <DialogTitle>Send for review</DialogTitle>
        <DialogDescription>Choose a CLI reviewer. The review opens as a new session.</DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-2">
        {reviewers.map((reviewer) => (
          <label
            key={reviewer.id}
            className="hover:bg-coslash-soft flex cursor-pointer items-center gap-2 rounded-lg border p-3"
          >
            <input
              type="radio"
              name="reviewer"
              value={reviewer.id}
              checked={selected === reviewer.id}
              onChange={() => onSelect(reviewer.id)}
              disabled={launching}
            />
            <span className="text-sm font-semibold">{reviewer.label}</span>
          </label>
        ))}
      </div>
      {error != null && (
        <div role="alert" className="text-danger-fg text-xs">
          {error}
        </div>
      )}
      <DialogFooter>
        <Button variant="outline" onClick={onClose} disabled={launching}>
          Close
        </Button>
        <Button onClick={onStart} disabled={launching || reviewers.length === 0}>
          {launching && <LoaderCircleIcon className="animate-spin" />}
          {launching ? 'Starting review' : 'Start review'}
        </Button>
      </DialogFooter>
    </>
  );
}

export function ReviewDialog({
  origin,
  reviewerOptions,
  disabled = false,
  active = false,
  reviewError,
  unavailableReason,
  open: controlledOpen,
  onOpenChange,
  showTrigger = true,
  returnFocusRef,
  onStarted,
}: {
  origin: SessionIdentity;
  reviewerOptions: readonly ReviewerOption[];
  disabled?: boolean;
  active?: boolean;
  reviewError?: string;
  unavailableReason?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  showTrigger?: boolean;
  returnFocusRef?: RefObject<HTMLButtonElement | null>;
  onStarted: () => void;
}) {
  const reviewers = availableReviewers(reviewerOptions, origin.agent);
  const [internalOpen, setInternalOpen] = useState(false);
  const [selected, setSelected] = useState<VendorKey>(reviewers[0]?.id ?? 'codex');
  const [state, setState] = useState<ReviewLaunchState>('idle');
  const [error, setError] = useState<string | null>(null);
  const open = controlledOpen ?? internalOpen;

  const setOpen = (next: boolean) => {
    if (controlledOpen === undefined) setInternalOpen(next);
    onOpenChange?.(next);
  };

  const effectiveSelected = reviewers.some(({ id }) => id === selected)
    ? selected
    : (reviewers[0]?.id ?? selected);

  const close = () => {
    if (state === 'launching') return;
    setOpen(false);
    setState('idle');
    setError(null);
  };

  const start = async () => {
    setState('launching');
    setError(null);
    try {
      const response = await apiFetch(reviewRequestPath(origin, effectiveSelected), { method: 'POST' });
      if (!response.ok)
        throw new Error((await response.text()).trim() || `Review launch failed (${response.status})`);
      setOpen(false);
      setState('idle');
      onStarted();
    } catch (launchError) {
      setState('error');
      setError(launchError instanceof Error ? launchError.message : String(launchError));
    }
  };

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
        {showTrigger && (
          <DialogTrigger asChild>
            <Button
              size="xs"
              variant="outline"
              disabled={disabled || active || unavailableReason != null || reviewers.length === 0}
              title={
                unavailableReason ??
                (reviewers.length === 0 ? 'Install a supported agent to start a review.' : undefined)
              }
              onClick={(event) => event.stopPropagation()}
            >
              {active ? <LoaderCircleIcon className="animate-spin" /> : <ScanSearchIcon />}
              {active ? 'Review running' : reviewError ? 'Retry review' : 'Send for review'}
            </Button>
          </DialogTrigger>
        )}
        <DialogContent
          className="coslash-shell"
          onClick={(event) => event.stopPropagation()}
          onCloseAutoFocus={(event) => {
            if (returnFocusRef?.current == null) return;
            event.preventDefault();
            returnFocusRef.current.focus();
          }}
        >
          <ReviewDialogContent
            reviewers={reviewers}
            selected={effectiveSelected}
            state={state}
            error={state === 'idle' ? (reviewError ?? null) : error}
            onSelect={setSelected}
            onClose={close}
            onStart={() => void start()}
          />
        </DialogContent>
      </Dialog>
      {showTrigger && reviewError && (
        <span role="alert" className="text-danger-fg text-xs">
          {reviewError}
        </span>
      )}
    </>
  );
}
