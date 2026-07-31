import { MINUTE } from '@/pages/coslash/lib/time';

export function formatCost(usd: number): string {
  if (usd > 0 && usd < 0.01) return '<$0.01';
  return `$${usd.toFixed(2)}`;
}

export function formatDuration(ms: number | null): string {
  if (ms == null) return '—';
  const mins = Math.round(ms / MINUTE);
  if (mins < 1) return '<1m';
  if (mins < 60) return `${mins}m`;
  return `${Math.floor(mins / 60)}h ${String(mins % 60).padStart(2, '0')}m`;
}

export function formatTokens(count: number): string {
  if (count >= 1e6) return `${(count / 1e6).toFixed(2).replace(/\.?0+$/, '')}M`;
  return `${Math.round(count / 1000)}k`;
}

export function formatTimeAgo(mtime: number): string {
  const mins = Math.floor((Date.now() - mtime) / MINUTE);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days === 1) return 'yesterday';
  if (days < 7) return `${days}d ago`;
  return new Date(mtime).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
  });
}
