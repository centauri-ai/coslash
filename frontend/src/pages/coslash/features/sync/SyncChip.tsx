import { LoaderCircle } from 'lucide-react';
import { cn } from '@/lib/utils';
import { chipLabel, type SyncChipState } from './model';

export function SyncChip({ state, className }: { state: SyncChipState; className?: string }) {
  const label = chipLabel(state);
  return (
    <span
      title={label}
      className={cn(
        'border-coslash-line bg-coslash-surface text-coslash-muted inline-flex h-5 w-[88px] shrink-0 items-center justify-center gap-1 overflow-hidden rounded-full border px-1.5 text-[10px] leading-none font-semibold whitespace-nowrap',
        {
          'bg-success-bg text-success-fg': state === 'in_hub',
          'bg-info-bg text-info-fg': state === 'syncing',
          'bg-warning-bg text-warning-fg': state === 'left_out',
        },
        className,
      )}
    >
      {state === 'syncing' && <LoaderCircle className="size-3 shrink-0 animate-spin" aria-hidden="true" />}
      <span className="truncate">{label}</span>
    </span>
  );
}
