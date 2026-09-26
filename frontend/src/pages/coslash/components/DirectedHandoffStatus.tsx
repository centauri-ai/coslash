import { Button } from '@/components/ui/button';
import { handoffLabel, type DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';

export function DirectedHandoffStatus({
  handoff,
  onOpenTarget,
  compact = false,
}: {
  handoff: DirectedHandoff;
  onOpenTarget?: (handoff: DirectedHandoff) => void;
  compact?: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs font-semibold">{handoffLabel(handoff)}</span>
        {handoff.targetSessionId && onOpenTarget && (
          <Button variant="outline" size="xs" onClick={() => onOpenTarget(handoff)}>
            Open target
          </Button>
        )}
      </div>
      {!compact && handoff.activity === 'not_detected' && handoff.status === 'running' && (
        <p role="status" className="text-warning-fg text-xs">
          Target not detected yet. Check the terminal; tracking will continue.
        </p>
      )}
      {!compact && handoff.error && (
        <p role="alert" className="text-danger-fg text-xs">
          {handoff.error}
        </p>
      )}
      {!compact &&
        handoff.status === 'completed' &&
        (handoff.result !== undefined || handoff.kind === 'review') && (
          <pre className="bg-coslash-soft max-h-64 overflow-auto rounded-lg p-3 text-xs wrap-break-word whitespace-pre-wrap">
            {handoff.result || 'No findings.'}
          </pre>
        )}
    </div>
  );
}
