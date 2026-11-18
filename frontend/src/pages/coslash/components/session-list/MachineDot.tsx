import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import {
  MACHINE_TONE_DOT,
  machineRetryable,
  machineStatusText,
  machineTone,
} from '@/pages/coslash/lib/machine-status';
import type { MachineFact } from '@/pages/coslash/lib/machines';

export function MachineDot({
  machine,
  onRetry,
  retrying,
}: {
  machine: MachineFact;
  onRetry: () => void;
  retrying: boolean;
}) {
  const tone = machineTone(machine);
  const retryable = machineRetryable(machine) && !retrying;
  const status = machineStatusText(machine);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn('size-[7px] shrink-0 rounded-full', MACHINE_TONE_DOT[tone])}
          role="img"
          aria-label={status}
        />
      </TooltipTrigger>
      {/* Portaled outside the shell, so the tokens have to be re-scoped here. */}
      <TooltipContent className="coslash-shell bg-coslash-surface text-coslash-ink border-coslash-line [&_svg]:bg-coslash-surface [&_svg]:fill-coslash-surface flex-col items-start gap-1 border">
        <span>{status}</span>
        {retrying && <span className="text-coslash-muted">Retrying…</span>}
        {retryable && (
          <button
            type="button"
            className="cursor-pointer font-semibold underline underline-offset-2"
            onClick={onRetry}
          >
            Retry the connection
          </button>
        )}
      </TooltipContent>
    </Tooltip>
  );
}
