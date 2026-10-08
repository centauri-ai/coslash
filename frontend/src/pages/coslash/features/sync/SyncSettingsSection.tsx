import { ExternalLink } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { SyncStatus } from './api';
import { syncSettingsView } from './model';

export function SyncSettingsSection({
  status,
  paused,
  onPauseChange,
}: {
  status: SyncStatus;
  paused: boolean;
  onPauseChange: (paused: boolean) => void;
}) {
  const view = syncSettingsView(status);
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 px-0.5">
        <span className="text-coslash-muted text-[11px] font-semibold tracking-widest uppercase">Hub sync</span>
        <span className="bg-coslash-line h-px flex-1" />
      </div>
      <div className="border-coslash-line bg-coslash-surface overflow-hidden rounded-xl border">
        {view.connected ? (
          <>
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 p-4 text-sm">
              <dt className="text-coslash-muted">Auto-sync</dt>
              <dd className="m-0 font-semibold">{view.autoLine}</dd>
            </dl>
            <div className="bg-coslash-soft flex items-center justify-between gap-3 p-3">
              <span className="text-sm">Auto-sync is managed in Hub.</span>
              {status.hubOrigin && (
                <a
                  href={status.hubOrigin}
                  target="_blank"
                  rel="noreferrer"
                  className="text-coslash-accent inline-flex shrink-0 items-center gap-1 text-sm font-semibold hover:underline"
                >
                  Manage in Hub <ExternalLink className="size-3" aria-hidden="true" />
                </a>
              )}
            </div>
            <div className="border-coslash-line flex items-start gap-3 border-t p-3">
              <button
                type="button"
                role="switch"
                aria-label="Pause syncing on this computer"
                aria-checked={paused}
                onClick={() => onPauseChange(!paused)}
                className={cn(
                  'focus-visible:ring-coslash-accent relative mt-px inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full p-0.5 outline-none focus-visible:ring-3',
                  { 'bg-coslash-accent justify-end': paused, 'bg-coslash-line justify-start': !paused },
                )}
              >
                <span className="bg-coslash-surface pointer-events-none size-4 rounded-full shadow-sm" />
              </button>
              <div className="flex min-w-0 flex-col gap-0.5">
                <span className="text-sm font-semibold">Pause syncing on this computer</span>
                <span className="text-coslash-muted text-xs leading-relaxed">
                  Nothing uploads from this computer until you turn this off. Share to Hub still works.
                </span>
              </div>
            </div>
          </>
        ) : (
          <div className="bg-coslash-soft flex items-center justify-between gap-3 p-3">
            <span className="text-sm">{view.notConnectedText}</span>
            {status.hubOrigin && (
              <a
                href={status.hubOrigin}
                target="_blank"
                rel="noreferrer"
                className="text-coslash-accent inline-flex shrink-0 items-center gap-1 text-sm font-semibold hover:underline"
              >
                Connect in Hub <ExternalLink className="size-3" aria-hidden="true" />
              </a>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
