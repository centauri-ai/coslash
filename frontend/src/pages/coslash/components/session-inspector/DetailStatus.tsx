import { InfoIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { needsSourceRefresh, type DetailErrorKind } from '@/pages/coslash/lib/session-detail';

export function SnapshotStalenessNotice() {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            role="status"
            tabIndex={0}
            aria-label="Session data may be stale"
            className="text-warning-fg inline-flex w-fit items-center gap-1 text-xs"
          >
            <InfoIcon className="size-3" aria-hidden="true" />
            Snapshot may be stale
          </span>
        </TooltipTrigger>
        <TooltipContent>
          Activity recorded after this snapshot is not shown. Close and reopen to inspect newer details.
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function SnapshotRefreshStatus({
  isLoading,
  error,
  kind,
  onRetry,
  onRefresh,
}: {
  isLoading: boolean;
  error: string | null;
  kind?: DetailErrorKind | null;
  onRetry?: () => void;
  onRefresh?: () => void;
}) {
  if (error != null) {
    return (
      <div
        role="alert"
        className="text-destructive flex flex-wrap items-center justify-between gap-2 px-4 py-2 text-xs"
      >
        <span>Snapshot refresh failed: {error}</span>
        {kind != null && needsSourceRefresh(kind) && onRefresh != null ? (
          <Button variant="outline" size="sm" onClick={onRefresh}>
            Refresh sessions
          </Button>
        ) : onRetry != null && kind !== 'authentication' ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            Retry details
          </Button>
        ) : null}
      </div>
    );
  }
  return isLoading ? (
    <div role="status" className="text-muted-foreground px-4 py-2 text-xs">
      Refreshing snapshot…
    </div>
  ) : null;
}

export function SummaryOnlyBanner() {
  return (
    <div role="status" className="text-warning-fg bg-warning-bg mx-4 mb-2 rounded-sm px-3 py-2 text-xs">
      Complete details are unavailable. Showing the bounded summary from the session library; exact file diffs
      are disabled.
    </div>
  );
}

export function DetailLoadError({
  message,
  kind,
  onRetry,
  onRefresh,
}: {
  message: string;
  kind: DetailErrorKind;
  onRetry: () => void;
  onRefresh: () => void;
}) {
  const refreshSessions = needsSourceRefresh(kind);
  return (
    <div role="alert" className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
      <div className="text-danger-fg text-sm">{message}</div>
      {refreshSessions && (
        <Button variant="outline" size="sm" onClick={onRefresh}>
          Refresh sessions
        </Button>
      )}
      {kind === 'other' && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          Retry details
        </Button>
      )}
    </div>
  );
}
