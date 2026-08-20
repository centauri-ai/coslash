import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { type HostStripModel } from '@/pages/coslash/lib/host-strip';

export function RemoteHostStrip({
  model,
  installationGuidePath,
  onRetry,
  onOpenDiagnostics,
}: {
  model: HostStripModel;
  installationGuidePath: string;
  onRetry: () => void;
  onOpenDiagnostics: () => void;
}) {
  return (
    <div
      role={model.role}
      className={cn(
        'flex items-center justify-between gap-3 border-b px-4 py-2 text-xs',
        model.tone === 'danger' ? 'bg-destructive/10 text-destructive' : 'bg-warning-bg text-warning-fg',
      )}
    >
      <span className="min-w-0 font-medium">{model.message}</span>
      <div className="flex shrink-0 items-center gap-2">
        {model.actions.includes('installation') && (
          <span className="text-[11px]">
            Installation guide · <code className="font-mono">{installationGuidePath}</code>
          </span>
        )}
        {model.actions.includes('diagnostics') && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            onClick={onOpenDiagnostics}
          >
            Diagnostics
          </Button>
        )}
        {model.actions.includes('retry') && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            disabled={model.retryDisabled}
            onClick={onRetry}
          >
            Retry
          </Button>
        )}
      </div>
    </div>
  );
}
