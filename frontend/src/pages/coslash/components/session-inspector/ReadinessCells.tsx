import { useEffect, useState } from 'react';
import { CheckIcon, XIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { formatTimeAgo } from '@/pages/coslash/lib/format';
import { promptCacheTiming } from '@/pages/coslash/lib/time';

export function ReadinessCell({ label, value, tone }: { label: string; value?: string; tone?: string }) {
  return (
    <div className="bg-coslash-surface min-w-0 p-2">
      <div className="text-coslash-muted text-xs wrap-break-word">{label}</div>
      <div className={cn('pt-1 text-xs font-semibold wrap-break-word', tone)}>{value ?? '—'}</div>
    </div>
  );
}

function CacheWindowMark({ within, label }: { within: boolean; label: string }) {
  return (
    <span
      className={cn('flex items-center gap-1 text-xs', within ? 'text-success-fg' : 'text-coslash-muted')}
    >
      {within ? <CheckIcon className="size-3 shrink-0" /> : <XIcon className="size-3 shrink-0" />}
      {label}
    </span>
  );
}

// Cache TTL refreshes on every request, so warmth keys off the transcript's
// last write: within 5 min both windows hold, within 1 hr only the 1-hr one.
export function PromptCacheCell({ lastAccessAt, className }: { lastAccessAt: number; className?: string }) {
  const [now, setNow] = useState(Date.now);
  const { within5m, within1h, nextRefreshAt } = promptCacheTiming(lastAccessAt, now);

  useEffect(() => {
    if (nextRefreshAt == null) return;
    const timeout = globalThis.setTimeout(() => setNow(Date.now()), Math.max(0, nextRefreshAt - Date.now()));
    return () => globalThis.clearTimeout(timeout);
  }, [nextRefreshAt]);

  return (
    <div className={cn('bg-coslash-surface p-2', className)}>
      <div className="text-coslash-muted text-xs">Prompt cache</div>
      <div className="flex flex-wrap items-baseline gap-1 pt-1">
        <span className={cn('text-xs font-semibold', within1h ? 'text-success-fg' : 'text-warning-fg')}>
          {within1h ? 'warm' : 'cold'}
        </span>
        <span className="text-coslash-muted text-xs">{formatTimeAgo(lastAccessAt)}</span>
      </div>
      <div className="flex flex-wrap gap-2 pt-1">
        <CacheWindowMark within={within5m} label="5 min" />
        <CacheWindowMark within={within1h} label="1 hr" />
      </div>
    </div>
  );
}
