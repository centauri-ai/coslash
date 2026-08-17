import { MINUTE } from '@/pages/coslash/lib/time';

export function formatEstimatedCost(usd: number): string {
  if (usd > 0 && usd < 0.01) return '<$0.01';
  return `≈$${usd.toFixed(2)}`;
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

export function formatDigestTime(time: number): string {
  return new Date(time).toLocaleTimeString(undefined, {
    hour: 'numeric',
    minute: '2-digit',
  });
}

export function digestDateKey(time: number): string {
  const d = new Date(time);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

export function formatDigestDateDivider(time: number): string {
  return new Date(time)
    .toLocaleDateString(undefined, {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
    })
    .toUpperCase();
}

export function formatDigestDateRange(times: number[]): string | null {
  const filtered = times.filter((t) => t > 0);
  if (filtered.length === 0) return null;

  const earliest = new Date(Math.min(...filtered));
  const latest = new Date(Math.max(...filtered));

  const fmt = (d: Date) =>
    d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });

  if (earliest.toDateString() === latest.toDateString()) {
    return `${fmt(earliest)}, ${earliest.getFullYear()} · local time`;
  }

  const year = earliest.getFullYear();
  if (earliest.getMonth() === latest.getMonth()) {
    return `${fmt(earliest)}–${latest.getDate()}, ${year} · local time`;
  }

  return `${fmt(earliest)} – ${fmt(latest)}, ${year} · local time`;
}
