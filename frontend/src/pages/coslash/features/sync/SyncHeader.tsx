import { Settings2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { SyncStatus } from './api';
import { syncHeader } from './model';

export function SyncHeader({
  status,
  loaded,
  onSettings,
}: {
  status: SyncStatus;
  loaded: boolean;
  onSettings: () => void;
}) {
  if (!loaded) return null;
  const view = syncHeader(status);
  const statusClass = cn('text-cell inline-flex min-w-0 items-center gap-1.5 font-semibold', {
    'text-success-fg': status.state === 'connected_idle' || status.state === 'connected_syncing',
    'text-warning-fg': status.state === 'paused' || status.state === 'update_required',
    'text-danger-fg': status.state === 'disconnected',
    'text-coslash-muted': status.state === 'not_connected' || status.state === 'auto_sync_off',
  });
  const stateDot = cn('size-2 shrink-0 rounded-full', {
    'bg-success': status.state === 'connected_idle' || status.state === 'connected_syncing',
    'bg-warning': status.state === 'paused' || status.state === 'update_required',
    'bg-danger': status.state === 'disconnected',
    'bg-coslash-neutral-dot': status.state === 'not_connected' || status.state === 'auto_sync_off',
  });
  const content = (
    <>
      <span className={stateDot} aria-hidden="true" />
      <span className="truncate">{view.label}</span>
    </>
  );
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      {view.linked && view.href ? (
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
