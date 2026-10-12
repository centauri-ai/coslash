import { useState } from 'react';
import { ChevronDown, Settings2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { SyncStatus } from './api';
import { syncHeader } from './model';

export function SyncHeader({
  status,
  loaded,
  onSettings,
  onRetry,
}: {
  status: SyncStatus;
  loaded: boolean;
  onSettings: () => void;
  onRetry: () => Promise<void>;
}) {
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState<string | null>(null);
  if (!loaded) return null;
  const failure = status.hubFailureActive ? status.lastFailure : undefined;
  const view = syncHeader(status);
  const statusClass = cn('text-cell inline-flex min-w-0 items-center gap-1.5 font-semibold', {
    'text-success-fg':
      !failure && (status.state === 'connected_idle' || status.state === 'connected_syncing'),
    'text-warning-fg': failure != null || status.state === 'paused' || status.state === 'update_required',
    'text-danger-fg': !failure && status.state === 'disconnected',
    'text-coslash-muted': !failure && (status.state === 'not_connected' || status.state === 'auto_sync_off'),
  });
  const stateDot = cn('size-2 shrink-0 rounded-full', {
    'bg-success': !failure && (status.state === 'connected_idle' || status.state === 'connected_syncing'),
    'bg-warning': failure != null || status.state === 'paused' || status.state === 'update_required',
    'bg-danger': !failure && status.state === 'disconnected',
    'bg-coslash-neutral-dot':
      !failure && (status.state === 'not_connected' || status.state === 'auto_sync_off'),
  });
  const content = (
    <>
      <span className={stateDot} aria-hidden="true" />
      <span className="truncate">{view.label}</span>
    </>
  );
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      {failure ? (
        <details className="relative min-w-0">
          <summary className={cn(statusClass, 'max-w-full cursor-pointer list-none hover:underline')}>
            {content}
            <ChevronDown className="size-3 shrink-0" aria-hidden="true" />
          </summary>
          <div
            role="alert"
            className="border-coslash-line bg-coslash-surface text-coslash-text absolute top-full right-0 z-50 mt-2 grid w-80 max-w-[calc(100vw-2rem)] gap-3 rounded-md border p-4 text-sm shadow-lg"
          >
            <p>{failure.sentence}</p>
            <p className="text-coslash-muted text-xs break-words">Raw error: {failure.rawError}</p>
            {retryError && <p className="text-danger-fg">{retryError}</p>}
            <Button
              variant="outline"
              size="sm"
              disabled={retrying}
              onClick={async () => {
                setRetrying(true);
                setRetryError(null);
                try {
                  await onRetry();
                } catch {
                  setRetryError('Hub sync could not be retried. Try again.');
                } finally {
                  setRetrying(false);
                }
              }}
            >
              {retrying ? 'Checking…' : 'Retry now'}
            </Button>
          </div>
        </details>
      ) : view.linked && view.href ? (
        <a className={cn(statusClass, 'hover:underline')} href={view.href} target="_blank" rel="noreferrer">
          {content}
        </a>
      ) : (
        <span className={statusClass} role="status">
          {content}
        </span>
      )}
      <Button variant="ghost" size="icon" aria-label="Hub sync settings" onClick={onSettings}>
        <Settings2 aria-hidden="true" className="size-4" />
      </Button>
    </div>
  );
}
